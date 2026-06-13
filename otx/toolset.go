// Package otx provides a gollem.ToolSet for AlienVault OTX threat intelligence
// lookups (IPv4/IPv6/domain/hostname/file hash).
package otx

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

const defaultBaseURL = "https://otx.alienvault.com/api/v1"

// ToolSet implements gollem.ToolSet for AlienVault OTX. Fields are unexported;
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

// WithBaseURL overrides the OTX API base URL.
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

// New constructs the ToolSet. apiKey is required; pass optional configuration
// via opts. It only validates static configuration — use Ping to verify
// connectivity and credentials.
func New(apiKey string, opts ...Option) (*ToolSet, error) {
	if apiKey == "" {
		return nil, goerr.New("OTX API key is required")
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

// indicatorTypes maps each tool name to the OTX indicator type path segment.
var indicatorTypes = map[string]string{
	"otx_ipv4":      "IPv4",
	"otx_ipv6":      "IPv6",
	"otx_domain":    "domain",
	"otx_hostname":  "hostname",
	"otx_file_hash": "file",
}

// Specs returns the OTX tool specifications.
func (t *ToolSet) Specs(ctx context.Context) ([]gollem.ToolSpec, error) {
	target := func(desc string) map[string]*gollem.Parameter {
		return map[string]*gollem.Parameter{
			"target": {
				Type:        gollem.TypeString,
				Description: desc,
				Required:    true,
			},
		}
	}
	return []gollem.ToolSpec{
		{Name: "otx_ipv4", Description: "Search the indicator of IPv4 from OTX.", Parameters: target("The IPv4 address to search")},
		{Name: "otx_domain", Description: "Search the indicator of domain from OTX.", Parameters: target("The domain to search")},
		{Name: "otx_ipv6", Description: "Search the indicator of IPv6 from OTX.", Parameters: target("The IPv6 address to search")},
		{Name: "otx_hostname", Description: "Search the indicator of hostname from OTX.", Parameters: target("The hostname to search")},
		{Name: "otx_file_hash", Description: "Search the indicator of file hash from OTX.", Parameters: target("The file hash to search")},
	}, nil
}

// Run executes the named OTX lookup.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	indicatorType, ok := indicatorTypes[name]
	if !ok {
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}

	indicator, ok := args["target"].(string)
	if !ok || indicator == "" {
		return nil, goerr.New("target is required", goerr.V("name", name), goerr.V("args", args))
	}

	return t.query(ctx, indicatorType, indicator)
}

// Ping verifies connectivity and credentials by querying a well-known IPv4
// indicator.
func (t *ToolSet) Ping(ctx context.Context) error {
	if _, err := t.query(ctx, "IPv4", "8.8.8.8"); err != nil {
		return goerr.Wrap(err, "OTX ping failed")
	}
	return nil
}

func (t *ToolSet) query(ctx context.Context, indicatorType, indicator string) (map[string]any, error) {
	endpoint := t.baseURL + "/indicators/" + indicatorType + "/" + url.PathEscape(indicator) + "/general"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create request", goerr.V("url", endpoint))
	}
	req.Header.Set("X-OTX-API-KEY", t.apiKey)

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
		return nil, eb.New("failed to query OTX", goerr.V("body", string(body)))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, eb.Wrap(err, "failed to unmarshal response", goerr.V("body", string(body)))
	}

	return result, nil
}
