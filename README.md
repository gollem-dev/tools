# Gollem Tools

Ready-to-use tool integrations for the [gollem](https://github.com/gollem-dev/gollem)
LLM agent framework. Each tool implements `gollem.ToolSet` and is published as its
own Go module, so you only pull the dependencies of the tools you actually use.

## Available tools

| Module | Description | Docs |
|--------|-------------|------|
| `github.com/gollem-dev/tools/otx` | AlienVault OTX threat-intelligence lookups (IPv4 / IPv6 / domain / hostname / file hash). | [![Go Reference](https://pkg.go.dev/badge/github.com/gollem-dev/tools/otx.svg)](https://pkg.go.dev/github.com/gollem-dev/tools/otx) |
| `github.com/gollem-dev/tools/vt` | VirusTotal indicator lookups (IP / domain / file hash / URL). | [![Go Reference](https://pkg.go.dev/badge/github.com/gollem-dev/tools/vt.svg)](https://pkg.go.dev/github.com/gollem-dev/tools/vt) |
| `github.com/gollem-dev/tools/abusech` | abuse.ch MalwareBazaar malware sample lookups. | [![Go Reference](https://pkg.go.dev/badge/github.com/gollem-dev/tools/abusech.svg)](https://pkg.go.dev/github.com/gollem-dev/tools/abusech) |
| `github.com/gollem-dev/tools/ipdb` | AbuseIPDB IP address reputation / abuse-confidence checks. | [![Go Reference](https://pkg.go.dev/badge/github.com/gollem-dev/tools/ipdb.svg)](https://pkg.go.dev/github.com/gollem-dev/tools/ipdb) |
| `github.com/gollem-dev/tools/shodan` | Shodan host, domain, and search lookups. | [![Go Reference](https://pkg.go.dev/badge/github.com/gollem-dev/tools/shodan.svg)](https://pkg.go.dev/github.com/gollem-dev/tools/shodan) |
| `github.com/gollem-dev/tools/whois` | WHOIS registration lookups for domain names and IP addresses. | [![Go Reference](https://pkg.go.dev/badge/github.com/gollem-dev/tools/whois.svg)](https://pkg.go.dev/github.com/gollem-dev/tools/whois) |
| `github.com/gollem-dev/tools/urlscan` | Submit URLs to urlscan.io and retrieve scan results. | [![Go Reference](https://pkg.go.dev/badge/github.com/gollem-dev/tools/urlscan.svg)](https://pkg.go.dev/github.com/gollem-dev/tools/urlscan) |
| `github.com/gollem-dev/tools/slack` | Search Slack messages and fetch messages with their thread context in bulk. | [![Go Reference](https://pkg.go.dev/badge/github.com/gollem-dev/tools/slack.svg)](https://pkg.go.dev/github.com/gollem-dev/tools/slack) |
| `github.com/gollem-dev/tools/intune` | Query Microsoft Intune managed devices via the Microsoft Graph API. | [![Go Reference](https://pkg.go.dev/badge/github.com/gollem-dev/tools/intune.svg)](https://pkg.go.dev/github.com/gollem-dev/tools/intune) |
| `github.com/gollem-dev/tools/github` | GitHub App tools: code/issue search, file content, commit history, blame, and single issue/PR retrieval. | [![Go Reference](https://pkg.go.dev/badge/github.com/gollem-dev/tools/github.svg)](https://pkg.go.dev/github.com/gollem-dev/tools/github) |
| `github.com/gollem-dev/tools/bigquery` | Run Google BigQuery queries and inspect datasets, table schemas, and SQL runbooks. | [![Go Reference](https://pkg.go.dev/badge/github.com/gollem-dev/tools/bigquery.svg)](https://pkg.go.dev/github.com/gollem-dev/tools/bigquery) |
| `github.com/gollem-dev/tools/webfetch` | Fetch web pages and extract their text (SSRF-guarded), with optional LLM-based indirect prompt-injection analysis. | [![Go Reference](https://pkg.go.dev/badge/github.com/gollem-dev/tools/webfetch.svg)](https://pkg.go.dev/github.com/gollem-dev/tools/webfetch) |
| `github.com/gollem-dev/tools/notion` | Search Notion pages/databases, read pages as Markdown, and query database rows. | [![Go Reference](https://pkg.go.dev/badge/github.com/gollem-dev/tools/notion.svg)](https://pkg.go.dev/github.com/gollem-dev/tools/notion) |
| `github.com/gollem-dev/tools/falcon` | Read-only CrowdStrike Falcon queries (incidents, alerts, behaviors, devices, CrowdScores, EDR events) with in-memory pagination to bound result size. | [![Go Reference](https://pkg.go.dev/badge/github.com/gollem-dev/tools/falcon.svg)](https://pkg.go.dev/github.com/gollem-dev/tools/falcon) |

## Usage

Every tool is constructed with `New` and the functional-option pattern, and returns
a concrete `*ToolSet` that satisfies `gollem.ToolSet`. Register it with a gollem
agent via `gollem.WithToolSets`, then `Execute`:

```go
import (
	"context"

	"github.com/gollem-dev/gollem"
	"github.com/gollem-dev/gollem/llm/claude"
	"github.com/gollem-dev/tools/otx"
)

func run(ctx context.Context) error {
	// Construct the tool. New only validates locally; it performs no network I/O.
	ts, err := otx.New("<OTX_API_KEY>")
	if err != nil {
		return err
	}

	// Optional: verify connectivity / credentials before use.
	if err := ts.Ping(ctx); err != nil {
		return err
	}

	// Wire the tool into a gollem agent backed by your LLM of choice.
	llm, err := claude.New(ctx, "<ANTHROPIC_API_KEY>")
	if err != nil {
		return err
	}
	agent := gollem.New(llm, gollem.WithToolSets(ts))

	// The agent can now call the otx_* tools while answering.
	_, err = agent.Execute(ctx, gollem.Text("Is 8.8.8.8 malicious?"))
	return err
}
```

Pass several tools at once with `gollem.WithToolSets(otxTS, vtTS, whoisTS)`.

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
modules, or `go -C <module> test ./...` for one. Each module is fully
self-contained — there is no shared helper module.
