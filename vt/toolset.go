// Package vt provides a gollem.ToolSet for VirusTotal threat-intelligence
// lookups (IP addresses, domains, file hashes, and URLs).
package vt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/gollem-dev/gollem"
	"github.com/gollem-dev/tools/internal/safe"
	"github.com/m-mizutani/goerr/v2"
)

const defaultBaseURL = "https://www.virustotal.com/api/v3"

// ToolSet implements gollem.ToolSet for VirusTotal. Fields are unexported;
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

// WithAPIKey sets the VirusTotal API key. It is required.
func WithAPIKey(key string) Option {
	return func(t *ToolSet) { t.apiKey = key }
}

// WithBaseURL overrides the VirusTotal API base URL.
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
		return nil, goerr.New("VirusTotal API key is required")
	}
	if _, err := url.Parse(t.baseURL); err != nil {
		return nil, goerr.Wrap(err, "invalid base URL", goerr.V("base_url", t.baseURL))
	}

	return t, nil
}

// indicatorTypes maps each tool name to the VirusTotal API path segment.
var indicatorTypes = map[string]string{
	"vt_ip":        "ip_addresses",
	"vt_domain":    "domains",
	"vt_file_hash": "files",
	"vt_url":       "urls",
}

// Specs returns the VirusTotal tool specifications.
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
		{Name: "vt_ip", Description: "Search the indicator of IPv4/IPv6 from VirusTotal.", Parameters: target("The IP address to search")},
		{Name: "vt_domain", Description: "Search the indicator of domain from VirusTotal.", Parameters: target("The domain to search")},
		{Name: "vt_file_hash", Description: "Search the indicator of file hash from VirusTotal.", Parameters: target("The file hash to search")},
		{Name: "vt_url", Description: "Search the indicator of URL from VirusTotal.", Parameters: target("The URL to search")},
	}, nil
}

// Run executes the named VirusTotal lookup.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	indicatorType, ok := indicatorTypes[name]
	if !ok {
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}

	indicator, ok := args["target"].(string)
	if !ok || indicator == "" {
		return nil, goerr.New("target is required", goerr.V("name", name), goerr.V("args", args))
	}

	// VirusTotal's /urls/{id} endpoint requires the URL to be encoded as
	// base64url (RawURLEncoding, no padding) rather than percent-escaped, because
	// the raw URL contains characters that are not valid path segments.
	if name == "vt_url" {
		id := base64.RawURLEncoding.EncodeToString([]byte(indicator))
		return t.queryRaw(ctx, indicatorType+"/"+id)
	}

	return t.query(ctx, indicatorType, indicator)
}

// Ping verifies connectivity and credentials by querying a well-known IP
// address indicator.
func (t *ToolSet) Ping(ctx context.Context) error {
	if _, err := t.query(ctx, "ip_addresses", "8.8.8.8"); err != nil {
		return goerr.Wrap(err, "VirusTotal ping failed")
	}
	return nil
}

// query builds an endpoint from indicatorType and a percent-escaped indicator
// segment, then delegates to queryRaw.
func (t *ToolSet) query(ctx context.Context, indicatorType, indicator string) (map[string]any, error) {
	return t.queryRaw(ctx, indicatorType+"/"+url.PathEscape(indicator))
}

// queryRaw fetches the given path (relative to baseURL) from the VirusTotal API.
func (t *ToolSet) queryRaw(ctx context.Context, path string) (map[string]any, error) {
	endpoint := t.baseURL + "/" + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create request", goerr.V("url", endpoint))
	}
	req.Header.Set("x-apikey", t.apiKey)

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
		return nil, eb.New("failed to query VirusTotal", goerr.V("body", string(body)))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, eb.Wrap(err, "failed to unmarshal response", goerr.V("body", string(body)))
	}

	return result, nil
}
