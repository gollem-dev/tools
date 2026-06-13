// Package whois provides a gollem.ToolSet for WHOIS domain and IP lookups.
package whois

import (
	"context"
	"log/slog"
	"time"

	"github.com/gollem-dev/gollem"
	whoislib "github.com/likexian/whois"
	"github.com/m-mizutani/goerr/v2"
)

// whoisClient abstracts the WHOIS query so tests can inject a fake without
// touching the network. It is context-aware so callers' deadlines/cancellation
// bound the lookup.
type whoisClient interface {
	Whois(ctx context.Context, query string, servers ...string) (string, error)
}

// defaultWhoisClient wraps the likexian whois client, applying the context
// deadline as the query timeout.
type defaultWhoisClient struct{}

func (defaultWhoisClient) Whois(ctx context.Context, query string, servers ...string) (string, error) {
	c := whoislib.NewClient()
	// likexian's client has no context support, so translate the context
	// deadline into its dial/read timeout. Without this, a caller's
	// timeout/cancel would not bound the WHOIS query.
	if deadline, ok := ctx.Deadline(); ok {
		c.SetTimeout(time.Until(deadline))
	}
	return c.Whois(query, servers...)
}

// ToolSet implements gollem.ToolSet for WHOIS lookups. Fields are unexported;
// configure via Option.
type ToolSet struct {
	logger *slog.Logger
	client whoisClient
}

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithLogger sets the logger. A nil logger keeps the default (slog.Default()).
func WithLogger(logger *slog.Logger) Option {
	return func(t *ToolSet) {
		if logger != nil {
			t.logger = logger
		}
	}
}

// New constructs the ToolSet. It only validates static configuration; use Ping
// to verify connectivity.
func New(opts ...Option) (*ToolSet, error) {
	t := &ToolSet{
		logger: slog.Default(),
		client: defaultWhoisClient{},
	}
	for _, opt := range opts {
		opt(t)
	}
	return t, nil
}

// Specs returns the WHOIS tool specifications.
func (t *ToolSet) Specs(_ context.Context) ([]gollem.ToolSpec, error) {
	return []gollem.ToolSpec{
		{
			Name:        "whois_domain",
			Description: "Perform a WHOIS lookup for a domain name to retrieve registration information such as owner, registrar, registration date, and expiration date.",
			Parameters: map[string]*gollem.Parameter{
				"target": {
					Type:        gollem.TypeString,
					Description: "The domain name to look up",
					Required:    true,
				},
			},
		},
		{
			Name:        "whois_ip",
			Description: "Perform a WHOIS lookup for an IP address (IPv4 or IPv6) to retrieve network registration information such as owner, ISP, and allocated range.",
			Parameters: map[string]*gollem.Parameter{
				"target": {
					Type:        gollem.TypeString,
					Description: "The IP address (IPv4 or IPv6) to look up",
					Required:    true,
				},
			},
		},
	}, nil
}

// Run executes the named WHOIS lookup.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	switch name {
	case "whois_domain", "whois_ip":
		// valid
	default:
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}

	target, ok := args["target"].(string)
	if !ok || target == "" {
		return nil, goerr.New("target is required", goerr.V("name", name), goerr.V("args", args))
	}

	result, err := t.client.Whois(ctx, target)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to query whois", goerr.V("target", target))
	}

	return map[string]any{
		"result": result,
	}, nil
}

// Ping verifies basic WHOIS connectivity by querying a well-known IP address.
func (t *ToolSet) Ping(ctx context.Context) error {
	if _, err := t.client.Whois(ctx, "8.8.8.8"); err != nil {
		return goerr.Wrap(err, "whois ping failed")
	}
	return nil
}
