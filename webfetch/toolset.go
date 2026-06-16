// Package webfetch provides a gollem.ToolSet that fetches a web page, extracts
// its text content, and optionally screens it for indirect prompt injection via
// an injected LLM client.
package webfetch

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/gollem-dev/gollem"
	"github.com/m-mizutani/goerr/v2"
)

const (
	defaultTimeout = 30 * time.Second
	userAgent      = "gollem-webfetch/1.0 (+https://github.com/gollem-dev/tools)"

	// defaultMaxContentBytes caps the number of bytes read from a response body
	// to prevent memory exhaustion when fetching large or adversarial payloads.
	defaultMaxContentBytes = 10 * 1024 * 1024 // 10 MiB
)

// ToolSet implements gollem.ToolSet for web-page fetching with optional
// LLM-based indirect prompt injection analysis. All fields are unexported;
// configure via Option.
type ToolSet struct {
	client          *http.Client
	logger          *slog.Logger
	llmClient       gollem.LLMClient
	maxContentBytes int64
	allowPrivateIP  bool
}

var _ gollem.ToolSet = (*ToolSet)(nil)

// Option configures a ToolSet.
type Option func(*ToolSet)

// WithHTTPClient overrides the HTTP client used for requests. A nil value is
// ignored and the default client is kept.
//
// An injected client carries its own transport, so the built-in SSRF guard
// (see WithAllowPrivateIP) is NOT installed on it. Supplying a client is the
// documented escape hatch for callers that need full control over dialing.
func WithHTTPClient(client *http.Client) Option {
	return func(t *ToolSet) {
		if client != nil {
			t.client = client
		}
	}
}

// WithAllowPrivateIP controls the SSRF guard on the default HTTP client. The
// guard is enabled by default (allow == false): connections to non-public IPs
// (loopback, RFC1918/ULA private ranges, CGNAT, link-local metadata endpoints,
// etc.) are rejected at dial time on every redirect hop. Pass true to disable
// it, e.g. to reach a loopback test server. This has no effect when a client is
// injected via WithHTTPClient.
func WithAllowPrivateIP(allow bool) Option {
	return func(t *ToolSet) {
		t.allowPrivateIP = allow
	}
}

// WithLogger sets the logger. A nil argument keeps the default (slog.Default()).
func WithLogger(logger *slog.Logger) Option {
	return func(t *ToolSet) {
		if logger != nil {
			t.logger = logger
		}
	}
}

// WithLLMClient injects an LLM client for the analyze step.  When set, each
// web_fetch call passes the extracted text through an indirect-prompt-injection
// analysis session and returns the cleaned Markdown on success.  When nil or
// not provided, the analyze step is disabled and the raw extracted text is
// returned verbatim.
func WithLLMClient(client gollem.LLMClient) Option {
	return func(t *ToolSet) {
		t.llmClient = client
	}
}

// WithMaxContentBytes sets the maximum number of bytes that will be read from
// an HTTP response body. Responses longer than this limit are truncated before
// extraction. The default is 10 MiB. A value <= 0 is ignored.
func WithMaxContentBytes(n int64) Option {
	return func(t *ToolSet) {
		if n > 0 {
			t.maxContentBytes = n
		}
	}
}

// New constructs a ToolSet. It performs only static validation; no network I/O
// is performed.  Use Ping to verify connectivity.
func New(opts ...Option) (*ToolSet, error) {
	t := &ToolSet{
		logger:          slog.Default(),
		maxContentBytes: defaultMaxContentBytes,
	}
	for _, opt := range opts {
		opt(t)
	}
	// Build the guarded default client only when the caller did not inject one
	// via WithHTTPClient. allowPrivateIP is read here, after options are applied,
	// so the guard reflects the final configuration.
	if t.client == nil {
		t.client = newGuardedClient(t.allowPrivateIP)
	}
	return t, nil
}

// Ping checks whether the configured dependencies are reachable. If an LLM
// client is set, it creates a session and performs a trivial generate call to
// confirm the client is operational; if no client is set, Ping always returns
// nil (the tool still works in fetch-only mode).
func (t *ToolSet) Ping(ctx context.Context) error {
	if t.llmClient == nil {
		return nil
	}

	session, err := t.llmClient.NewSession(ctx,
		gollem.WithSessionContentType(gollem.ContentTypeJSON),
	)
	if err != nil {
		return goerr.Wrap(err, "webfetch ping: failed to create LLM session")
	}

	resp, err := session.Generate(ctx, []gollem.Input{gollem.Text("ping")})
	if err != nil {
		return goerr.Wrap(err, "webfetch ping: LLM generate failed")
	}

	outputTokens := 0
	if resp != nil {
		outputTokens = resp.OutputToken
	}
	t.logger.DebugContext(ctx, "webfetch LLM ping succeeded",
		slog.Int("output_tokens", outputTokens))

	return nil
}

// Specs returns the tool specifications exposed by this ToolSet.
func (t *ToolSet) Specs(_ context.Context) ([]gollem.ToolSpec, error) {
	return []gollem.ToolSpec{
		{
			Name: "web_fetch",
			Description: "Fetch a web page and return its body. " +
				"When LLM analysis is enabled, the body is reformatted as Markdown and " +
				"screened for indirect prompt injection; otherwise the extracted text is " +
				"returned verbatim.",
			Parameters: map[string]*gollem.Parameter{
				"url": {
					Type:        gollem.TypeString,
					Description: "The URL to fetch (http or https only)",
					Required:    true,
				},
			},
		},
	}, nil
}

// Run dispatches tool calls. Only "web_fetch" is supported.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	if name != "web_fetch" {
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}

	rawURL, ok := args["url"].(string)
	if !ok || rawURL == "" {
		return nil, goerr.New("url is required", goerr.V("args", args))
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to parse url", goerr.V("url", rawURL))
	}
	switch parsed.Scheme {
	case "http", "https":
		// accepted
	default:
		return nil, goerr.New("unsupported url scheme (only http/https are allowed)",
			goerr.V("url", rawURL),
			goerr.V("scheme", parsed.Scheme))
	}
	if parsed.Host == "" {
		return nil, goerr.New("url is missing a host", goerr.V("url", rawURL))
	}

	status, contentType, body, err := t.fetch(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	text, _, err := extractContent(contentType, body)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to extract body", goerr.V("url", rawURL))
	}

	if t.llmClient == nil {
		// LLM analysis disabled: return the extracted text verbatim.
		return map[string]any{
			"result":       text,
			"url":          rawURL,
			"status":       status,
			"content_type": contentType,
			"llm_analysis": "disabled",
		}, nil
	}

	result, err := analyzeContent(ctx, t.llmClient, text)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to analyze body", goerr.V("url", rawURL))
	}

	if result.Malicious {
		return nil, goerr.New("indirect prompt injection detected in fetched body",
			goerr.V("url", rawURL),
			goerr.V("reason", result.Reason))
	}

	return map[string]any{
		"result":       result.Markdown,
		"url":          rawURL,
		"status":       status,
		"content_type": contentType,
	}, nil
}

// fetch performs the HTTP GET, enforcing the configured timeout and a body-size
// cap. It sets a stable User-Agent to identify requests.
func (t *ToolSet) fetch(ctx context.Context, rawURL string) (status int, contentType string, body []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, "", nil, goerr.Wrap(err, "failed to create http request", goerr.V("url", rawURL))
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := t.client.Do(req)
	if err != nil {
		return 0, "", nil, goerr.Wrap(err, "failed to fetch url", goerr.V("url", rawURL))
	}
	defer safeClose(t.logger, resp.Body)

	limited := io.LimitReader(resp.Body, t.maxContentBytes)
	b, err := io.ReadAll(limited)
	if err != nil {
		return resp.StatusCode, resp.Header.Get("Content-Type"), nil,
			goerr.Wrap(err, "failed to read response body", goerr.V("url", rawURL))
	}

	return resp.StatusCode, resp.Header.Get("Content-Type"), b, nil
}
