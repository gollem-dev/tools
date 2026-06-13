# slack

Search Slack messages via the
[`search.messages`](https://api.slack.com/methods/search.messages) API, for the
[gollem](https://github.com/gollem-dev/gollem) LLM agent framework.

```
github.com/gollem-dev/tools/slack
```

## Tools

| Name | Description |
|------|-------------|
| `slack_message_search` | Search messages in a Slack workspace using the `search.messages` API. |

## Usage

```go
ts, err := slack.New("xoxp-...")
if err != nil {
	return err
}
if err := ts.Ping(ctx); err != nil { // optional preflight
	return err
}
```

`search.messages` is only available with a **user** token (`xoxp-...`) carrying
the `search:read` scope; bot tokens cannot call it.

## Options

The first argument to `New` is the required user token (`xoxp-…` with the
`search:read` scope).

| Option | Default |
|--------|---------|
| `WithBaseURL(string)` | `https://slack.com/api` |
| `WithHTTPClient(*http.Client)` | `http.DefaultClient` |
| `WithLogger(*slog.Logger)` | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when
`TEST_SLACK_USER_TOKEN` and `TEST_SLACK_QUERY` are set:

```sh
TEST_SLACK_USER_TOKEN=xoxp-... TEST_SLACK_QUERY="deploy" go test ./...
```
