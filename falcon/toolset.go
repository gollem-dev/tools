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

	tokens *tokenProvider
	pages  *pageStore
}

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

	return t, nil
}

// Ping verifies connectivity and credentials by acquiring an OAuth2 token.
func (t *ToolSet) Ping(ctx context.Context) error {
	if _, err := t.tokens.getToken(ctx); err != nil {
		return goerr.Wrap(err, "Falcon ping failed: could not obtain OAuth2 token")
	}
	return nil
}

// Specs returns the specifications for the ten Falcon tools.
func (t *ToolSet) Specs(_ context.Context) ([]gollem.ToolSpec, error) {
	// pageToken is shared by every search tool: passing it returns the next
	// in-memory page and ignores the other search arguments.
	pageToken := &gollem.Parameter{
		Type:        gollem.TypeString,
		Description: "Opaque token from a previous response's page_token. When set, returns the next page from memory and ignores the other search parameters.",
	}
	limitParam := func(extra string) *gollem.Parameter {
		return &gollem.Parameter{
			Type:        gollem.TypeNumber,
			Description: fmt.Sprintf("Page size: maximum records to return in this page (default and ceiling: %d). %sUse page_token to fetch further pages.", t.maxRecords, extra),
		}
	}

	return []gollem.ToolSpec{
		{
			Name:        "falcon_search_incidents",
			Description: "Search incidents using FQL (Falcon Query Language) filters and return full incident details (status, tactics, techniques, hosts, users). Results are paginated in memory; pass page_token for more.",
			Parameters: map[string]*gollem.Parameter{
				"filter": {
					Type:        gollem.TypeString,
					Description: "FQL filter expression (e.g., \"status:'30'\", \"tags:'critical'\", \"start:>'2025-01-01'\")",
				},
				"sort": {
					Type:        gollem.TypeString,
					Description: "Sort expression (e.g., \"start.desc\", \"end.asc\")",
				},
				"limit":      limitParam(""),
				"page_token": pageToken,
			},
		},
		{
			Name:        "falcon_get_incidents",
			Description: "Get detailed information for specific incidents by their IDs. Returns full incident details including status, tactics, techniques, hosts, and users involved.",
			Parameters: map[string]*gollem.Parameter{
				"ids": {
					Type:        gollem.TypeString,
					Description: fmt.Sprintf("Comma-separated incident IDs (e.g., \"inc:abc123:def456,inc:abc123:ghi789\"). At most %d IDs are fetched per call.", t.maxRecords),
					Required:    true,
				},
			},
		},
		{
			Name:        "falcon_search_alerts",
			Description: "Search and retrieve full alert details using FQL filters. Returns alert objects including severity, tactic, technique, and device info. Results are paginated in memory; pass page_token for more.",
			Parameters: map[string]*gollem.Parameter{
				"filter": {
					Type:        gollem.TypeString,
					Description: "FQL filter expression (e.g., \"status:'new'\", \"severity:>50\", \"tactics:'Lateral Movement'\")",
				},
				"sort": {
					Type:        gollem.TypeString,
					Description: "Sort property (e.g., \"timestamp|desc\", \"severity|asc\")",
				},
				"limit":      limitParam(""),
				"page_token": pageToken,
			},
		},
		{
			Name:        "falcon_get_alerts",
			Description: "Get detailed alert information by composite IDs. Use this when you already have specific alert IDs.",
			Parameters: map[string]*gollem.Parameter{
				"composite_ids": {
					Type:        gollem.TypeString,
					Description: fmt.Sprintf("Comma-separated composite alert IDs. At most %d IDs are fetched per call.", t.maxRecords),
					Required:    true,
				},
			},
		},
		{
			Name:        "falcon_search_behaviors",
			Description: "Search behaviors using FQL filters and return full behavior details (tactic, technique, severity, pattern, device info). Results are paginated in memory; pass page_token for more.",
			Parameters: map[string]*gollem.Parameter{
				"filter": {
					Type:        gollem.TypeString,
					Description: "FQL filter expression",
				},
				"limit":      limitParam(""),
				"page_token": pageToken,
			},
		},
		{
			Name:        "falcon_get_behaviors",
			Description: "Get detailed behavior information by IDs. Returns behavior details including tactic, technique, severity, pattern, and associated device info.",
			Parameters: map[string]*gollem.Parameter{
				"ids": {
					Type:        gollem.TypeString,
					Description: fmt.Sprintf("Comma-separated behavior IDs. At most %d IDs are fetched per call.", t.maxRecords),
					Required:    true,
				},
			},
		},
		{
			Name:        "falcon_search_devices",
			Description: "Search devices (hosts) using FQL filters and return full host details (OS, IP addresses, sensor version, containment status). Results are paginated in memory; pass page_token for more.",
			Parameters: map[string]*gollem.Parameter{
				"filter": {
					Type:        gollem.TypeString,
					Description: "FQL filter expression (e.g., \"hostname:'*web*'\", \"platform_name:'Windows'\", \"last_seen:>='2025-01-01'\")",
				},
				"sort": {
					Type:        gollem.TypeString,
					Description: "Sort expression (e.g., \"hostname.asc\", \"last_seen.desc\")",
				},
				"limit":      limitParam(""),
				"page_token": pageToken,
			},
		},
		{
			Name:        "falcon_get_devices",
			Description: "Get detailed device (host) information by device IDs. Returns full host details including hostname, OS, IP addresses, sensor version, tags, and containment status.",
			Parameters: map[string]*gollem.Parameter{
				"ids": {
					Type:        gollem.TypeString,
					Description: fmt.Sprintf("Comma-separated device IDs. At most %d IDs are fetched per call.", t.maxRecords),
					Required:    true,
				},
			},
		},
		{
			Name:        "falcon_get_crowdscores",
			Description: "Get CrowdScore values for the environment. CrowdScore is an overall threat level indicator.",
			Parameters: map[string]*gollem.Parameter{
				"filter": {
					Type:        gollem.TypeString,
					Description: "FQL filter expression (e.g., \"timestamp:>'2025-01-01'\")",
				},
			},
		},
		{
			Name:        "falcon_search_events",
			Description: "Search EDR telemetry events using CrowdStrike Query Language (CQL) via the Next-Gen SIEM Search API (process executions, network connections, file writes, DNS, etc.). The search runs asynchronously and is polled until ready. Results are paginated in memory; pass page_token for more.",
			Parameters: map[string]*gollem.Parameter{
				"query_string": {
					Type:        gollem.TypeString,
					Description: "CQL query string (e.g., \"aid=abc123\", \"#event_simpleName=ProcessRollup2 AND FileName=cmd.exe\", \"ComputerName=workstation1 | tail(100)\")",
				},
				"repository": {
					Type:        gollem.TypeString,
					Description: "Repository to search. Values: \"search-all\" (default), \"investigate_view\" (Falcon EDR), \"third-party\", \"falcon_for_it_view\", \"forensics_view\"",
				},
				"start": {
					Type:        gollem.TypeString,
					Description: "Start time (e.g., \"1d\", \"24h\", \"2025-01-01T00:00:00Z\"). Default: \"1d\"",
				},
				"end": {
					Type:        gollem.TypeString,
					Description: "End time (e.g., \"now\", \"2025-01-02T00:00:00Z\"). Default: \"now\"",
				},
				"page_token": pageToken,
			},
		},
	}, nil
}

// Run executes the named Falcon tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	switch name {
	case "falcon_search_incidents":
		return t.searchIncidents(ctx, args)
	case "falcon_get_incidents":
		return t.getIncidents(ctx, args)
	case "falcon_search_alerts":
		return t.searchAlerts(ctx, args)
	case "falcon_get_alerts":
		return t.getAlerts(ctx, args)
	case "falcon_search_behaviors":
		return t.searchBehaviors(ctx, args)
	case "falcon_get_behaviors":
		return t.getBehaviors(ctx, args)
	case "falcon_search_devices":
		return t.searchDevices(ctx, args)
	case "falcon_get_devices":
		return t.getDevices(ctx, args)
	case "falcon_get_crowdscores":
		return t.getCrowdScores(ctx, args)
	case "falcon_search_events":
		return t.searchEvents(ctx, args)
	default:
		return nil, goerr.New("unknown tool name", goerr.V("name", name))
	}
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
