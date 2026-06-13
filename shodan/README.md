# shodan

[Shodan](https://www.shodan.io/) host, domain, and search lookups for the
[gollem](https://github.com/gollem-dev/gollem) LLM agent framework.

```
github.com/gollem-dev/tools/shodan
```

## Tools

| Name | Description |
|------|-------------|
| `shodan_host` | Search host information from Shodan by IP. |
| `shodan_domain` | Search domain information from Shodan. |
| `shodan_search` | Search the internet using a Shodan query. |

## Usage

```go
ts, err := shodan.New("YOUR_API_KEY")
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
| `WithBaseURL(string)` | `https://api.shodan.io` |
| `WithHTTPClient(*http.Client)` | `http.DefaultClient` |
| `WithLogger(*slog.Logger)` | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when
`TEST_SHODAN_API_KEY` is set:

```sh
TEST_SHODAN_API_KEY=... go test ./...
```
