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

Both tools require a **user** token (`xoxp-...`); bot tokens cannot call
`search.messages`. Required scopes per tool:

| Tool | Scopes |
|------|--------|
| `slack_message_search` | `search:read` |
| `slack_get_messages` | `conversations.replies` needs the relevant `*:history` read scopes for the conversations you fetch — `channels:history` (public), `groups:history` (private), `im:history` (DMs), `mpim:history` (group DMs). `chat.getPermalink` needs no extra scope. |

A user token can read public channels via `conversations.replies` even when no
bot has joined them. Missing a `*:history` scope surfaces as a `missing_scope`
error in that target's result.

> **Note:** As of 2025-05-29, Slack reduced `conversations.replies` to a default
> and maximum `limit` of **15** (and 1 request/minute) for apps newly distributed
> outside the Marketplace. `slack_get_messages` defaults `thread_limit` to 15 so
> it works on both the legacy and the new tier.

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
