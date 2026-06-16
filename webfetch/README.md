# webfetch

Fetch web pages and extract their text — with optional LLM-based indirect
prompt-injection analysis — for the
[gollem](https://github.com/gollem-dev/gollem) LLM agent framework.

```
github.com/gollem-dev/tools/webfetch
```

## Tools

| Name | Description |
|------|-------------|
| `web_fetch` | Fetch a web page and extract its text, optionally with LLM-based indirect prompt-injection screening. |

When an LLM client is supplied via `WithLLMClient`, fetched content is screened
for indirect prompt-injection attempts before being returned to the agent.
Without it, the page text is returned as-is.

## SSRF guard

Because `web_fetch` may be handed URLs from untrusted sources (LLM output, chat
messages, case data), the default HTTP client enforces an SSRF guard: a
`net.Dialer.Control` hook inspects the **already-resolved** destination IP and
rejects anything that is not a public, global-unicast address — loopback,
RFC1918/ULA private ranges, CGNAT (`100.64.0.0/10`), link-local (including the
`169.254.169.254` metadata endpoint), unspecified, and multicast. Inspecting the
resolved IP defeats DNS rebinding and applies to every redirect hop.

The guard is **enabled by default**. Disable it with `WithAllowPrivateIP(true)`
(e.g. to reach a loopback test server). When you inject your own client via
`WithHTTPClient`, the guard is not installed — that client's transport is used
as-is, so add your own dial control if needed.

## Usage

```go
ts, err := webfetch.New(
	webfetch.WithLLMClient(llm), // optional: enables injection screening
)
if err != nil {
	return err
}
if err := ts.Ping(ctx); err != nil { // optional preflight
	return err
}
```

## Options

| Option | Required | Default |
|--------|----------|---------|
| `WithLLMClient(gollem.LLMClient)` | no | none (screening disabled) |
| `WithMaxContentBytes(int64)` | no | 10 MiB |
| `WithAllowPrivateIP(bool)` | no | `false` (SSRF guard enabled) |
| `WithHTTPClient(*http.Client)` | no | guarded built-in client |
| `WithLogger(*slog.Logger)` | no | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when
`TEST_WEBFETCH_URL` is set; the injection-screening path additionally needs
`TEST_GEMINI_PROJECT_ID` and `TEST_GEMINI_LOCATION`:

```sh
TEST_WEBFETCH_URL=https://example.com \
	TEST_GEMINI_PROJECT_ID=... TEST_GEMINI_LOCATION=us-central1 go test ./...
```
