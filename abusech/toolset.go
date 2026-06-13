// Package abusech provides a gollem.ToolSet for abuse.ch MalwareBazaar hash
// lookups.
package abusech

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gollem-dev/gollem"
	"github.com/gollem-dev/tools/internal/safe"
	"github.com/m-mizutani/goerr/v2"
)

const defaultBaseURL = "https://mb-api.abuse.ch/api/v1"

// ToolSet implements gollem.ToolSet for abuse.ch MalwareBazaar. Fields are
// unexported; configure via Option.
type ToolSet struct {
	apiKey  string
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithAPIKey sets the abuse.ch API key. It is required.
func WithAPIKey(key string) Option {
	return func(t *ToolSet) { t.apiKey = key }
}

// WithBaseURL overrides the MalwareBazaar API base URL.
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
		return nil, goerr.New("abuse.ch API key is required")
	}
	if _, err := url.Parse(t.baseURL); err != nil {
		return nil, goerr.Wrap(err, "invalid base URL", goerr.V("base_url", t.baseURL))
	}

	return t, nil
}

// Specs returns the MalwareBazaar tool specifications.
func (t *ToolSet) Specs(ctx context.Context) ([]gollem.ToolSpec, error) {
	return []gollem.ToolSpec{
		{
			Name:        "abusech.bazaar.query",
			Description: "Query malware information from MalwareBazaar by file hash value.",
			Parameters: map[string]*gollem.Parameter{
				"hash": {
					Type:        gollem.TypeString,
					Description: "The hash value (MD5, SHA1, or SHA256) to query",
					Required:    true,
				},
			},
		},
	}, nil
}

// Run executes the named MalwareBazaar lookup.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	switch name {
	case "abusech.bazaar.query":
		hash, ok := args["hash"].(string)
		if !ok || hash == "" {
			return nil, goerr.New("hash is required", goerr.V("name", name), goerr.V("args", args))
		}
		return t.query(ctx, hash)
	default:
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}
}

// Ping verifies connectivity and credentials by querying a well-known SHA256
// hash (Mirai malware sample present in MalwareBazaar).
func (t *ToolSet) Ping(ctx context.Context) error {
	// Use a known hash that exists in MalwareBazaar. We do not validate the
	// returned data — a successful HTTP 200 response is sufficient.
	if _, err := t.query(ctx, "b3d9ea5fb70f1b6ecf74f36e3a88da0ac44dd8afb4a4d70e8f15fa10f6fa3f7b"); err != nil {
		return goerr.Wrap(err, "abuse.ch ping failed")
	}
	return nil
}

// query posts a get_info request to MalwareBazaar for the given hash value.
func (t *ToolSet) query(ctx context.Context, hash string) (map[string]any, error) {
	endpoint := t.baseURL + "/"

	formData := url.Values{}
	formData.Set("query", "get_info")
	formData.Set("hash", hash)
	body := formData.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create request", goerr.V("url", endpoint))
	}
	req.Header.Set("Auth-Key", t.apiKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	eb := goerr.NewBuilder(goerr.V("url", endpoint), goerr.V("hash", hash))

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, eb.Wrap(err, "failed to send request")
	}
	defer safe.Close(t.logger, resp.Body)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, eb.Wrap(err, "failed to read response body", goerr.V("status", resp.StatusCode))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, eb.New("unexpected status from MalwareBazaar",
			goerr.V("status", resp.StatusCode),
			goerr.V("body", string(respBody)))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, eb.Wrap(err, "failed to unmarshal response", goerr.V("body", string(respBody)))
	}

	// Surface API-level errors reported inside the JSON payload.
	if status, ok := result["query_status"].(string); ok && status == "error" {
		errMsg, _ := result["error_message"].(string)
		if errMsg == "" {
			errMsg, _ = result["error"].(string)
		}
		return nil, eb.New("MalwareBazaar API returned error", goerr.V("error", errMsg))
	}

	return result, nil
}
