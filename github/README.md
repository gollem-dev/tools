# github

GitHub App tools — code search, issue search, file content, commit history, and
blame — for the [gollem](https://github.com/gollem-dev/gollem) LLM agent
framework.

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

## Usage

```go
ts, err := github.New(
	github.WithAppID(123456),
	github.WithInstallationID(7890123),
	github.WithPrivateKey(pemString),
)
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
| `WithAppID(int64)` | yes | — |
| `WithInstallationID(int64)` | yes | — |
| `WithPrivateKey(string)` | yes | — |
| `WithLogger(*slog.Logger)` | no | `slog.Default()` |

## Testing

Mock tests run unconditionally. The live-service test runs only when the
`TEST_GITHUB_*` variables are set:

```sh
TEST_GITHUB_APP_ID=... TEST_GITHUB_APP_INSTALLATION_ID=... \
	TEST_GITHUB_APP_PRIVATE_KEY="$(cat key.pem)" \
	TEST_GITHUB_REPO=owner/repo go test ./...
```
