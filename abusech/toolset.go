// Package abusech provides a gollem.ToolSet for abuse.ch MalwareBazaar hash
// lookups.
package abusech

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

const defaultBaseURL = "https://mb-api.abuse.ch/api/v1"

// ToolSet implements gollem.ToolSet for abuse.ch MalwareBazaar. Fields are
// unexported; configure via Option.
type ToolSet struct {
	apiKey     string
	baseURL    string
	client     *http.Client
	logger     *slog.Logger
	tools      []gollem.Tool
	toolByName map[string]gollem.Tool
}

// hashInput is the typed argument for the MalwareBazaar query tool. The schema
// is inferred from this struct by gollem.NewTool, eliminating the hand-written
// parameter map and the args["hash"].(string) assertion in Run.
type hashInput struct {
	Hash string `json:"hash" description:"The hash value (MD5, SHA1, or SHA256) to query" required:"true"`
}

// Startup assertions: a malformed input/output type (a broken struct tag, a
// non-object kind) is a programming error that should surface at init rather
// than on the first LLM call. See gollem docs "Validating Tool Types".
var _ = gollem.MustToolSchema[hashInput, map[string]any]()

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithBaseURL overrides the MalwareBazaar API base URL.
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

// New constructs the ToolSet. apiKey is required; pass functional opts for
// optional configuration. It only validates static configuration; use Ping
// to verify connectivity and credentials.
func New(apiKey string, opts ...Option) (*ToolSet, error) {
	if apiKey == "" {
		return nil, goerr.New("abuse.ch API key is required")
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

// buildTools constructs the typed MalwareBazaar lookup tool. The schema is
// derived from hashInput, making it the single source of truth for both the
// spec and the runtime argument decode.
// MustNewTool is used because the In/Out types are static: a build failure is a
// programming error (already guarded by the package-level MustToolSchema), not a
// runtime condition New should report.
func (t *ToolSet) buildTools() []gollem.Tool {
	tool := gollem.MustNewTool(
		"abusech.bazaar.query",
		"Query malware information from MalwareBazaar by file hash value.",
		func(ctx context.Context, in hashInput) (map[string]any, error) {
			if in.Hash == "" {
				return nil, goerr.New("hash is required")
			}
			return t.query(ctx, in.Hash)
		},
	)
	return []gollem.Tool{tool}
}

// Specs returns the MalwareBazaar tool specifications, derived from the typed tools.
func (t *ToolSet) Specs(ctx context.Context) ([]gollem.ToolSpec, error) {
	specs := make([]gollem.ToolSpec, len(t.tools))
	for i, tool := range t.tools {
		specs[i] = tool.Spec()
	}
	return specs, nil
}

// Run executes the named MalwareBazaar lookup by delegating to the matching typed tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	tool, ok := t.toolByName[name]
	if !ok {
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}
	return tool.Run(ctx, args)
}

// Ping verifies connectivity and credentials by querying a well-known SHA256
// hash (Mirai malware sample present in MalwareBazaar).
func (t *ToolSet) Ping(ctx context.Context) error {
	// Use a known hash that exists in MalwareBazaar. We do not validate the
	// returned data — a successful HTTP 200 response is sufficient.
	if _, err := t.query(ctx, "b3d9ea5fb70f1b6ecf74f36e3a88da0ac44dd8afb4a4d70e8f15fa10f6fa3f7b"); err != nil {
		return goerr.Wrap(err, "abuse.ch ping failed")
	}
	return nil
}

// query posts a get_info request to MalwareBazaar for the given hash value.
func (t *ToolSet) query(ctx context.Context, hash string) (map[string]any, error) {
	endpoint := t.baseURL + "/"

	formData := url.Values{}
	formData.Set("query", "get_info")
	formData.Set("hash", hash)
	body := formData.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create request", goerr.V("url", endpoint))
	}
	req.Header.Set("Auth-Key", t.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	eb := goerr.NewBuilder(goerr.V("url", endpoint), goerr.V("hash", hash))

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, eb.Wrap(err, "failed to send request")
	}
	defer safeClose(t.logger, resp.Body)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, eb.Wrap(err, "failed to read response body", goerr.V("status", resp.StatusCode))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, eb.New("unexpected status from MalwareBazaar",
			goerr.V("status", resp.StatusCode),
			goerr.V("body", string(respBody)))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, eb.Wrap(err, "failed to unmarshal response", goerr.V("body", string(respBody)))
	}

	// Surface API-level errors reported inside the JSON payload.
	if status, ok := result["query_status"].(string); ok && status == "error" {
		errMsg, _ := result["error_message"].(string)
		if errMsg == "" {
			errMsg, _ = result["error"].(string)
		}
		return nil, eb.New("MalwareBazaar API returned error", goerr.V("error", errMsg))
	}

	return result, nil
}
