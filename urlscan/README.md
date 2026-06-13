# urlscan

Submit URLs to [urlscan.io](https://urlscan.io/) and retrieve scan results, for
the [gollem](https://github.com/gollem-dev/gollem) LLM agent framework.

```
github.com/gollem-dev/tools/urlscan
```

## Tools

| Name | Description |
|------|-------------|
| `urlscan_scan` | Scan a URL with urlscan.io to analyse its content and behaviour. |

The tool submits the URL, then polls for the finished result, honouring
`WithBackoff` between polls and giving up after `WithTimeout`.

## Usage

```go
ts, err := urlscan.New("your-api-key")
if err != nil {
	return err
}
if err := ts.Ping(ctx); err != nil { // optional preflight
	return err
}
```

## Options

| Option | Default |
|--------|---------|
| `WithBaseURL(string)` | `https://urlscan.io/api/v1` |
| `WithHTTPClient(*http.Client)` | `http.DefaultClient` |
| `WithBackoff(time.Duration)` | `3s` |
| `WithTimeout(time.Duration)` | `30s` |
| `WithLogger(*slog.Logger)` | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when
`TEST_URLSCAN_API_KEY` is set:

```sh
TEST_URLSCAN_API_KEY=... go test ./...
```
