package shodan_test

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"

	"github.com/gollem-dev/tools/shodan"
	"github.com/m-mizutani/gt"
)

type stubCloser struct {
	err    error
	called bool
}

func (c *stubCloser) Close() error {
	c.called = true
	return c.err
}

func TestSafeCloseNilCloser(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	// A nil io.Closer must be a no-op and must not panic.
	shodan.SafeClose(logger, nil)

	gt.String(t, buf.String()).Equal("")
}

func TestSafeCloseSuccess(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	c := &stubCloser{err: nil}
	shodan.SafeClose(logger, c)

	gt.Bool(t, c.called).True()
	gt.String(t, buf.String()).Equal("")
}

func TestSafeCloseError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	c := &stubCloser{err: errors.New("boom")}
	shodan.SafeClose(logger, c)

	gt.Bool(t, c.called).True()
	gt.String(t, buf.String()).
		Contains("failed to close resource").
		Contains("boom")
}

func TestSafeCloseNilLoggerDoesNotPanic(t *testing.T) {
	// A nil logger must fall back to the default logger without panicking.
	c := &stubCloser{err: errors.New("boom")}
	shodan.SafeClose(nil, c)

	gt.Bool(t, c.called).True()
}
