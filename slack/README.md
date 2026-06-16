# slack

Search Slack messages and fetch messages with their thread context, for the
[gollem](https://github.com/gollem-dev/gollem) LLM agent framework.

```
github.com/gollem-dev/tools/slack
```

## Tools

| Name | Description |
|------|-------------|
| `slack_message_search` | Search messages in a Slack workspace using the `search.messages` API. |
| `slack_get_messages` | Fetch up to 10 messages and their thread context in parallel, by channel ID and timestamp. |

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
the `search:read` scope; bot tokens cannot call it. `slack_get_messages` calls
`conversations.replies` / `chat.getPermalink`; a user token can read public
channels even when no bot has joined them.

## Options

The first argument to `New` is the required user token (`xoxp-…` with the
`search:read` scope).

| Option | Default |
|--------|---------|
| `WithBaseURL(string)` | `https://slack.com/api` |
| `WithHTTPClient(*http.Client)` | `http.DefaultClient` |
| `WithLogger(*slog.Logger)` | `slog.Default()` |

## Testing

Mock tests run unconditionally. The `slack_message_search` live test runs only
when `TEST_SLACK_USER_TOKEN` and `TEST_SLACK_QUERY` are set:

```sh
TEST_SLACK_USER_TOKEN=xoxp-... TEST_SLACK_QUERY="deploy" go test ./...
```

The `slack_get_messages` live test additionally requires `TEST_SLACK_CHANNEL_ID`
and `TEST_SLACK_TS` (a channel ID and message timestamp to fetch):

```sh
TEST_SLACK_USER_TOKEN=xoxp-... \
	TEST_SLACK_CHANNEL_ID=C0123ABCD TEST_SLACK_TS=1700000000.000100 go test ./...
```
