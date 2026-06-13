package github

// WithClientForTest injects a fake ghClient for unit testing without touching
// the network. This file is compiled only during tests.
func WithClientForTest(c ghClient) Option {
	return func(t *ToolSet) {
		t.client = c
		// Clear transport so Ping falls back to the fake-client path.
		t.transport = nil
	}
}
