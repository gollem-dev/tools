// Package falcon provides a gollem.ToolSet for read-only CrowdStrike Falcon
// queries: incidents, alerts, behaviors, devices, CrowdScores, and raw EDR
// telemetry events (Next-Gen SIEM).
//
// To keep large Falcon result sets from overwhelming an LLM's context, every
// search tool returns at most maxRecords records per call and keeps the
// overflow (up to maxFetchRecords) in an in-memory page store. The LLM fetches
// subsequent pages by passing the returned page_token back to the same tool;
// no Falcon round-trip is made for cached pages.
package falcon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

// --- Typed input structs for each tool ---
// The json tag name on each field matches the old parameter key; description
// and required mirror the old Parameter definition. The schema is inferred by
// gollem.NewTool, eliminating the hand-written parameter map.

// searchIncidentsInput is the typed argument for falcon_search_incidents.
type searchIncidentsInput struct {
	Filter    string  `json:"filter" description:"FQL filter expression (e.g., \"status:'30'\", \"tags:'critical'\", \"start:>'2025-01-01'\")"`
	Sort      string  `json:"sort" description:"Sort expression (e.g., \"start.desc\", \"end.asc\")"`
	Limit     float64 `json:"limit" description:"Page size: maximum records to return in this page. Use page_token to fetch further pages."`
	PageToken string  `json:"page_token" description:"Opaque token from a previous response's page_token. When set, returns the next page from memory and ignores the other search parameters."`
}

// getIncidentsInput is the typed argument for falcon_get_incidents.
type getIncidentsInput struct {
	IDs string `json:"ids" description:"Comma-separated incident IDs (e.g., \"inc:abc123:def456,inc:abc123:ghi789\"). At most maxRecords IDs are fetched per call." required:"true"`
}

// searchAlertsInput is the typed argument for falcon_search_alerts.
type searchAlertsInput struct {
	Filter    string  `json:"filter" description:"FQL filter expression (e.g., \"status:'new'\", \"severity:>50\", \"tactics:'Lateral Movement'\")"`
	Sort      string  `json:"sort" description:"Sort property (e.g., \"timestamp|desc\", \"severity|asc\")"`
	Limit     float64 `json:"limit" description:"Page size: maximum records to return in this page. Use page_token to fetch further pages."`
	PageToken string  `json:"page_token" description:"Opaque token from a previous response's page_token. When set, returns the next page from memory and ignores the other search parameters."`
}

// getAlertsInput is the typed argument for falcon_get_alerts.
type getAlertsInput struct {
	CompositeIDs string `json:"composite_ids" description:"Comma-separated composite alert IDs. At most maxRecords IDs are fetched per call." required:"true"`
}

// searchBehaviorsInput is the typed argument for falcon_search_behaviors.
type searchBehaviorsInput struct {
	Filter    string  `json:"filter" description:"FQL filter expression"`
	Limit     float64 `json:"limit" description:"Page size: maximum records to return in this page. Use page_token to fetch further pages."`
	PageToken string  `json:"page_token" description:"Opaque token from a previous response's page_token. When set, returns the next page from memory and ignores the other search parameters."`
}

// getBehaviorsInput is the typed argument for falcon_get_behaviors.
type getBehaviorsInput struct {
	IDs string `json:"ids" description:"Comma-separated behavior IDs. At most maxRecords IDs are fetched per call." required:"true"`
}

// searchDevicesInput is the typed argument for falcon_search_devices.
type searchDevicesInput struct {
	Filter    string  `json:"filter" description:"FQL filter expression (e.g., \"hostname:'*web*'\", \"platform_name:'Windows'\", \"last_seen:>='2025-01-01'\")"`
	Sort      string  `json:"sort" description:"Sort expression (e.g., \"hostname.asc\", \"last_seen.desc\")"`
	Limit     float64 `json:"limit" description:"Page size: maximum records to return in this page. Use page_token to fetch further pages."`
	PageToken string  `json:"page_token" description:"Opaque token from a previous response's page_token. When set, returns the next page from memory and ignores the other search parameters."`
}

// getDevicesInput is the typed argument for falcon_get_devices.
type getDevicesInput struct {
	IDs string `json:"ids" description:"Comma-separated device IDs. At most maxRecords IDs are fetched per call." required:"true"`
}

// getCrowdScoresInput is the typed argument for falcon_get_crowdscores.
type getCrowdScoresInput struct {
	Filter string `json:"filter" description:"FQL filter expression (e.g., \"timestamp:>'2025-01-01'\")"`
}

// searchEventsInput is the typed argument for falcon_search_events.
// query_string is not marked required in the spec (matching the old Parameter
// definition), but the handler validates it explicitly to preserve the
// "query_string is required" error that direct ToolSet.Run callers expect.
type searchEventsInput struct {
	QueryString string `json:"query_string" description:"CQL query string (e.g., \"aid=abc123\", \"#event_simpleName=ProcessRollup2 AND FileName=cmd.exe\", \"ComputerName=workstation1 | tail(100)\")"`
	Repository  string `json:"repository" description:"Repository to search. Values: \"search-all\" (default), \"investigate_view\" (Falcon EDR), \"third-party\", \"falcon_for_it_view\", \"forensics_view\""`
	Start       string `json:"start" description:"Start time (e.g., \"1d\", \"24h\", \"2025-01-01T00:00:00Z\"). Default: \"1d\""`
	End         string `json:"end" description:"End time (e.g., \"now\", \"2025-01-02T00:00:00Z\"). Default: \"now\""`
	PageToken   string `json:"page_token" description:"Opaque token from a previous response's page_token. When set, returns the next page from memory and ignores the other search parameters."`
}

const (
	defaultBaseURL          = "https://api.crowdstrike.com"
	defaultMaxRecords       = 100
	defaultMaxFetchRecords  = 2000
	defaultMaxCachedQueries = 32
	defaultPageTTL          = 10 * time.Minute
)

// ToolSet implements gollem.ToolSet for CrowdStrike Falcon. Fields are
// unexported; configure via Option.
type ToolSet struct {
	clientID     string
	clientSecret string
	baseURL      string
	httpClient   *http.Client
	logger       *slog.Logger

	// maxRecords is the page size returned to the LLM; maxFetchRecords is the
	// hard cap on how many records a single search pulls into memory.
	maxRecords      int
	maxFetchRecords int
	pageTTL         time.Duration

	tokens     *tokenProvider
	pages      *pageStore
	tools      []gollem.Tool
	toolByName map[string]gollem.Tool
}

// Startup assertions: a malformed input/output type (a broken struct tag, a
// non-object kind) is a programming error that should surface at init rather
// than on the first LLM call. See gollem docs "Validating Tool Types".
var (
	_ = gollem.MustToolSchema[searchIncidentsInput, map[string]any]()
	_ = gollem.MustToolSchema[getIncidentsInput, map[string]any]()
	_ = gollem.MustToolSchema[searchAlertsInput, map[string]any]()
	_ = gollem.MustToolSchema[getAlertsInput, map[string]any]()
	_ = gollem.MustToolSchema[searchBehaviorsInput, map[string]any]()
	_ = gollem.MustToolSchema[getBehaviorsInput, map[string]any]()
	_ = gollem.MustToolSchema[searchDevicesInput, map[string]any]()
	_ = gollem.MustToolSchema[getDevicesInput, map[string]any]()
	_ = gollem.MustToolSchema[getCrowdScoresInput, map[string]any]()
	_ = gollem.MustToolSchema[searchEventsInput, map[string]any]()
)

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithBaseURL overrides the Falcon API base URL (use the URL matching your
// CrowdStrike cloud region). It applies to both the OAuth2 token endpoint and
// the API. An empty string keeps the default (US-1).
func WithBaseURL(baseURL string) Option {
	return func(t *ToolSet) {
		if baseURL != "" {
			t.baseURL = baseURL
		}
	}
}

// WithHTTPClient overrides the HTTP client used for both token and API
// requests. A nil client keeps the default.
func WithHTTPClient(client *http.Client) Option {
	return func(t *ToolSet) {
		if client != nil {
			t.httpClient = client
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

// WithMaxRecords sets the maximum number of records returned per page. A
// non-positive value keeps the default (100).
func WithMaxRecords(n int) Option {
	return func(t *ToolSet) {
		if n > 0 {
			t.maxRecords = n
		}
	}
}

// WithMaxFetchRecords sets the maximum number of records a single search pulls
// into memory across internal pagination. A non-positive value keeps the
// default (2000).
func WithMaxFetchRecords(n int) Option {
	return func(t *ToolSet) {
		if n > 0 {
			t.maxFetchRecords = n
		}
	}
}

// WithPageTTL sets how long a page_token remains valid. A non-positive value
// keeps the default (10 minutes).
func WithPageTTL(ttl time.Duration) Option {
	return func(t *ToolSet) {
		if ttl > 0 {
			t.pageTTL = ttl
		}
	}
}

// New constructs the ToolSet. clientID and clientSecret are required. New only
// validates static configuration and builds in-memory state; it performs no
// network I/O. Use Ping to verify credentials and connectivity.
func New(clientID, clientSecret string, opts ...Option) (*ToolSet, error) {
	if clientID == "" {
		return nil, goerr.New("Falcon API client ID is required")
	}
	if clientSecret == "" {
		return nil, goerr.New("Falcon API client secret is required")
	}

	t := &ToolSet{
		clientID:        clientID,
		clientSecret:    clientSecret,
		baseURL:         defaultBaseURL,
		httpClient:      &http.Client{Timeout: 60 * time.Second},
		logger:          slog.Default(),
		maxRecords:      defaultMaxRecords,
		maxFetchRecords: defaultMaxFetchRecords,
		pageTTL:         defaultPageTTL,
	}
	for _, opt := range opts {
		opt(t)
	}

	// maxRecords must never exceed maxFetchRecords, or a single page could not
	// be served from the fetched set.
	if t.maxRecords > t.maxFetchRecords {
		t.maxRecords = t.maxFetchRecords
	}

	t.tokens = newTokenProvider(t.clientID, t.clientSecret, t.baseURL, t.httpClient, t.logger)
	t.pages = newPageStore(defaultMaxCachedQueries, t.pageTTL)

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

// Ping verifies connectivity and credentials by acquiring an OAuth2 token.
func (t *ToolSet) Ping(ctx context.Context) error {
	if _, err := t.tokens.getToken(ctx); err != nil {
		return goerr.Wrap(err, "Falcon ping failed: could not obtain OAuth2 token")
	}
	return nil
}

// buildTools constructs the ten typed Falcon tools. Each handler captures the
// *ToolSet receiver and translates the typed input back into the args map
// expected by the existing private search/get methods, preserving all
// in-memory pagination logic and error messages unchanged.
// MustNewTool is used because the In/Out types are static: a build failure is a
// programming error (already guarded by the package-level MustToolSchema), not a
// runtime condition New should report.
func (t *ToolSet) buildTools() []gollem.Tool {
	toolSearchIncidents := gollem.MustNewTool("falcon_search_incidents",
		"Search incidents using FQL (Falcon Query Language) filters and return full incident details (status, tactics, techniques, hosts, users). Results are paginated in memory; pass page_token for more.",
		func(ctx context.Context, in searchIncidentsInput) (map[string]any, error) {
			args := map[string]any{
				"filter":     in.Filter,
				"sort":       in.Sort,
				"limit":      in.Limit,
				"page_token": in.PageToken,
			}
			return t.searchIncidents(ctx, args)
		})

	toolGetIncidents := gollem.MustNewTool("falcon_get_incidents",
		"Get detailed information for specific incidents by their IDs. Returns full incident details including status, tactics, techniques, hosts, and users involved.",
		func(ctx context.Context, in getIncidentsInput) (map[string]any, error) {
			args := map[string]any{"ids": in.IDs}
			return t.getIncidents(ctx, args)
		})

	toolSearchAlerts := gollem.MustNewTool("falcon_search_alerts",
		"Search and retrieve full alert details using FQL filters. Returns alert objects including severity, tactic, technique, and device info. Results are paginated in memory; pass page_token for more.",
		func(ctx context.Context, in searchAlertsInput) (map[string]any, error) {
			args := map[string]any{
				"filter":     in.Filter,
				"sort":       in.Sort,
				"limit":      in.Limit,
				"page_token": in.PageToken,
			}
			return t.searchAlerts(ctx, args)
		})

	toolGetAlerts := gollem.MustNewTool("falcon_get_alerts",
		"Get detailed alert information by composite IDs. Use this when you already have specific alert IDs.",
		func(ctx context.Context, in getAlertsInput) (map[string]any, error) {
			args := map[string]any{"composite_ids": in.CompositeIDs}
			return t.getAlerts(ctx, args)
		})

	toolSearchBehaviors := gollem.MustNewTool("falcon_search_behaviors",
		"Search behaviors using FQL filters and return full behavior details (tactic, technique, severity, pattern, device info). Results are paginated in memory; pass page_token for more.",
		func(ctx context.Context, in searchBehaviorsInput) (map[string]any, error) {
			args := map[string]any{
				"filter":     in.Filter,
				"limit":      in.Limit,
				"page_token": in.PageToken,
			}
			return t.searchBehaviors(ctx, args)
		})

	toolGetBehaviors := gollem.MustNewTool("falcon_get_behaviors",
		"Get detailed behavior information by IDs. Returns behavior details including tactic, technique, severity, pattern, and associated device info.",
		func(ctx context.Context, in getBehaviorsInput) (map[string]any, error) {
			args := map[string]any{"ids": in.IDs}
			return t.getBehaviors(ctx, args)
		})

	toolSearchDevices := gollem.MustNewTool("falcon_search_devices",
		"Search devices (hosts) using FQL filters and return full host details (OS, IP addresses, sensor version, containment status). Results are paginated in memory; pass page_token for more.",
		func(ctx context.Context, in searchDevicesInput) (map[string]any, error) {
			args := map[string]any{
				"filter":     in.Filter,
				"sort":       in.Sort,
				"limit":      in.Limit,
				"page_token": in.PageToken,
			}
			return t.searchDevices(ctx, args)
		})

	toolGetDevices := gollem.MustNewTool("falcon_get_devices",
		"Get detailed device (host) information by device IDs. Returns full host details including hostname, OS, IP addresses, sensor version, tags, and containment status.",
		func(ctx context.Context, in getDevicesInput) (map[string]any, error) {
			args := map[string]any{"ids": in.IDs}
			return t.getDevices(ctx, args)
		})

	toolGetCrowdScores := gollem.MustNewTool("falcon_get_crowdscores",
		"Get CrowdScore values for the environment. CrowdScore is an overall threat level indicator.",
		func(ctx context.Context, in getCrowdScoresInput) (map[string]any, error) {
			args := map[string]any{"filter": in.Filter}
			return t.getCrowdScores(ctx, args)
		})

	toolSearchEvents := gollem.MustNewTool("falcon_search_events",
		"Search EDR telemetry events using CrowdStrike Query Language (CQL) via the Next-Gen SIEM Search API (process executions, network connections, file writes, DNS, etc.). The search runs asynchronously and is polled until ready. Results are paginated in memory; pass page_token for more.",
		func(ctx context.Context, in searchEventsInput) (map[string]any, error) {
			args := map[string]any{
				"query_string": in.QueryString,
				"repository":   in.Repository,
				"start":        in.Start,
				"end":          in.End,
				"page_token":   in.PageToken,
			}
			return t.searchEvents(ctx, args)
		})

	return []gollem.Tool{
		toolSearchIncidents,
		toolGetIncidents,
		toolSearchAlerts,
		toolGetAlerts,
		toolSearchBehaviors,
		toolGetBehaviors,
		toolSearchDevices,
		toolGetDevices,
		toolGetCrowdScores,
		toolSearchEvents,
	}
}

// Specs returns the specifications for the ten Falcon tools.
func (t *ToolSet) Specs(_ context.Context) ([]gollem.ToolSpec, error) {
	specs := make([]gollem.ToolSpec, len(t.tools))
	for i, tool := range t.tools {
		specs[i] = tool.Spec()
	}
	return specs, nil
}

// Run executes the named Falcon tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	tool, ok := t.toolByName[name]
	if !ok {
		return nil, goerr.New("unknown tool name", goerr.V("name", name))
	}
	return tool.Run(ctx, args)
}

// apiError represents an HTTP error from the CrowdStrike API.
type apiError struct {
	statusCode int
	body       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("Falcon API error: status=%d", e.statusCode)
}

// doRequest performs an authenticated request. On a 401 it clears the cached
// token and retries once, recovering from a server-side token revocation.
func (t *ToolSet) doRequest(ctx context.Context, method, path string, body any) (map[string]any, error) {
	t.logger.Debug("Falcon API request", slog.String("method", method), slog.String("path", path))

	result, err := t.doRequestOnce(ctx, method, path, body)
	if err == nil {
		return result, nil
	}

	var apiErr *apiError
	if errors.As(err, &apiErr) && apiErr.statusCode == http.StatusUnauthorized {
		t.logger.Debug("received 401, clearing token and retrying")
		t.tokens.clearToken()
		return t.doRequestOnce(ctx, method, path, body)
	}

	return nil, err
}

// doRequestOnce performs a single authenticated request and decodes the JSON
// response into a map.
func (t *ToolSet) doRequestOnce(ctx context.Context, method, path string, body any) (map[string]any, error) {
	token, err := t.tokens.getToken(ctx)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get auth token")
	}

	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to marshal request body")
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, reqBody)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create request")
	}

	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to send request", goerr.V("path", path))
	}
	defer safeClose(t.logger, resp.Body)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read response body")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.logger.Warn("Falcon API request failed", slog.Int("status", resp.StatusCode), slog.String("path", path))
		return nil, goerr.Wrap(&apiError{statusCode: resp.StatusCode, body: string(respBody)},
			"Falcon API request failed",
			goerr.V("status", resp.StatusCode),
			goerr.V("path", path),
		)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, goerr.Wrap(err, "failed to parse response JSON", goerr.V("body", string(respBody)))
	}

	return result, nil
}

// extractResources returns the "resources" array of a Falcon response.
func extractResources(resp map[string]any) []any {
	if r, ok := resp["resources"].([]any); ok {
		return r
	}
	return nil
}

// metaPagination returns the meta.pagination object of a Falcon response.
func metaPagination(resp map[string]any) map[string]any {
	meta, ok := resp["meta"].(map[string]any)
	if !ok {
		return nil
	}
	p, _ := meta["pagination"].(map[string]any)
	return p
}

// extractTotal returns meta.pagination.total, or prev when absent.
func extractTotal(resp map[string]any, prev int) int {
	if p := metaPagination(resp); p != nil {
		if v, ok := p["total"].(float64); ok {
			return int(v)
		}
	}
	return prev
}

// extractStringOffset returns the string meta.pagination.offset (devices-scroll).
func extractStringOffset(resp map[string]any) string {
	if p := metaPagination(resp); p != nil {
		if v, ok := p["offset"].(string); ok {
			return v
		}
	}
	return ""
}

// extractAfter returns the meta.pagination.after cursor (alerts).
func extractAfter(resp map[string]any) string {
	if p := metaPagination(resp); p != nil {
		if v, ok := p["after"].(string); ok {
			return v
		}
	}
	return ""
}
