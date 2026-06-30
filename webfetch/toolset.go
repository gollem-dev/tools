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
	tools           []gollem.Tool
	toolByName      map[string]gollem.Tool
}

// webFetchInput is the typed input for the web_fetch tool. The schema is inferred
// from the struct tags, eliminating a separate hand-written parameter map.
type webFetchInput struct {
	URL string `json:"url" description:"The URL to fetch (http or https only)" required:"true"`
}

// Startup assertions: a malformed input/output type (a broken struct tag, a
// non-object kind) is a programming error that should surface at init rather
// than on the first LLM call. See gollem docs "Validating Tool Types".
var (
	_ = gollem.MustToolSchema[webFetchInput, map[string]any]()
)

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

	t.tools = t.buildTools()
	t.toolByName = indexTools(t.tools)

	return t, nil
}

// indexTools builds a name->tool lookup so Run dispatches in O(1) instead of
// scanning (and re-deriving Spec()) on every call. The map is built once at
// construction and never mutated, so it is safe for concurrent Run calls.
func indexTools(tools []gollem.Tool) map[string]gollem.Tool {
	byName := make(map[string]gollem.Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Spec().Name] = tool
	}
	return byName
}

// buildTools constructs the typed web_fetch tool. The input struct encodes the
// schema, so there is no separate hand-written parameter map to drift from the
// handler implementation.
// MustNewTool is used because the In/Out types are static: a build failure is a programming error (already guarded by the package-level MustToolSchema), not a runtime condition New should report.
func (t *ToolSet) buildTools() []gollem.Tool {
	const toolDesc = "Fetch a web page and return its body. " +
		"When LLM analysis is enabled, the body is reformatted as Markdown and " +
		"screened for indirect prompt injection; otherwise the extracted text is " +
		"returned verbatim."

	tool := gollem.MustNewTool("web_fetch", toolDesc,
		func(ctx context.Context, in webFetchInput) (map[string]any, error) {
			if in.URL == "" {
				return nil, goerr.New("url is required", goerr.V("args", map[string]any{"url": in.URL}))
			}

			parsed, err := url.Parse(in.URL)
			if err != nil {
				return nil, goerr.Wrap(err, "failed to parse url", goerr.V("url", in.URL))
			}
			switch parsed.Scheme {
			case "http", "https":
				// accepted
			default:
				return nil, goerr.New("unsupported url scheme (only http/https are allowed)",
					goerr.V("url", in.URL),
					goerr.V("scheme", parsed.Scheme))
			}
			if parsed.Host == "" {
				return nil, goerr.New("url is missing a host", goerr.V("url", in.URL))
			}

			status, contentType, body, err := t.fetch(ctx, in.URL)
			if err != nil {
				return nil, err
			}

			text, _, err := extractContent(contentType, body)
			if err != nil {
				return nil, goerr.Wrap(err, "failed to extract body", goerr.V("url", in.URL))
			}

			if t.llmClient == nil {
				// LLM analysis disabled: return the extracted text verbatim.
				return map[string]any{
					"result":       text,
					"url":          in.URL,
					"status":       status,
					"content_type": contentType,
					"llm_analysis": "disabled",
				}, nil
			}

			result, err := analyzeContent(ctx, t.llmClient, text)
			if err != nil {
				return nil, goerr.Wrap(err, "failed to analyze body", goerr.V("url", in.URL))
			}

			if result.Malicious {
				return nil, goerr.New("indirect prompt injection detected in fetched body",
					goerr.V("url", in.URL),
					goerr.V("reason", result.Reason))
			}

			return map[string]any{
				"result":       result.Markdown,
				"url":          in.URL,
				"status":       status,
				"content_type": contentType,
			}, nil
		})
	return []gollem.Tool{tool}
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
	specs := make([]gollem.ToolSpec, len(t.tools))
	for i, tool := range t.tools {
		specs[i] = tool.Spec()
	}
	return specs, nil
}

// Run dispatches tool calls by name to the matching typed tool.
func (t *ToolSet) Run(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	tool, ok := t.toolByName[name]
	if !ok {
		return nil, goerr.New("invalid function name", goerr.V("name", name))
	}
	return tool.Run(ctx, args)
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
