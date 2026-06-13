# otx

AlienVault OTX (Open Threat Exchange) threat-intelligence lookups for the
[gollem](https://github.com/gollem-dev/gollem) LLM agent framework.

```
github.com/gollem-dev/tools/otx
```

## Tools

| Name | Description |
|------|-------------|
| `otx_ipv4` | Search IPv4 indicator from OTX. |
| `otx_ipv6` | Search IPv6 indicator from OTX. |
| `otx_domain` | Search domain indicator from OTX. |
| `otx_hostname` | Search hostname indicator from OTX. |
| `otx_file_hash` | Search file hash indicator from OTX. |

## Usage

```go
ts, err := otx.New(otx.WithAPIKey("..."))
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
| `WithAPIKey(string)` | yes | — |
| `WithBaseURL(string)` | no | `https://otx.alienvault.com/api/v1` |
| `WithHTTPClient(*http.Client)` | no | `http.DefaultClient` |
| `WithLogger(*slog.Logger)` | no | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when
`TEST_OTX_API_KEY` is set:

```sh
TEST_OTX_API_KEY=... go test ./...
```
