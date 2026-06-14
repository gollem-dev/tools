# notion

Read-oriented Notion integration for the
[gollem](https://github.com/gollem-dev/gollem) LLM agent framework: search shared
pages/databases, read a page as Markdown, and query database rows.

```
github.com/gollem-dev/tools/notion
```

## Tools

| Name | Description |
|------|-------------|
| `notion_search` | Search pages and databases shared with the integration by title. |
| `notion_get_page` | Retrieve a page's full content as Notion-flavored Markdown. |
| `notion_query_database` | Query a database's rows with their flattened properties. |

## Usage

```go
ts, err := notion.New("ntn_your-integration-token")
if err != nil {
	return err
}
if err := ts.Ping(ctx); err != nil { // optional preflight
	return err
}
```

The token must be a Notion integration token with read access, and the target
pages/databases must be shared with the integration.

## Options

| Option | Default |
|--------|---------|
| `WithBaseURL(string)` | `https://api.notion.com` |
| `WithHTTPClient(*http.Client)` | `&http.Client{Timeout: 30s}` |
| `WithLogger(*slog.Logger)` | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when
`TEST_NOTION_TOKEN` is set; `TEST_NOTION_PAGE_ID` and `TEST_NOTION_DATABASE_ID`
enable the page and database subtests respectively:

```sh
TEST_NOTION_TOKEN=... TEST_NOTION_PAGE_ID=... TEST_NOTION_DATABASE_ID=... go test ./...
```
