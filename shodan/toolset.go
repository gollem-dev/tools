// Package shodan provides a gollem.ToolSet for Shodan internet-facing asset
// and service lookups (host, domain, search).
package shodan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

const defaultBaseURL = "https://api.shodan.io"

// ToolSet implements gollem.ToolSet for Shodan. Fields are unexported;
// configure via Option.
type ToolSet struct {
	apiKey     string
	baseURL    string
	client     *http.Client
	logger     *slog.Logger
	tools      []gollem.Tool
	toolByName map[string]gollem.Tool
}

// hostInput is the typed argument for shodan_host.
type hostInput struct {
	Target string `json:"target" description:"The IP address to search" required:"true"`
}

// domainInput is the typed argument for shodan_domain.
type domainInput struct {
	Target string `json:"target" description:"The domain to search" required:"true"`
}

// searchInput is the typed argument for shodan_search.
type searchInput struct {
	Query string `json:"query" description:"The search query to use" required:"true"`
	Limit int    `json:"limit" description:"Maximum number of results to return (default: 100)"`
}

// Startup assertions: a malformed input/output type (a broken struct tag, a
// non-object kind) is a programming error that should surface at init rather
// than on the first LLM call. See gollem docs "Validating Tool Types".
var (
	_ = gollem.MustToolSchema[hostInput, map[string]any]()
	_ = gollem.MustToolSchema[domainInput, map[string]any]()
	_ = gollem.MustToolSchema[searchInput, map[string]any]()
)

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithBaseURL overrides the Shodan API base URL.
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

// New constructs the ToolSet. It only validates static configuration; use Ping
// to verify connectivity and credentials.
func New(apiKey string, opts ...Option) (*ToolSet, error) {
	if apiKey == "" {
		return nil, goerr.New("Shodan API key is required")
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

// buildTools constructs the typed Shodan lookup tools. Each tool captures the
// ToolSet receiver so the handlers can reach the client and API key.
// MustNewTool is used because the In/Out types are static: a build failure is a
// programming error (already guarded by the package-level MustToolSchema), not a
// runtime condition New should report.
func (t *ToolSet) buildTools() []gollem.Tool {
	hostTool := gollem.MustNewTool("shodan_host", "Search the host information from Shodan.",
		func(ctx context.Context, in hostInput) (map[string]any, error) {
			if in.Target == "" {
				return nil, goerr.New("target is required", goerr.V("name", "shodan_host"))
			}
			params := url.Values{}
			params.Set("key", t.apiKey)
			endpoint := fmt.Sprintf("%s/shodan/host/%s", t.baseURL, url.PathEscape(in.Target))
			return t.doGet(ctx, endpoint, params)
		})

	domainTool := gollem.MustNewTool("shodan_domain", "Search the domain information from Shodan.",
		func(ctx context.Context, in domainInput) (map[string]any, error) {
			if in.Target == "" {
				return nil, goerr.New("target is required", goerr.V("name", "shodan_domain"))
			}
			params := url.Values{}
			params.Set("key", t.apiKey)
			endpoint := fmt.Sprintf("%s/dns/domain/%s", t.baseURL, url.PathEscape(in.Target))
			return t.doGet(ctx, endpoint, params)
		})

	searchTool := gollem.MustNewTool("shodan_search", "Search the internet using Shodan search query.",
		func(ctx context.Context, in searchInput) (map[string]any, error) {
			if in.Query == "" {
				return nil, goerr.New("query is required", goerr.V("name", "shodan_search"))
			}
			params := url.Values{}
			params.Set("key", t.apiKey)
			params.Set("query", in.Query)
			if in.Limit != 0 {
				params.Set("limit", fmt.Sprintf("%d", in.Limit))
			}
			endpoint := fmt.Sprintf("%s/shodan/host/search", t.baseURL)
			return t.doGet(ctx, endpoint, params)
		})

	return []gollem.Tool{hostTool, domainTool, searchTool}
}

// doGet executes a GET request to endpoint with the given query params and
// returns the decoded JSON response body.
func (t *ToolSet) doGet(ctx context.Context, endpoint string, params url.Values) (map[string]any, error) {
	fullURL := endpoint + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create request", goerr.V("url", fullURL))
	}

	eb := goerr.NewBuilder(goerr.V("url", endpoint))

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, eb.Wrap(err, "failed to send request")
	}
	defer safeClose(t.logger, resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, eb.Wrap(err, "failed to read response body", goerr.V("status", resp.StatusCode))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, eb.New("failed to query Shodan",
			goerr.V("status", resp.StatusCode),
			goerr.V("body", string(body)))
	}

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, eb.Wrap(err, "failed to unmarshal response", goerr.V("body", string(body)))
	}

	// Surface API-level errors embedded in a successful HTTP 200 response.
	if errMsg, ok := data["error"].(string); ok {
		return nil, eb.New("Shodan API returned error", goerr.V("error", errMsg))
	}

	return data, nil
}

// Specs returns the Shodan tool specifications, derived from the typed tools.
func (t *ToolSet) Specs(ctx context.Context) ([]gollem.ToolSpec, error) {
	specs := make([]gollem.ToolSpec, len(t.tools))
	for i, tool := range t.tools {
		specs[i] = tool.Spec()
	}
	return specs, nil
}

// Run executes the named Shodan lookup by delegating to the matching typed tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	tool, ok := t.toolByName[name]
	if !ok {
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}
	return tool.Run(ctx, args)
}

// Ping verifies connectivity and credentials by querying the well-known Google
// DNS host (8.8.8.8) via shodan_host.
func (t *ToolSet) Ping(ctx context.Context) error {
	params := url.Values{}
	params.Set("key", t.apiKey)
	endpoint := fmt.Sprintf("%s/shodan/host/%s", t.baseURL, "8.8.8.8")
	fullURL := endpoint + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return goerr.Wrap(err, "Shodan ping: failed to create request")
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return goerr.Wrap(err, "Shodan ping failed")
	}
	defer safeClose(t.logger, resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return goerr.Wrap(err, "Shodan ping: failed to read response body")
	}

	if resp.StatusCode != http.StatusOK {
		return goerr.New("Shodan ping: unexpected status",
			goerr.V("status", resp.StatusCode),
			goerr.V("body", string(body)))
	}

	return nil
}
