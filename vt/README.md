# vt

VirusTotal indicator lookups for the
[gollem](https://github.com/gollem-dev/gollem) LLM agent framework.

```
github.com/gollem-dev/tools/vt
```

## Tools

| Name | Description |
|------|-------------|
| `vt_ip` | Search IPv4/IPv6 indicator from VirusTotal. |
| `vt_domain` | Search domain indicator from VirusTotal. |
| `vt_file_hash` | Search file hash indicator from VirusTotal. |
| `vt_url` | Search URL indicator from VirusTotal. |

## Usage

```go
ts, err := vt.New("YOUR_API_KEY")
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
| `WithBaseURL(string)` | `https://www.virustotal.com/api/v3` |
| `WithHTTPClient(*http.Client)` | `http.DefaultClient` |
| `WithLogger(*slog.Logger)` | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when
`TEST_VT_API_KEY` is set:

```sh
TEST_VT_API_KEY=... go test ./...
```
