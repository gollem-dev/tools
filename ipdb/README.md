# ipdb

[AbuseIPDB](https://www.abuseipdb.com/) IP-address reputation and
abuse-confidence checks for the
[gollem](https://github.com/gollem-dev/gollem) LLM agent framework.

```
github.com/gollem-dev/tools/ipdb
```

## Tools

| Name | Description |
|------|-------------|
| `ipdb_check` | Check IP address information from AbuseIPDB. |

## Usage

```go
ts, err := ipdb.New(ipdb.WithAPIKey("..."))
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
| `WithBaseURL(string)` | no | `https://api.abuseipdb.com/api/v2` |
| `WithHTTPClient(*http.Client)` | no | `http.DefaultClient` |
| `WithLogger(*slog.Logger)` | no | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when
`TEST_IPDB_API_KEY` is set:

```sh
TEST_IPDB_API_KEY=... go test ./...
```
