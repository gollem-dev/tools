# abusech

abuse.ch [MalwareBazaar](https://bazaar.abuse.ch/) malware-sample lookups for the
[gollem](https://github.com/gollem-dev/gollem) LLM agent framework.

```
github.com/gollem-dev/tools/abusech
```

## Tools

| Name | Description |
|------|-------------|
| `abusech.bazaar.query` | Query malware information from MalwareBazaar by file hash value. |

## Usage

```go
ts, err := abusech.New(abusech.WithAPIKey("..."))
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
| `WithBaseURL(string)` | no | `https://mb-api.abuse.ch/api/v1` |
| `WithHTTPClient(*http.Client)` | no | `http.DefaultClient` |
| `WithLogger(*slog.Logger)` | no | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when
`TEST_ABUSECH_API_KEY` is set:

```sh
TEST_ABUSECH_API_KEY=... go test ./...
```
