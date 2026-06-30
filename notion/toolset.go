// Package notion provides a gollem.ToolSet for reading from Notion: full-text
// search across shared pages/databases, retrieving a page's content as
// Notion-flavored Markdown, and querying database rows.
//
// The tool talks to the Notion API directly over HTTP (no third-party SDK) so
// the module stays self-contained. A Notion integration token with read access,
// shared with the target pages/databases, is required.
package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

const (
	defaultBaseURL = "https://api.notion.com"

	// apiVersion is the stable Notion-Version used for the search and database
	// query endpoints. The database query endpoint is the legacy
	// /v1/databases/{id}/query; Notion deprecated it in favour of the
	// 2025-09-03+ /v1/data_sources/{id}/query model, but the legacy form remains
	// the broadly compatible choice for read access here.
	apiVersion = "2022-06-28"

	// markdownAPIVersion is the minimum Notion-Version that exposes the
	// GET /v1/pages/{id}/markdown endpoint.
	markdownAPIVersion = "2026-03-11"

	// httpTimeout caps every Notion API request. Without it, a stalled response
	// from Notion would let a goroutine hang indefinitely.
	httpTimeout = 30 * time.Second
)

// Tool names exposed by this ToolSet.
const (
	toolSearch        = "notion_search"
	toolGetPage       = "notion_get_page"
	toolQueryDatabase = "notion_query_database"
)

// ToolSet implements gollem.ToolSet for reading from Notion. Fields are
// unexported; configure via Option.
type ToolSet struct {
	token      string
	baseURL    string
	client     *http.Client
	logger     *slog.Logger
	tools      []gollem.Tool
	toolByName map[string]gollem.Tool
}

// Startup assertions: a malformed input/output type (a broken struct tag, a
// non-object kind) is a programming error that should surface at init rather
// than on the first LLM call. See gollem docs "Validating Tool Types".
var (
	_ = gollem.MustToolSchema[searchInput, map[string]any]()
	_ = gollem.MustToolSchema[getPageInput, map[string]any]()
	_ = gollem.MustToolSchema[queryDatabaseInput, map[string]any]()
)

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithBaseURL overrides the Notion API base URL (e.g. for testing). The value
// is the scheme+host without a trailing slash; endpoint paths are appended.
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

// New constructs the ToolSet. token is the Notion integration token and is
// required; pass optional configuration via opts. It only validates static
// configuration — use Ping to verify connectivity and credentials.
func New(token string, opts ...Option) (*ToolSet, error) {
	if token == "" {
		return nil, goerr.New("Notion API token is required")
	}
	t := &ToolSet{
		token:   token,
		baseURL: defaultBaseURL,
		client:  &http.Client{Timeout: httpTimeout},
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

// buildTools constructs the typed Notion tools. Each tool has its own input
// struct because their argument shapes differ.
// MustNewTool is used because the In/Out types are static: a build failure is a programming error (already guarded by the package-level MustToolSchema), not a runtime condition New should report.
func (t *ToolSet) buildTools() []gollem.Tool {
	searchTool := gollem.MustNewTool(
		toolSearch,
		"Search Notion pages and databases shared with the integration. "+
			"Matches titles against the query string. Returns id, type (page or database), title, URL, and last edited timestamp.",
		t.runSearch,
	)

	getPageTool := gollem.MustNewTool(
		toolGetPage,
		"Retrieve a Notion page's full content as Notion-flavored Markdown. "+
			"The integration must have access to the page. Returns the markdown body, a 'truncated' flag "+
			"(true when the page exceeds Notion's render limits), and 'unknown_block_ids' (the block IDs whose "+
			"subtrees were omitted when truncated; pass any of them as page_id to fetch the missing subtree).",
		t.runGetPage,
	)

	queryDatabaseTool := gollem.MustNewTool(
		toolQueryDatabase,
		"Query the rows (pages) of a Notion database shared with the integration. "+
			"Uses the legacy database query API (Notion-Version 2022-06-28); databases created under the "+
			"2025-09-03+ data-source model may not be addressable by this tool. "+
			"Returns each row's id, title, URL, last edited timestamp, and a flattened map of its "+
			"properties (title, text, number, select, multi_select, date, checkbox, url, email, phone, etc.).",
		t.runQueryDatabase,
	)

	return []gollem.Tool{searchTool, getPageTool, queryDatabaseTool}
}

// Specs returns the Notion tool specifications.
func (t *ToolSet) Specs(ctx context.Context) ([]gollem.ToolSpec, error) {
	specs := make([]gollem.ToolSpec, len(t.tools))
	for i, tool := range t.tools {
		specs[i] = tool.Spec()
	}
	return specs, nil
}

// Run dispatches to the named Notion tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	tool, ok := t.toolByName[name]
	if !ok {
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}
	return tool.Run(ctx, args)
}

// Ping verifies connectivity and credentials by issuing a minimal search.
func (t *ToolSet) Ping(ctx context.Context) error {
	var resp searchResponse
	if err := t.doJSON(ctx, http.MethodPost, "/v1/search", apiVersion,
		map[string]any{"page_size": 1}, &resp); err != nil {
		return goerr.Wrap(err, "Notion ping failed")
	}
	return nil
}

// doJSON performs an authenticated Notion API call. body (if non-nil) is JSON
// encoded as the request payload, and the response is JSON decoded into out (if
// non-nil). version selects the Notion-Version header.
func (t *ToolSet) doJSON(ctx context.Context, method, path, version string, body any, out any) error {
	endpoint := t.baseURL + path

	var reqBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return goerr.Wrap(err, "failed to encode request body", goerr.V("path", path))
		}
		reqBody = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return goerr.Wrap(err, "failed to create request", goerr.V("url", endpoint))
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Notion-Version", version)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return goerr.Wrap(err, "failed to send request", goerr.V("url", endpoint))
	}
	defer safeClose(t.logger, resp.Body)

	eb := goerr.NewBuilder(goerr.V("status", resp.StatusCode), goerr.V("url", endpoint))

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return eb.Wrap(err, "failed to read response body")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Cap the echoed body: Notion error payloads are small, but a proxy could
		// return a large HTML page.
		snippet := respBody
		if len(snippet) > 4096 {
			snippet = snippet[:4096]
		}
		return eb.New("Notion API returned non-2xx", goerr.V("body", string(snippet)))
	}

	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return eb.Wrap(err, "failed to unmarshal response", goerr.V("body", string(respBody)))
		}
	}
	return nil
}
