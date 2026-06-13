package bigquery

import (
	"context"
	"fmt"
	"testing"
)

// SafeClose exposes the package-internal safeClose helper for unit testing.
var SafeClose = safeClose

// WithClientFactoryForTest injects a custom bigQueryClientFactory into the
// ToolSet. It is intentionally unexported outside this package and only
// accessible via the export_test.go file so that production code cannot reach
// the test seam.
func WithClientFactoryForTest(f bigQueryClientFactory) Option {
	return func(t *ToolSet) { t.clientFactory = f }
}

// MockClientFactory re-exports the unexported mockBigQueryClientFactory so that
// external test packages (package bigquery_test) can construct one.
type MockClientFactory = mockBigQueryClientFactory

// MockClient re-exports the unexported mockBigQueryClient.
type MockClient = mockBigQueryClient

// NewMockClient constructs a mockBigQueryClient with initialised maps.
func NewMockClient() *mockBigQueryClient {
	return newMockBigQueryClient()
}

// RunbookIDsForTest returns all runbook IDs registered in the ToolSet. This
// is only available to tests so they can retrieve a valid ID to pass to
// get_runbook_entry without coupling to the random UUID generation.
func RunbookIDsForTest(ts *ToolSet) []string {
	ids := make([]string, 0, len(ts.runbooks))
	for id := range ts.runbooks {
		ids = append(ids, string(id))
	}
	return ids
}

// WithStorageForTest injects an in-memory storageBackend so that tests can
// exercise the bigquery_query / bigquery_result flow without a real GCS bucket.
func WithStorageForTest(s storageBackend) Option {
	return func(t *ToolSet) { t.storage = s }
}

// NewMemStorageForTest creates a fresh in-memory storageBackend for use in
// tests.
func NewMemStorageForTest() storageBackend {
	return newMemStorageBackend()
}

// WriteStorageForTest writes data to the named bucket/object path on the given
// storageBackend. It is a test helper that eliminates boilerplate.
func WriteStorageForTest(tb testing.TB, s storageBackend, bucket, object string, data []byte) {
	tb.Helper()
	if err := s.WriteObject(context.Background(), bucket, object, data); err != nil {
		tb.Fatalf("WriteStorageForTest: %v", err)
	}
}

// ResultPathForTest returns the GCS object path for a query result data file.
// This mirrors toResultStoragePath("") with an empty prefix and allows tests
// to pre-populate the cache.
func ResultPathForTest(queryID string) string {
	return fmt.Sprintf("bigquery/%s/data.json", queryID)
}
