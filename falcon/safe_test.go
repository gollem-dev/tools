package falcon_test

import (
	"errors"
	"testing"

	"github.com/gollem-dev/tools/falcon"
	"github.com/m-mizutani/gt"
)

type errCloser struct{ err error }

func (c errCloser) Close() error { return c.err }

func TestSafeClose(t *testing.T) {
	t.Run("nil closer is a no-op", func(t *testing.T) {
		falcon.SafeClose(nil, nil) // must not panic
	})

	t.Run("closes without error", func(t *testing.T) {
		falcon.SafeClose(nil, errCloser{err: nil})
	})

	t.Run("logs on error without panicking", func(t *testing.T) {
		falcon.SafeClose(nil, errCloser{err: errors.New("boom")})
	})
}

func TestSplitAndTrim(t *testing.T) {
	gt.Array(t, falcon.SplitAndTrim("a, b ,, c")).Equal([]string{"a", "b", "c"})
	gt.Array(t, falcon.SplitAndTrim("   ")).Length(0)
	gt.Array(t, falcon.SplitAndTrim("only")).Equal([]string{"only"})
}
