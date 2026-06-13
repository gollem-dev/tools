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
	"github.com/gollem-dev/tools/internal/safe"
	"github.com/m-mizutani/goerr/v2"
)

const defaultBaseURL = "https://api.shodan.io"

// ToolSet implements gollem.ToolSet for Shodan. Fields are unexported;
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

// WithAPIKey sets the Shodan API key. It is required.
func WithAPIKey(key string) Option {
	return func(t *ToolSet) { t.apiKey = key }
}

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
func New(opts ...Option) (*ToolSet, error) {
	t := &ToolSet{
		baseURL: defaultBaseURL,
		client:  http.DefaultClient,
		logger:  slog.Default(),
	}
	for _, opt := range opts {
		opt(t)
	}

	if t.apiKey == "" {
		return nil, goerr.New("Shodan API key is required")
	}
	if _, err := url.Parse(t.baseURL); err != nil {
		return nil, goerr.Wrap(err, "invalid base URL", goerr.V("base_url", t.baseURL))
	}

	return t, nil
}

// Specs returns the Shodan tool specifications.
func (t *ToolSet) Specs(ctx context.Context) ([]gollem.ToolSpec, error) {
	return []gollem.ToolSpec{
		{
			Name:        "shodan_host",
			Description: "Search the host information from Shodan.",
			Parameters: map[string]*gollem.Parameter{
				"target": {
					Type:        gollem.TypeString,
					Description: "The IP address to search",
					Required:    true,
				},
			},
		},
		{
			Name:        "shodan_domain",
			Description: "Search the domain information from Shodan.",
			Parameters: map[string]*gollem.Parameter{
				"target": {
					Type:        gollem.TypeString,
					Description: "The domain to search",
					Required:    true,
				},
			},
		},
		{
			Name:        "shodan_search",
			Description: "Search the internet using Shodan search query.",
			Parameters: map[string]*gollem.Parameter{
				"query": {
					Type:        gollem.TypeString,
					Description: "The search query to use",
					Required:    true,
				},
				"limit": {
					Type:        gollem.TypeInteger,
					Description: "Maximum number of results to return (default: 100)",
					Required:    false,
				},
			},
		},
	}, nil
}

// Run executes the named Shodan lookup.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	var endpoint string
	params := url.Values{}
	params.Set("key", t.apiKey)

	switch name {
	case "shodan_host":
		target, ok := args["target"].(string)
		if !ok || target == "" {
			return nil, goerr.New("target is required", goerr.V("name", name), goerr.V("args", args))
		}
		endpoint = fmt.Sprintf("%s/shodan/host/%s", t.baseURL, url.PathEscape(target))

	case "shodan_domain":
		target, ok := args["target"].(string)
		if !ok || target == "" {
			return nil, goerr.New("target is required", goerr.V("name", name), goerr.V("args", args))
		}
		endpoint = fmt.Sprintf("%s/dns/domain/%s", t.baseURL, url.PathEscape(target))

	case "shodan_search":
		query, ok := args["query"].(string)
		if !ok || query == "" {
			return nil, goerr.New("query is required", goerr.V("name", name), goerr.V("args", args))
		}
		endpoint = fmt.Sprintf("%s/shodan/host/search", t.baseURL)
		params.Set("query", query)

		// limit is optional; LLMs may pass it as float64 (JSON numbers).
		if raw, exists := args["limit"]; exists && raw != nil {
			limit, ok := raw.(float64)
			if !ok {
				return nil, goerr.New("invalid limit parameter type",
					goerr.V("type", fmt.Sprintf("%T", raw)),
					goerr.V("value", raw))
			}
			params.Set("limit", fmt.Sprintf("%d", int(limit)))
		}

	default:
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}

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
	defer safe.Close(t.logger, resp.Body)

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
	defer safe.Close(t.logger, resp.Body)

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
