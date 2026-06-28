// Package otx provides a gollem.ToolSet for AlienVault OTX threat intelligence
// lookups (IPv4/IPv6/domain/hostname/file hash).
package otx

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

const defaultBaseURL = "https://otx.alienvault.com/api/v1"

// ToolSet implements gollem.ToolSet for AlienVault OTX. Fields are unexported;
// configure via Option.
type ToolSet struct {
	apiKey  string
	baseURL string
	client  *http.Client
	logger  *slog.Logger
	tools   []gollem.Tool
}

// targetInput is the typed argument shared by every OTX lookup tool. The schema
// (and the runtime decode) is inferred from this struct, so there is no separate
// hand-written parameter map to drift from the Run implementation.
type targetInput struct {
	Target string `json:"target" description:"The indicator value to search (IPv4/IPv6/domain/hostname/file hash, depending on the tool)" required:"true"`
}

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithBaseURL overrides the OTX API base URL.
func WithBaseURL(baseURL string) Option {
	return func(t *ToolSet) {
		if baseURL != "" {
			t.baseURL = baseURL
		}
	}
}

// WithHTTPClient overrides the HTTP client used for requests.
func WithHTTPClient(client *http.Client) Option {
	return func(t *ToolSet) {
		if client != nil {
			t.client = client
		}
	}
}

// WithLogger sets the logger. A nil logger keeps the default (slog.Default()).
func WithLogger(logger *slog.Logger) Option {
	return func(t *ToolSet) {
		if logger != nil {
			t.logger = logger
		}
	}
}

// New constructs the ToolSet. apiKey is required; pass optional configuration
// via opts. It only validates static configuration — use Ping to verify
// connectivity and credentials.
func New(apiKey string, opts ...Option) (*ToolSet, error) {
	if apiKey == "" {
		return nil, goerr.New("OTX API key is required")
	}
	t := &ToolSet{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		client:  http.DefaultClient,
		logger:  slog.Default(),
	}
	for _, opt := range opts {
		opt(t)
	}
	if _, err := url.Parse(t.baseURL); err != nil {
		return nil, goerr.Wrap(err, "invalid base URL", goerr.V("base_url", t.baseURL))
	}

	tools, err := t.buildTools()
	if err != nil {
		return nil, goerr.Wrap(err, "failed to build OTX tools")
	}
	t.tools = tools

	return t, nil
}

// buildTools constructs the typed OTX lookup tools. Each tool shares targetInput
// but binds a distinct OTX indicator path segment, captured per closure.
func (t *ToolSet) buildTools() ([]gollem.Tool, error) {
	defs := []struct {
		name, desc, indicator string
	}{
		{"otx_ipv4", "Search the indicator of IPv4 from OTX.", "IPv4"},
		{"otx_domain", "Search the indicator of domain from OTX.", "domain"},
		{"otx_ipv6", "Search the indicator of IPv6 from OTX.", "IPv6"},
		{"otx_hostname", "Search the indicator of hostname from OTX.", "hostname"},
		{"otx_file_hash", "Search the indicator of file hash from OTX.", "file"},
	}

	tools := make([]gollem.Tool, 0, len(defs))
	for _, d := range defs {
		indicator := d.indicator
		tool, err := gollem.NewTool(d.name, d.desc,
			func(ctx context.Context, in targetInput) (map[string]any, error) {
				if in.Target == "" {
					return nil, goerr.New("target is required", goerr.V("indicator", indicator))
				}
				return t.query(ctx, indicator, in.Target)
			})
		if err != nil {
			return nil, goerr.Wrap(err, "failed to build tool", goerr.V("name", d.name))
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// Specs returns the OTX tool specifications, derived from the typed tools.
func (t *ToolSet) Specs(ctx context.Context) ([]gollem.ToolSpec, error) {
	specs := make([]gollem.ToolSpec, len(t.tools))
	for i, tool := range t.tools {
		specs[i] = tool.Spec()
	}
	return specs, nil
}

// Run executes the named OTX lookup by delegating to the matching typed tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	for _, tool := range t.tools {
		if tool.Spec().Name == name {
			return tool.Run(ctx, args)
		}
	}
	return nil, goerr.New("invalid function name", goerr.V("name", name))
}

// Ping verifies connectivity and credentials by querying a well-known IPv4
// indicator.
func (t *ToolSet) Ping(ctx context.Context) error {
	if _, err := t.query(ctx, "IPv4", "8.8.8.8"); err != nil {
		return goerr.Wrap(err, "OTX ping failed")
	}
	return nil
}

func (t *ToolSet) query(ctx context.Context, indicatorType, indicator string) (map[string]any, error) {
	endpoint := t.baseURL + "/indicators/" + indicatorType + "/" + url.PathEscape(indicator) + "/general"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create request", goerr.V("url", endpoint))
	}
	req.Header.Set("X-OTX-API-KEY", t.apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to send request", goerr.V("url", endpoint))
	}
	defer safeClose(t.logger, resp.Body)

	eb := goerr.NewBuilder(goerr.V("status", resp.StatusCode), goerr.V("url", endpoint))

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, eb.Wrap(err, "failed to read response body")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, eb.New("failed to query OTX", goerr.V("body", string(body)))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, eb.Wrap(err, "failed to unmarshal response", goerr.V("body", string(body)))
	}

	return result, nil
}
