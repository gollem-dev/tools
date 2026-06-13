// Package urlscan provides a gollem.ToolSet for scanning URLs via urlscan.io.
package urlscan

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
	defaultBaseURL = "https://urlscan.io/api/v1"
	defaultBackoff = 3 * time.Second
	defaultTimeout = 30 * time.Second
)

// ToolSet implements gollem.ToolSet for urlscan.io. Fields are unexported;
// configure via Option.
type ToolSet struct {
	apiKey  string
	baseURL string
	backoff time.Duration
	timeout time.Duration
	client  *http.Client
	logger  *slog.Logger
}

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithBaseURL overrides the urlscan.io API base URL.
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

// WithBackoff sets the interval between poll attempts while waiting for a
// scan result to become available. Default is 3 seconds.
func WithBackoff(d time.Duration) Option {
	return func(t *ToolSet) {
		if d > 0 {
			t.backoff = d
		}
	}
}

// WithTimeout sets the maximum time to wait for a scan result. Default is 30
// seconds.
func WithTimeout(d time.Duration) Option {
	return func(t *ToolSet) {
		if d > 0 {
			t.timeout = d
		}
	}
}

// New constructs the ToolSet. It only validates static configuration; use Ping
// to verify connectivity and credentials.
func New(apiKey string, opts ...Option) (*ToolSet, error) {
	if apiKey == "" {
		return nil, goerr.New("urlscan API key is required")
	}
	t := &ToolSet{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		backoff: defaultBackoff,
		timeout: defaultTimeout,
		client:  http.DefaultClient,
		logger:  slog.Default(),
	}
	for _, opt := range opts {
		opt(t)
	}
	if _, err := url.Parse(t.baseURL); err != nil {
		return nil, goerr.Wrap(err, "invalid base URL", goerr.V("base_url", t.baseURL))
	}

	return t, nil
}

// Specs returns the urlscan tool specifications.
func (t *ToolSet) Specs(_ context.Context) ([]gollem.ToolSpec, error) {
	return []gollem.ToolSpec{
		{
			Name:        "urlscan_scan",
			Description: "Scan a URL with urlscan.io to analyse its content and behaviour.",
			Parameters: map[string]*gollem.Parameter{
				"url": {
					Type:        gollem.TypeString,
					Description: "The URL to scan",
					Required:    true,
				},
			},
		},
	}, nil
}

// Run executes the named urlscan tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	switch name {
	case "urlscan_scan":
		// valid
	default:
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}

	urlStr, ok := args["url"].(string)
	if !ok || urlStr == "" {
		return nil, goerr.New("url parameter is required", goerr.V("args", args))
	}
	if _, err := url.Parse(urlStr); err != nil {
		return nil, goerr.Wrap(err, "invalid URL", goerr.V("url", urlStr))
	}

	return t.scan(ctx, urlStr)
}

// Ping verifies connectivity and credentials by performing a minimal
// authenticated GET against the API root. Any non-5xx response is treated as
// reachable; only network errors and server-side failures are considered fatal.
func (t *ToolSet) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.baseURL+"/", nil)
	if err != nil {
		return goerr.Wrap(err, "urlscan ping: failed to create request")
	}
	req.Header.Set("API-Key", t.apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return goerr.Wrap(err, "urlscan ping: request failed")
	}
	defer safeClose(t.logger, resp.Body)

	if resp.StatusCode >= http.StatusInternalServerError {
		return goerr.New("urlscan ping: server error", goerr.V("status", resp.StatusCode))
	}
	return nil
}

// submitResponse is the JSON payload returned by POST /scan/.
type submitResponse struct {
	UUID      string `json:"uuid"`
	ResultURL string `json:"result"`
}

// scan submits the URL and polls until the result is ready.
func (t *ToolSet) scan(ctx context.Context, targetURL string) (map[string]any, error) {
	// --- Step 1: submit the scan ---
	body, err := json.Marshal(map[string]string{
		"url":        targetURL,
		"visibility": "public",
	})
	if err != nil {
		return nil, goerr.Wrap(err, "failed to marshal scan request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/scan/", bytes.NewReader(body))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create scan request")
	}
	req.Header.Set("API-Key", t.apiKey)
	req.Header.Set("Content-Type", "application/json")

	t.logger.Debug("submitting urlscan request", slog.String("url", targetURL))

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to send scan request", goerr.V("url", targetURL))
	}
	defer safeClose(t.logger, resp.Body)

	eb := goerr.NewBuilder(goerr.V("status", resp.StatusCode), goerr.V("url", targetURL))

	if resp.StatusCode != http.StatusOK {
		rawBody, _ := io.ReadAll(resp.Body)
		return nil, eb.New("failed to submit urlscan request", goerr.V("body", string(rawBody)))
	}

	var submitted submitResponse
	if err := json.NewDecoder(resp.Body).Decode(&submitted); err != nil {
		return nil, goerr.Wrap(err, "failed to decode scan submission response")
	}

	// --- Step 2: poll for the result ---
	resultURL := t.baseURL + "/result/" + submitted.UUID + "/"

	deadline := time.Now().Add(t.timeout)
	for {
		select {
		case <-ctx.Done():
			return nil, goerr.Wrap(ctx.Err(), "context cancelled while waiting for urlscan result",
				goerr.V("uuid", submitted.UUID), goerr.V("url", targetURL))
		default:
		}

		if time.Now().After(deadline) {
			break
		}

		// Wait the backoff period, but respect context cancellation.
		timer := time.NewTimer(t.backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, goerr.Wrap(ctx.Err(), "context cancelled during backoff",
				goerr.V("uuid", submitted.UUID), goerr.V("url", targetURL))
		case <-timer.C:
		}

		pollReq, err := http.NewRequestWithContext(ctx, http.MethodGet, resultURL, nil)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to create result request", goerr.V("uuid", submitted.UUID))
		}
		pollReq.Header.Set("API-Key", t.apiKey)

		pollResp, err := t.client.Do(pollReq)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to poll for result", goerr.V("uuid", submitted.UUID))
		}

		if pollResp.StatusCode == http.StatusNotFound {
			safeClose(t.logger, pollResp.Body)
			t.logger.Debug("urlscan result not yet available, retrying",
				slog.String("uuid", submitted.UUID),
				slog.Duration("backoff", t.backoff),
			)
			continue
		}

		if pollResp.StatusCode != http.StatusOK {
			rawBody, _ := io.ReadAll(pollResp.Body)
			safeClose(t.logger, pollResp.Body)
			return nil, goerr.New("failed to get urlscan result",
				goerr.V("status", pollResp.StatusCode),
				goerr.V("body", string(rawBody)),
				goerr.V("uuid", submitted.UUID),
			)
		}

		rawBody, err := io.ReadAll(pollResp.Body)
		safeClose(t.logger, pollResp.Body)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to read urlscan result body", goerr.V("uuid", submitted.UUID))
		}

		var result map[string]any
		if err := json.Unmarshal(rawBody, &result); err != nil {
			return nil, goerr.Wrap(err, "failed to unmarshal urlscan result", goerr.V("uuid", submitted.UUID))
		}
		return result, nil
	}

	return nil, goerr.New("urlscan result timed out",
		goerr.V("timeout", t.timeout),
		goerr.V("url", targetURL),
		goerr.V("uuid", submitted.UUID),
	)
}
