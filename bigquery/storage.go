package bigquery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"

	"cloud.google.com/go/storage"
	"github.com/gollem-dev/tools/internal/safe"
	"github.com/m-mizutani/goerr/v2"
)

// storageBackend abstracts the GCS operations used by the BigQuery tool so
// that tests can inject an in-memory substitute without a real GCS bucket.
type storageBackend interface {
	// WriteObject writes data to the named object, overwriting any existing
	// content. The writer must be closed by the caller.
	WriteObject(ctx context.Context, bucket, object string, data []byte) error
	// ReadObject returns the content of the named object, or
	// storage.ErrObjectNotExist if it does not exist.
	ReadObject(ctx context.Context, bucket, object string) ([]byte, error)
	// ObjectExists reports whether the named object exists.
	ObjectExists(ctx context.Context, bucket, object string) (bool, error)
}

// gcsStorageBackend is the production implementation that calls real GCS.
type gcsStorageBackend struct {
	client *storage.Client
	logger *slog.Logger
}

func (b *gcsStorageBackend) WriteObject(ctx context.Context, bucket, object string, data []byte) error {
	w := b.client.Bucket(bucket).Object(object).NewWriter(ctx)
	if _, err := io.Copy(w, bytes.NewReader(data)); err != nil {
		// The copy error is primary; close to release the writer and log any
		// secondary close error rather than masking the real failure.
		safe.Close(b.logger, w)
		return goerr.Wrap(err, "failed to write GCS object",
			goerr.V("bucket", bucket), goerr.V("object", object))
	}
	if err := w.Close(); err != nil {
		return goerr.Wrap(err, "failed to close GCS writer",
			goerr.V("bucket", bucket), goerr.V("object", object))
	}
	return nil
}

func (b *gcsStorageBackend) ReadObject(ctx context.Context, bucket, object string) ([]byte, error) {
	r, err := b.client.Bucket(bucket).Object(object).NewReader(ctx)
	if err != nil {
		return nil, err // preserve storage.ErrObjectNotExist
	}
	data, readErr := io.ReadAll(r)
	if closeErr := r.Close(); closeErr != nil && readErr == nil {
		return nil, goerr.Wrap(closeErr, "failed to close GCS reader",
			goerr.V("bucket", bucket), goerr.V("object", object))
	}
	if readErr != nil {
		return nil, goerr.Wrap(readErr, "failed to read GCS object",
			goerr.V("bucket", bucket), goerr.V("object", object))
	}
	return data, nil
}

func (b *gcsStorageBackend) ObjectExists(ctx context.Context, bucket, object string) (bool, error) {
	_, err := b.client.Bucket(bucket).Object(object).Attrs(ctx)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, storage.ErrObjectNotExist) {
		return false, nil
	}
	return false, goerr.Wrap(err, "failed to check GCS object existence",
		goerr.V("bucket", bucket), goerr.V("object", object))
}

// memStorageBackend is a thread-unsafe in-memory implementation for testing.
type memStorageBackend struct {
	objects map[string][]byte
}

func newMemStorageBackend() *memStorageBackend {
	return &memStorageBackend{objects: make(map[string][]byte)}
}

func (b *memStorageBackend) key(bucket, object string) string {
	return bucket + "/" + object
}

func (b *memStorageBackend) WriteObject(_ context.Context, bucket, object string, data []byte) error {
	cp := make([]byte, len(data))
	copy(cp, data)
	b.objects[b.key(bucket, object)] = cp
	return nil
}

func (b *memStorageBackend) ReadObject(_ context.Context, bucket, object string) ([]byte, error) {
	data, ok := b.objects[b.key(bucket, object)]
	if !ok {
		return nil, storage.ErrObjectNotExist
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp, nil
}

func (b *memStorageBackend) ObjectExists(_ context.Context, bucket, object string) (bool, error) {
	_, ok := b.objects[b.key(bucket, object)]
	return ok, nil
}

// encodeMetadata serialises a queryMetadata to JSON bytes.
func encodeMetadata(meta queryMetadata) ([]byte, error) {
	data, err := json.Marshal(meta)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to encode query metadata")
	}
	return data, nil
}

// decodeMetadata deserialises JSON bytes into a queryMetadata.
func decodeMetadata(data []byte) (queryMetadata, error) {
	var meta queryMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return queryMetadata{}, goerr.Wrap(err, "failed to decode query metadata")
	}
	return meta, nil
}
