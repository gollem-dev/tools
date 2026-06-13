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
	"github.com/gollem-dev/tools/internal/safe"
	"github.com/m-mizutani/goerr/v2"
)

const defaultBaseURL = "https://api.abuseipdb.com/api/v2"

// ToolSet implements gollem.ToolSet for AbuseIPDB. Fields are unexported;
// configure via Option.
type ToolSet struct {
	apiKey  string
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

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

	return t, nil
}

// Specs returns the AbuseIPDB tool specifications.
func (t *ToolSet) Specs(ctx context.Context) ([]gollem.ToolSpec, error) {
	return []gollem.ToolSpec{
		{
			Name:        "ipdb_check",
			Description: "Check IP address information from AbuseIPDB.",
			Parameters: map[string]*gollem.Parameter{
				"target": {
					Type:        gollem.TypeString,
					Description: "The IP address to check",
					Required:    true,
				},
				"maxAgeInDays": {
					Type:        gollem.TypeInteger,
					Description: "The maximum age of reports in days (1-365)",
				},
			},
		},
	}, nil
}

// Run executes the named AbuseIPDB lookup.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	switch name {
	case "ipdb_check":
		return t.check(ctx, args)
	default:
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}
}

// Ping verifies connectivity and credentials by querying a well-known IP
// address.
func (t *ToolSet) Ping(ctx context.Context) error {
	if _, err := t.check(ctx, map[string]any{"target": "8.8.8.8"}); err != nil {
		return goerr.Wrap(err, "AbuseIPDB ping failed")
	}
	return nil
}

func (t *ToolSet) check(ctx context.Context, args map[string]any) (map[string]any, error) {
	ipAddress, ok := args["target"].(string)
	if !ok || ipAddress == "" {
		return nil, goerr.New("target is required", goerr.V("args", args))
	}

	endpoint := t.baseURL + "/check"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create request", goerr.V("url", endpoint))
	}

	// Build query parameters.
	q := req.URL.Query()
	q.Add("ipAddress", ipAddress)
	if maxAge, ok := args["maxAgeInDays"].(float64); ok {
		q.Add("maxAgeInDays", fmt.Sprintf("%d", int(maxAge)))
	} else if args["maxAgeInDays"] != nil {
		return nil, goerr.New("invalid maxAgeInDays parameter type",
			goerr.V("type", fmt.Sprintf("%T", args["maxAgeInDays"])),
			goerr.V("value", args["maxAgeInDays"]))
	}
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Key", t.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to send request", goerr.V("url", endpoint))
	}
	defer safe.Close(t.logger, resp.Body)

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
