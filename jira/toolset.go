// Package jira provides a gollem.ToolSet for read-only access to Jira Cloud:
// listing accessible projects, searching issues with JQL, and fetching the
// content of one or more issues (with their description rendered to Markdown).
//
// The tool talks to the Jira Cloud REST API v3 directly over HTTP (no
// third-party SDK) so the module stays self-contained. Authentication uses Jira
// Cloud Basic auth: an account email plus an API token. Because each Jira site
// lives on its own tenant domain (https://<your-domain>.atlassian.net), the
// base URL is a required argument to New rather than a fixed constant.
package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

// httpTimeout caps every Jira API request. Without it, a stalled response from
// Jira would let a goroutine hang indefinitely.
const httpTimeout = 30 * time.Second

// ToolSet implements gollem.ToolSet for read-only Jira Cloud access. Fields are
// unexported; configure via Option.
type ToolSet struct {
	baseURL  string
	email    string
	apiToken string
	client   *http.Client
	logger   *slog.Logger
}

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

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

// New constructs the ToolSet. baseURL is the Jira site URL
// (e.g. https://your-domain.atlassian.net), email is the account email and
// apiToken is the Jira API token; all three are required. It only validates
// static configuration — use Ping to verify connectivity and credentials.
func New(baseURL, email, apiToken string, opts ...Option) (*ToolSet, error) {
	if baseURL == "" {
		return nil, goerr.New("Jira base URL is required")
	}
	if email == "" {
		return nil, goerr.New("Jira account email is required")
	}
	if apiToken == "" {
		return nil, goerr.New("Jira API token is required")
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, goerr.Wrap(err, "invalid base URL", goerr.V("base_url", baseURL))
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, goerr.New("base URL must be absolute (scheme + host)", goerr.V("base_url", baseURL))
	}

	t := &ToolSet{
		// Trim a trailing slash so endpoint paths can be appended uniformly.
		baseURL:  strings.TrimRight(baseURL, "/"),
		email:    email,
		apiToken: apiToken,
		client:   &http.Client{Timeout: httpTimeout},
		logger:   slog.Default(),
	}
	for _, opt := range opts {
		opt(t)
	}

	return t, nil
}

// Tool names exposed by this ToolSet.
const (
	toolListProjects = "jira_list_projects"
	toolSearchIssues = "jira_search_issues"
	toolGetIssues    = "jira_get_issues"
)

// Specs returns the Jira tool specifications.
func (t *ToolSet) Specs(ctx context.Context) ([]gollem.ToolSpec, error) {
	return []gollem.ToolSpec{
		listProjectsSpec(),
		searchIssuesSpec(),
		getIssuesSpec(),
	}, nil
}

// Run dispatches to the named Jira tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	switch name {
	case toolListProjects:
		return t.listProjects(ctx, args)
	case toolSearchIssues:
		return t.searchIssues(ctx, args)
	case toolGetIssues:
		return t.getIssues(ctx, args)
	default:
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}
}

// Ping verifies connectivity and credentials by fetching the current user.
func (t *ToolSet) Ping(ctx context.Context) error {
	if err := t.doJSON(ctx, http.MethodGet, "/rest/api/3/myself", nil, nil); err != nil {
		return goerr.Wrap(err, "Jira ping failed")
	}
	return nil
}

// doJSON performs an authenticated Jira API call. body (if non-nil) is JSON
// encoded as the request payload, and the response is JSON decoded into out (if
// non-nil).
func (t *ToolSet) doJSON(ctx context.Context, method, path string, body any, out any) error {
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
	// Jira Cloud Basic auth: base64("email:apiToken").
	cred := base64.StdEncoding.EncodeToString([]byte(t.email + ":" + t.apiToken))
	req.Header.Set("Authorization", "Basic "+cred)
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
		// Cap the echoed body: a proxy or Jira itself could return a large HTML
		// error page rather than a compact JSON error.
		snippet := respBody
		if len(snippet) > 4096 {
			snippet = snippet[:4096]
		}
		return eb.New("Jira API returned non-2xx", goerr.V("body", string(snippet)))
	}

	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return eb.Wrap(err, "failed to unmarshal response", goerr.V("body", string(respBody)))
		}
	}
	return nil
}

// clampInt coerces a tool argument into an int within [min, max], using def when
// the value is absent or non-positive.
func clampInt(v any, def, minv, maxv int) int {
	n := 0
	switch x := v.(type) {
	case float64: // JSON numbers decode to float64 through map[string]any
		n = int(x)
	case int:
		n = x
	case int64:
		n = int(x)
	}
	if n <= 0 {
		n = def
	}
	if n < minv {
		return minv
	}
	if n > maxv {
		return maxv
	}
	return n
}
