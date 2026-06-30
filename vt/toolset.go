// Package vt provides a gollem.ToolSet for VirusTotal threat-intelligence
// lookups (IP addresses, domains, file hashes, and URLs).
package vt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

const defaultBaseURL = "https://www.virustotal.com/api/v3"

// ToolSet implements gollem.ToolSet for VirusTotal. Fields are unexported;
// configure via Option.
type ToolSet struct {
	apiKey     string
	baseURL    string
	client     *http.Client
	logger     *slog.Logger
	tools      []gollem.Tool
	toolByName map[string]gollem.Tool
}

// targetInput is the typed argument shared by every VT lookup tool. The schema
// (and the runtime decode) is inferred from this struct, so there is no
// separate hand-written parameter map to drift from the Run implementation.
type targetInput struct {
	Target string `json:"target" description:"The indicator value to search" required:"true"`
}

// Startup assertions: a malformed input/output type (a broken struct tag, a
// non-object kind) is a programming error that should surface at init rather
// than on the first LLM call. See gollem docs "Validating Tool Types".
var (
	_ = gollem.MustToolSchema[targetInput, map[string]any]()
)

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithBaseURL overrides the VirusTotal API base URL.
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

// New constructs the ToolSet. apiKey is required; New returns an error if it is
// empty. It only validates static configuration; use Ping to verify
// connectivity and credentials.
func New(apiKey string, opts ...Option) (*ToolSet, error) {
	if apiKey == "" {
		return nil, goerr.New("VirusTotal API key is required")
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

// buildTools constructs the typed VirusTotal lookup tools. Each tool shares
// targetInput but binds a distinct query function, captured per closure.
// MustNewTool is used because the In/Out types are static: a build failure is a
// programming error (already guarded by the package-level MustToolSchema), not a
// runtime condition New should report.
func (t *ToolSet) buildTools() []gollem.Tool {
	type def struct {
		name, desc string
		queryFn    func(ctx context.Context, target string) (map[string]any, error)
	}
	defs := []def{
		{
			name: "vt_ip",
			desc: "Search the indicator of IPv4/IPv6 from VirusTotal.",
			queryFn: func(ctx context.Context, target string) (map[string]any, error) {
				return t.query(ctx, "ip_addresses", target)
			},
		},
		{
			name: "vt_domain",
			desc: "Search the indicator of domain from VirusTotal.",
			queryFn: func(ctx context.Context, target string) (map[string]any, error) {
				return t.query(ctx, "domains", target)
			},
		},
		{
			name: "vt_file_hash",
			desc: "Search the indicator of file hash from VirusTotal.",
			queryFn: func(ctx context.Context, target string) (map[string]any, error) {
				return t.query(ctx, "files", target)
			},
		},
		{
			// VirusTotal's /urls/{id} endpoint requires the URL to be encoded as
			// base64url (RawURLEncoding, no padding) rather than percent-escaped.
			name: "vt_url",
			desc: "Search the indicator of URL from VirusTotal.",
			queryFn: func(ctx context.Context, target string) (map[string]any, error) {
				id := base64.RawURLEncoding.EncodeToString([]byte(target))
				return t.queryRaw(ctx, "urls/"+id)
			},
		},
	}

	tools := make([]gollem.Tool, 0, len(defs))
	for _, d := range defs {
		queryFn := d.queryFn
		tool := gollem.MustNewTool(d.name, d.desc,
			func(ctx context.Context, in targetInput) (map[string]any, error) {
				if in.Target == "" {
					return nil, goerr.New("target is required")
				}
				return queryFn(ctx, in.Target)
			})
		tools = append(tools, tool)
	}
	return tools
}

// Specs returns the VirusTotal tool specifications, derived from the typed tools.
func (t *ToolSet) Specs(ctx context.Context) ([]gollem.ToolSpec, error) {
	specs := make([]gollem.ToolSpec, len(t.tools))
	for i, tool := range t.tools {
		specs[i] = tool.Spec()
	}
	return specs, nil
}

// Run executes the named VirusTotal lookup by delegating to the matching typed tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	tool, ok := t.toolByName[name]
	if !ok {
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}
	return tool.Run(ctx, args)
}

// Ping verifies connectivity and credentials by querying a well-known IP
// address indicator.
func (t *ToolSet) Ping(ctx context.Context) error {
	if _, err := t.query(ctx, "ip_addresses", "8.8.8.8"); err != nil {
		return goerr.Wrap(err, "VirusTotal ping failed")
	}
	return nil
}

// query builds an endpoint from indicatorType and a percent-escaped indicator
// segment, then delegates to queryRaw.
func (t *ToolSet) query(ctx context.Context, indicatorType, indicator string) (map[string]any, error) {
	return t.queryRaw(ctx, indicatorType+"/"+url.PathEscape(indicator))
}

// queryRaw fetches the given path (relative to baseURL) from the VirusTotal API.
func (t *ToolSet) queryRaw(ctx context.Context, path string) (map[string]any, error) {
	endpoint := t.baseURL + "/" + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create request", goerr.V("url", endpoint))
	}
	req.Header.Set("x-apikey", t.apiKey)

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
		return nil, eb.New("failed to query VirusTotal", goerr.V("body", string(body)))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, eb.Wrap(err, "failed to unmarshal response", goerr.V("body", string(body)))
	}

	return result, nil
}
