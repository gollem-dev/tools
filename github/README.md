# github

GitHub App tools — code search, issue search, file content, commit history,
blame, and single issue/PR retrieval — for the
[gollem](https://github.com/gollem-dev/gollem) LLM agent framework.

```
github.com/gollem-dev/tools/github
```

## Tools

| Name | Description |
|------|-------------|
| `github_code_search` | Search code across GitHub repositories. |
| `github_issue_search` | Search issues and pull requests. |
| `github_get_content` | Get file content from a repository. |
| `github_list_commits` | List commits for a repository. |
| `github_get_blame` | Get git blame information for a file. |
| `github_get_issue` | Fetch a single issue with body, labels, and all comments. |
| `github_get_pull_request` | Fetch a single PR with body, labels, comments, reviews, and optionally the file diff. |

## Usage

```go
ts, err := github.New(123456, 7890123, pemString)
if err != nil {
	return err
}
if err := ts.Ping(ctx); err != nil { // optional preflight
	return err
}
```

Authentication is via a GitHub App installation: the tool mints installation
access tokens from the App ID, installation ID, and the App's PEM private key.

## Options

| Option | Required | Default |
|--------|----------|---------|
| `WithLogger(*slog.Logger)` | no | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when the
`TEST_GITHUB_*` variables are set:

```sh
TEST_GITHUB_APP_ID=... TEST_GITHUB_APP_INSTALLATION_ID=... \
	TEST_GITHUB_APP_PRIVATE_KEY="$(cat key.pem)" \
	TEST_GITHUB_REPO=owner/repo go test ./...
```

The `github_get_issue` and `github_get_pull_request` live subtests additionally
require `TEST_GITHUB_ISSUE_NUMBER` and `TEST_GITHUB_PR_NUMBER` respectively;
each subtest skips when its variable is unset.
