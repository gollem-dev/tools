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

// whoisInput is the typed argument for all WHOIS lookup tools. The schema is
// inferred from struct tags by gollem.NewTool, making it the single source of
// truth for both the spec and the Run decode.
type whoisInput struct {
	Target string `json:"target" description:"The target (domain name or IP address) to look up" required:"true"`
}

// ToolSet implements gollem.ToolSet for WHOIS lookups. Fields are unexported;
// configure via Option.
type ToolSet struct {
	logger     *slog.Logger
	client     whoisClient
	tools      []gollem.Tool
	toolByName map[string]gollem.Tool
}

// Startup assertions: a malformed input/output type (a broken struct tag, a
// non-object kind) is a programming error that should surface at init rather
// than on the first LLM call. See gollem docs "Validating Tool Types".
var (
	_ = gollem.MustToolSchema[whoisInput, map[string]any]()
)

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

	t.tools = t.buildTools()
	t.toolByName = indexTools(t.tools)

	return t, nil
}

// indexTools builds a name->tool lookup so Run dispatches in O(1) instead of
// scanning (and re-deriving Spec()) on every call. The map is built once at
// construction and never mutated, so it is safe for concurrent Run calls.
func indexTools(tools []gollem.Tool) map[string]gollem.Tool {
	byName := make(map[string]gollem.Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Spec().Name] = tool
	}
	return byName
}

// buildTools constructs the typed WHOIS lookup tools. Both tools share the
// same whoisInput struct and implementation; they differ only in name and
// description.
// MustNewTool is used because the In/Out types are static: a build failure is a
// programming error (already guarded by the package-level MustToolSchema), not a
// runtime condition New should report.
func (t *ToolSet) buildTools() []gollem.Tool {
	defs := []struct{ name, desc string }{
		{
			"whois_domain",
			"Perform a WHOIS lookup for a domain name to retrieve registration information such as owner, registrar, registration date, and expiration date.",
		},
		{
			"whois_ip",
			"Perform a WHOIS lookup for an IP address (IPv4 or IPv6) to retrieve network registration information such as owner, ISP, and allocated range.",
		},
	}

	tools := make([]gollem.Tool, 0, len(defs))
	for _, d := range defs {
		name := d.name // capture per-iteration
		tool := gollem.MustNewTool(d.name, d.desc,
			func(ctx context.Context, in whoisInput) (map[string]any, error) {
				if in.Target == "" {
					return nil, goerr.New("target is required", goerr.V("name", name))
				}
				result, err := t.client.Whois(ctx, in.Target)
				if err != nil {
					return nil, goerr.Wrap(err, "failed to query whois", goerr.V("target", in.Target))
				}
				return map[string]any{"result": result}, nil
			})
		tools = append(tools, tool)
	}
	return tools
}

// Specs returns the WHOIS tool specifications, derived from the typed tools.
func (t *ToolSet) Specs(_ context.Context) ([]gollem.ToolSpec, error) {
	specs := make([]gollem.ToolSpec, len(t.tools))
	for i, tool := range t.tools {
		specs[i] = tool.Spec()
	}
	return specs, nil
}

// Run executes the named WHOIS lookup by delegating to the matching typed tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	tool, ok := t.toolByName[name]
	if !ok {
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}
	return tool.Run(ctx, args)
}

// Ping verifies basic WHOIS connectivity by querying a well-known IP address.
func (t *ToolSet) Ping(ctx context.Context) error {
	if _, err := t.client.Whois(ctx, "8.8.8.8"); err != nil {
		return goerr.Wrap(err, "whois ping failed")
	}
	return nil
}
