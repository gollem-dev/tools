// Package slack provides a gollem.ToolSet for searching Slack messages via
// the Slack search.messages API using a Slack user token (xoxp-…).
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

const defaultBaseURL = "https://slack.com/api"

// maxSearchRetries bounds how many times a search request is retried on rate
// limiting (HTTP 429 or a "rate_limited" API error).
const maxSearchRetries = 3

// defaultRetryWait is the base backoff used between retries when the Slack
// response does not carry a Retry-After header.
const defaultRetryWait = time.Second

// ToolSet implements gollem.ToolSet for Slack message search. Fields are
// unexported; configure via Option.
type ToolSet struct {
	userToken string
	baseURL   string
	client    *http.Client
	logger    *slog.Logger
	retryWait time.Duration
}

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithBaseURL overrides the Slack API base URL (default: https://slack.com/api).
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

// New constructs the ToolSet with the required user token. It only validates
// static configuration; use Ping to verify connectivity and credentials.
// userToken must be a user token (xoxp-…) with the search:read scope; bot
// tokens cannot call search.messages.
func New(userToken string, opts ...Option) (*ToolSet, error) {
	if userToken == "" {
		return nil, goerr.New("Slack user token is required")
	}

	t := &ToolSet{
		userToken: userToken,
		baseURL:   defaultBaseURL,
		client:    http.DefaultClient,
		logger:    slog.Default(),
		retryWait: defaultRetryWait,
	}
	for _, opt := range opts {
		opt(t)
	}

	return t, nil
}

// Specs returns the Slack tool specifications.
func (t *ToolSet) Specs(_ context.Context) ([]gollem.ToolSpec, error) {
	intPtr := func(v int) *int { return &v }
	float64Ptr := func(v float64) *float64 { return &v }

	return []gollem.ToolSpec{
		{
			Name:        "slack_message_search",
			Description: "Search for messages in Slack workspace using the search.messages API",
			Parameters: map[string]*gollem.Parameter{
				"query": {
					Type:        gollem.TypeString,
					Description: "The search query (e.g., 'from:@user', 'in:general', 'has:link')",
					Required:    true,
				},
				"sort": {
					Type:        gollem.TypeString,
					Description: "Sort order: 'score' (relevance) or 'timestamp' (newest first)",
				},
				"sort_dir": {
					Type:        gollem.TypeString,
					Description: "Sort direction: 'asc' or 'desc'",
				},
				"count": {
					Type:        gollem.TypeNumber,
					Description: "Number of results to return (default: 20, max: 100)",
				},
				"page": {
					Type:        gollem.TypeNumber,
					Description: "Page number for pagination (default: 1)",
				},
				"highlight": {
					Type:        gollem.TypeBoolean,
					Description: "Enable highlighting of search terms in results",
				},
			},
		},
		{
			Name: "slack_get_messages",
			Description: "Fetch one or more Slack messages and their thread context in bulk " +
				"(max 10 per call). Each target is fetched in parallel; per-target failures " +
				"are reported in the response without aborting the whole call.",
			Parameters: map[string]*gollem.Parameter{
				"targets": {
					Type:        gollem.TypeArray,
					Description: "Messages to fetch, each identified by channel_id and ts.",
					Required:    true,
					MinItems:    intPtr(1),
					MaxItems:    intPtr(maxGetMessagesTargets),
					Items: &gollem.Parameter{
						Type: gollem.TypeObject,
						Properties: map[string]*gollem.Parameter{
							"channel_id": {
								Type:        gollem.TypeString,
								Description: "Slack channel ID (e.g., 'C0123ABCD')",
								Required:    true,
							},
							"ts": {
								Type:        gollem.TypeString,
								Description: "Message timestamp (e.g., '1700000000.000100')",
								Required:    true,
							},
						},
					},
				},
				"include_thread": {
					Type:        gollem.TypeBoolean,
					Description: "If true (default), return the full thread when ts is a thread root; if false, only the message itself.",
				},
				"thread_limit": {
					Type:        gollem.TypeInteger,
					Description: "Max replies per thread (default: 20, max: 200).",
					Minimum:     float64Ptr(1),
					Maximum:     float64Ptr(maxThreadLimit),
				},
			},
		},
	}, nil
}

// Run executes the named Slack tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	switch name {
	case "slack_message_search":
		return t.runMessageSearch(ctx, args)
	case "slack_get_messages":
		return t.runGetMessages(ctx, args)
	default:
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}
}

// runMessageSearch handles the slack_message_search tool.
func (t *ToolSet) runMessageSearch(ctx context.Context, args map[string]any) (map[string]any, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, goerr.New("query is required", goerr.V("args", args))
	}

	opts := &searchOptions{
		Query: query,
		Count: 20,
		Page:  1,
	}

	if sort, ok := args["sort"].(string); ok {
		opts.Sort = sort
	}
	if sortDir, ok := args["sort_dir"].(string); ok {
		opts.SortDir = sortDir
	}
	if count, ok := args["count"].(float64); ok {
		opts.Count = int(count)
	}
	if page, ok := args["page"].(float64); ok {
		opts.Page = int(page)
	}
	if highlight, ok := args["highlight"].(bool); ok {
		opts.Highlight = highlight
	}

	resp, err := t.searchMessages(ctx, opts)
	if err != nil {
		return nil, err
	}

	return t.formatResult(resp), nil
}

// Ping verifies connectivity and credentials by calling auth.test.
func (t *ToolSet) Ping(ctx context.Context) error {
	endpoint := t.baseURL + "/auth.test"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return goerr.Wrap(err, "failed to create auth.test request", goerr.V("url", endpoint))
	}
	req.Header.Set("Authorization", "Bearer "+t.userToken)

	resp, err := t.client.Do(req)
	if err != nil {
		return goerr.Wrap(err, "failed to send auth.test request", goerr.V("url", endpoint))
	}
	defer safeClose(t.logger, resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return goerr.Wrap(err, "failed to read auth.test response body")
	}

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return goerr.Wrap(err, "failed to unmarshal auth.test response", goerr.V("body", string(body)))
	}

	if !result.OK {
		return goerr.New("Slack auth.test failed", goerr.V("error", result.Error))
	}

	return nil
}

// searchOptions holds parameters for the search.messages API call.
type searchOptions struct {
	Query     string
	Sort      string
	SortDir   string
	Count     int
	Page      int
	Highlight bool
}

// searchResponse mirrors the Slack search.messages JSON response.
type searchResponse struct {
	OK       bool          `json:"ok"`
	Query    string        `json:"query"`
	Messages messagesBlock `json:"messages"`
	Error    string        `json:"error,omitempty"`
}

type messagesBlock struct {
	Total   int       `json:"total"`
	Paging  paging    `json:"paging"`
	Matches []message `json:"matches"`
}

type paging struct {
	Count int `json:"count"`
	Total int `json:"total"`
	Page  int `json:"page"`
	Pages int `json:"pages"`
}

type message struct {
	Channel   channelInfo `json:"channel"`
	User      string      `json:"user"`
	Username  string      `json:"username"`
	Text      string      `json:"text"`
	Timestamp string      `json:"ts"`
	Permalink string      `json:"permalink"`
}

type channelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// searchMessages calls the Slack search.messages endpoint and returns the parsed response.
func (t *ToolSet) searchMessages(ctx context.Context, opts *searchOptions) (*searchResponse, error) {
	params := url.Values{}
	params.Set("query", opts.Query)
	if opts.Sort != "" {
		params.Set("sort", opts.Sort)
	}
	if opts.SortDir != "" {
		params.Set("sort_dir", opts.SortDir)
	}
	params.Set("count", strconv.Itoa(opts.Count))
	params.Set("page", strconv.Itoa(opts.Page))
	if opts.Highlight {
		params.Set("highlight", "true")
	}

	endpoint := fmt.Sprintf("%s/search.messages?%s", t.baseURL, params.Encode())

	// Slack search is rate-limit prone, so retry on HTTP 429 / "rate_limited",
	// honoring Retry-After. Non-rate-limit errors are returned immediately.
	var lastErr error
	for attempt := range maxSearchRetries {
		if err := ctx.Err(); err != nil {
			return nil, goerr.Wrap(err, "context cancelled during Slack search")
		}

		result, retryAfter, retry, err := t.searchOnce(ctx, endpoint)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}

		// Don't wait after the final attempt.
		if attempt == maxSearchRetries-1 {
			break
		}
		wait := retryAfter
		if wait <= 0 {
			wait = t.retryWait * time.Duration(attempt+1)
		}
		t.logger.InfoContext(ctx, "Slack search rate limited; retrying",
			slog.Duration("wait", wait), slog.Int("attempt", attempt+1))
		if waitErr := sleepCtx(ctx, wait); waitErr != nil {
			return nil, goerr.Wrap(waitErr, "context cancelled while waiting to retry Slack search")
		}
	}

	return nil, goerr.Wrap(lastErr, "Slack search failed after retries",
		goerr.V("retries", maxSearchRetries))
}

// searchOnce performs a single search.messages request. When retry is true the
// caller should wait (retryAfter if > 0, otherwise a backoff) and try again.
func (t *ToolSet) searchOnce(ctx context.Context, endpoint string) (result *searchResponse, retryAfter time.Duration, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, false, goerr.Wrap(err, "failed to create search request", goerr.V("url", endpoint))
	}
	req.Header.Set("Authorization", "Bearer "+t.userToken)

	resp, err := t.client.Do(req)
	if err != nil {
		// Treat transport errors as retryable.
		return nil, 0, true, goerr.Wrap(err, "failed to send search request", goerr.V("url", endpoint))
	}
	defer safeClose(t.logger, resp.Body)

	eb := goerr.NewBuilder(goerr.V("status", resp.StatusCode), goerr.V("url", endpoint))

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, true, eb.Wrap(err, "failed to read search response body")
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, parseRetryAfter(resp), true, eb.New("Slack search rate limited (HTTP 429)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, false, eb.New("Slack search request failed", goerr.V("body", string(body)))
	}

	var parsed searchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, 0, false, eb.Wrap(err, "failed to unmarshal search response", goerr.V("body", string(body)))
	}

	if !parsed.OK {
		if parsed.Error == "rate_limited" {
			return nil, 0, true, goerr.New("Slack API rate limited", goerr.V("error", parsed.Error))
		}
		return nil, 0, false, goerr.New("Slack API error", goerr.V("error", parsed.Error))
	}

	return &parsed, 0, false, nil
}

// parseRetryAfter reads the Retry-After header (in seconds). It returns 0 when
// the header is absent or unparseable.
func parseRetryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// sleepCtx waits for d or until ctx is cancelled, whichever comes first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// formatResult converts a searchResponse into the map[string]any expected by gollem.
func (t *ToolSet) formatResult(resp *searchResponse) map[string]any {
	messages := make([]any, 0, len(resp.Messages.Matches))
	for _, msg := range resp.Messages.Matches {
		item := map[string]any{
			"channel":      msg.Channel.ID,
			"channel_name": msg.Channel.Name,
			"user":         msg.User,
			"user_name":    msg.Username,
			"text":         msg.Text,
			"timestamp":    msg.Timestamp,
			"permalink":    msg.Permalink,
		}

		// Attach a human-readable time when the timestamp is parseable.
		if ts, err := strconv.ParseFloat(msg.Timestamp, 64); err == nil {
			item["formatted_time"] = time.Unix(int64(ts), 0).UTC().Format(time.RFC3339)
		}

		messages = append(messages, item)
	}

	return map[string]any{
		"total":    float64(resp.Messages.Total),
		"messages": messages,
	}
}
