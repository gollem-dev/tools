# jira

Read-only Jira Cloud integration for the
[gollem](https://github.com/gollem-dev/gollem) LLM agent framework: list
projects, search issues with JQL, and fetch issue content (with descriptions and
comments rendered to Markdown).

```
github.com/gollem-dev/tools/jira
```

## Tools

| Name | Description |
|------|-------------|
| `jira_list_projects` | List projects accessible to the account (id, key, name, type, lead), with pagination. |
| `jira_search_issues` | Search issues with JQL; returns key, summary, status, type, assignee, priority, and updated time. A `project` argument is spliced into the JQL via AND for convenience. |
| `jira_get_issues` | Fetch one or more issues by key/id in a single batch request. Descriptions (and optional comments) are returned as Markdown; unresolved keys are listed in `not_found`. |

## Usage

```go
ts, err := jira.New(
    "https://your-domain.atlassian.net", // tenant site URL (required)
    "you@example.com",                   // account email (required)
    "your-api-token",                    // Jira API token (required)
)
if err != nil {
    return err
}
if err := ts.Ping(ctx); err != nil { // optional preflight
    return err
}
```

Authentication uses Jira Cloud Basic auth: an account email plus an
[API token](https://id.atlassian.com/manage-profile/security/api-tokens). Because
each Jira site lives on its own tenant domain
(`https://<your-domain>.atlassian.net`), the base URL is a required argument
rather than a fixed default.

The description/comment bodies are stored by Jira as Atlassian Document Format
(ADF) JSON; this tool walks the ADF node tree and renders it to Markdown
(headings, lists, code blocks, blockquotes, links, and inline marks). Unknown
node types degrade to their plain text rather than being dropped.

## Options

| Option | Default |
|--------|---------|
| `WithHTTPClient(*http.Client)` | `&http.Client{Timeout: 30s}` |
| `WithLogger(*slog.Logger)` | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when
`TEST_JIRA_BASE_URL`, `TEST_JIRA_EMAIL`, and `TEST_JIRA_API_TOKEN` are all set;
`TEST_JIRA_PROJECT` and `TEST_JIRA_ISSUE_KEY` enable the search and get-issues
subtests respectively:

```sh
TEST_JIRA_BASE_URL=https://your-domain.atlassian.net \
TEST_JIRA_EMAIL=you@example.com \
TEST_JIRA_API_TOKEN=... \
TEST_JIRA_PROJECT=PROJ \
TEST_JIRA_ISSUE_KEY=PROJ-1 \
go test ./...
```
