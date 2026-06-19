package jira_test

import (
	"errors"
	"testing"

	"github.com/gollem-dev/tools/jira"
	"github.com/m-mizutani/gt"
)

type errCloser struct{ called *bool }

func (c errCloser) Close() error {
	*c.called = true
	return errors.New("close failed")
}

func TestSafeClose(t *testing.T) {
	t.Run("nil closer is a no-op", func(t *testing.T) {
		// Must not panic.
		jira.SafeClose(nil, nil)
	})

	t.Run("closes the closer", func(t *testing.T) {
		called := false
		jira.SafeClose(nil, errCloser{called: &called})
		gt.True(t, called)
	})
}
