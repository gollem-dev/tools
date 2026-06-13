package whois

// WithWhoisClientForTest injects a fake whoisClient for unit testing without
// touching the network. This file is compiled only during tests.
func WithWhoisClientForTest(c whoisClient) Option {
	return func(t *ToolSet) {
		t.client = c
	}
}
