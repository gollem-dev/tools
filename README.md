# Gollem Tools

Ready-to-use tool integrations for the [gollem](https://github.com/gollem-dev/gollem)
LLM agent framework. Each tool implements `gollem.ToolSet` and is published as its
own Go module, so you only pull the dependencies of the tools you actually use.

## Available tools

| Module | Description |
|--------|-------------|
| `github.com/gollem-dev/tools/otx` | AlienVault OTX threat-intelligence lookups (IPv4 / IPv6 / domain / hostname / file hash). |
| `github.com/gollem-dev/tools/vt` | VirusTotal indicator lookups (IP / domain / file hash / URL). |
| `github.com/gollem-dev/tools/abusech` | abuse.ch MalwareBazaar malware sample lookups. |
| `github.com/gollem-dev/tools/ipdb` | AbuseIPDB IP address reputation / abuse-confidence checks. |
| `github.com/gollem-dev/tools/shodan` | Shodan host, domain, and search lookups. |
| `github.com/gollem-dev/tools/whois` | WHOIS registration lookups for domain names and IP addresses. |
| `github.com/gollem-dev/tools/urlscan` | Submit URLs to urlscan.io and retrieve scan results. |
| `github.com/gollem-dev/tools/slack` | Search Slack messages via the `search.messages` API. |
| `github.com/gollem-dev/tools/intune` | Query Microsoft Intune managed devices via the Microsoft Graph API. |
| `github.com/gollem-dev/tools/github` | GitHub App tools: code search, issue search, file content, commit history, and blame. |
| `github.com/gollem-dev/tools/bigquery` | Run Google BigQuery queries and inspect datasets, table schemas, and SQL runbooks. |
| `github.com/gollem-dev/tools/webfetch` | Fetch web pages and extract their text, with optional LLM-based indirect prompt-injection analysis. |

## Usage

Every tool is constructed with `New` and the functional-option pattern, and returns
a concrete `*ToolSet` that satisfies `gollem.ToolSet`:

```go
import (
	"context"

	"github.com/gollem-dev/gollem"
	"github.com/gollem-dev/tools/otx"
)

func setup(ctx context.Context) (gollem.ToolSet, error) {
	ts, err := otx.New(otx.WithAPIKey("..."))
	if err != nil {
		return nil, err
	}

	// Optional: verify connectivity / credentials before use.
	if err := ts.Ping(ctx); err != nil {
		return nil, err
	}
	return ts, nil
}
```

Common conventions across every tool:

- `New(opts ...Option) (*ToolSet, error)` only constructs the value and performs
  local, in-memory validation. It never performs network I/O.
- `Ping(ctx) error` verifies connectivity and credentials against the backend.
  It is optional; call it for a preflight check.
- `WithLogger(*slog.Logger)` is available on every tool (defaults to
  `slog.Default()`). HTTP-based tools also accept `WithBaseURL` and
  `WithHTTPClient`.

## Testing

Each tool has mock-based unit tests (always run) and a live-service test that hits
the real backend. Live tests run only when their `TEST_*` environment variables are
set; otherwise they are skipped. See [`.env.example.hcl`](./.env.example.hcl) for the
full list of variables and run them with [zenv](https://github.com/m-mizutani/zenv).

Run the whole suite across every module with [Task](https://taskfile.dev):

```sh
task test          # go test in every module (mock tests only)
task check         # vet + lint + test in every module
zenv task test     # include live-service tests (loads TEST_* from .env.hcl)
```

A single module: `go -C otx test ./...` (or `zenv go -C otx test ./...` for live).

## Development

This is a multi-module workspace (`go.work`); `go test ./...` from the repo root does
not work because the root is not a module. Use `task` (see above) to run across all
modules, or `go -C <module> test ./...` for one. Shared helpers live in the `internal`
module.
