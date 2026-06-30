// Package ipdb provides a gollem.ToolSet for AbuseIPDB IP address reputation
// and abuse-report lookups.
package ipdb

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

const defaultBaseURL = "https://api.abuseipdb.com/api/v2"

// ToolSet implements gollem.ToolSet for AbuseIPDB. Fields are unexported;
// configure via Option.
type ToolSet struct {
	apiKey     string
	baseURL    string
	client     *http.Client
	logger     *slog.Logger
	tools      []gollem.Tool
	toolByName map[string]gollem.Tool
}

// checkInput is the typed argument for the ipdb_check tool. The schema is
// inferred from this struct by gollem.NewTool, so it is the single source of
// truth — no separate hand-written parameter map is needed.
type checkInput struct {
	Target       string `json:"target" description:"The IP address to check" required:"true"`
	MaxAgeInDays int    `json:"maxAgeInDays" description:"The maximum age of reports in days (1-365)"`
}

// Startup assertions: a malformed input/output type (a broken struct tag, a
// non-object kind) is a programming error that should surface at init rather
// than on the first LLM call. See gollem docs "Validating Tool Types".
var _ = gollem.MustToolSchema[checkInput, map[string]any]()

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithBaseURL overrides the AbuseIPDB API base URL.
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

// New constructs the ToolSet. apiKey is the AbuseIPDB API key and is required.
// It only validates static configuration; use Ping to verify connectivity and
// credentials.
func New(apiKey string, opts ...Option) (*ToolSet, error) {
	if apiKey == "" {
		return nil, goerr.New("AbuseIPDB API key is required")
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

// buildTools constructs the typed AbuseIPDB lookup tool.
// MustNewTool is used because the In/Out types are static: a build failure is a
// programming error (already guarded by the package-level MustToolSchema), not a
// runtime condition New should report.
func (t *ToolSet) buildTools() []gollem.Tool {
	tool := gollem.MustNewTool("ipdb_check", "Check IP address information from AbuseIPDB.",
		func(ctx context.Context, in checkInput) (map[string]any, error) {
			if in.Target == "" {
				return nil, goerr.New("target is required", goerr.V("args", in))
			}
			return t.check(ctx, in.Target, in.MaxAgeInDays)
		})
	return []gollem.Tool{tool}
}

// Specs returns the AbuseIPDB tool specifications, derived from the typed tools.
func (t *ToolSet) Specs(ctx context.Context) ([]gollem.ToolSpec, error) {
	specs := make([]gollem.ToolSpec, len(t.tools))
	for i, tool := range t.tools {
		specs[i] = tool.Spec()
	}
	return specs, nil
}

// Run executes the named AbuseIPDB lookup by delegating to the matching typed tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	tool, ok := t.toolByName[name]
	if !ok {
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}
	return tool.Run(ctx, args)
}

// Ping verifies connectivity and credentials by querying a well-known IP
// address.
func (t *ToolSet) Ping(ctx context.Context) error {
	if _, err := t.check(ctx, "8.8.8.8", 0); err != nil {
		return goerr.Wrap(err, "AbuseIPDB ping failed")
	}
	return nil
}

func (t *ToolSet) check(ctx context.Context, ipAddress string, maxAgeInDays int) (map[string]any, error) {
	endpoint := t.baseURL + "/check"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create request", goerr.V("url", endpoint))
	}

	// Build query parameters.
	q := req.URL.Query()
	q.Add("ipAddress", ipAddress)
	if maxAgeInDays != 0 {
		q.Add("maxAgeInDays", fmt.Sprintf("%d", maxAgeInDays))
	}
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Key", t.apiKey)
	req.Header.Set("Accept", "application/json")

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
		return nil, eb.New("failed to query AbuseIPDB", goerr.V("body", string(body)))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, eb.Wrap(err, "failed to unmarshal response", goerr.V("body", string(body)))
	}

	return result, nil
}
