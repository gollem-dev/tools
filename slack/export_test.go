package slack

import "time"

// WithRetryWaitForTest overrides the base retry backoff so rate-limit retry
// tests run without real delays. Test-only.
func WithRetryWaitForTest(d time.Duration) Option {
	return func(t *ToolSet) { t.retryWait = d }
}
