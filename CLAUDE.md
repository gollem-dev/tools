# CLAUDE.md

Project-specific guidance for `github.com/gollem-dev/tools`.

## Overview

This repository provides ready-to-use tool integrations for the
[gollem](https://github.com/gollem-dev/gollem) LLM agent framework. Each tool is
an independent package that implements the `gollem.ToolSet` interface.

- The gollem dependency is **`github.com/gollem-dev/gollem`** (NOT
  `github.com/m-mizutani/gollem`; the module moved to the `gollem-dev` org as of
  v0.26.0).
- Tools are ported from `secmon-lab/warren/pkg/tool`, but only the `Specs`/`Run`
  logic is carried over. The warren-specific methods (`Helper/ID/Description/
  Flags/Configure/LogValue/Prompt`) are intentionally dropped.

## Repository layout (multi-module)

This repo is a **multi-module workspace**: every tool directory is its own Go
module so that consumers only pull the dependencies of the tool they import
(e.g. importing `otx` must not drag in BigQuery's cloud SDKs).

- Each tool dir (`otx/`, `bigquery/`, ...) has its own `go.mod` with path
  `github.com/gollem-dev/tools/<tool>`.
- Shared helpers live in the `internal/` module
  (`github.com/gollem-dev/tools/internal`). Because Go's `internal/` visibility
  is enforced lexically on the import path (parent = `github.com/gollem-dev/tools`),
  every tool module can import `github.com/gollem-dev/tools/internal/...`, while
  external consumers cannot. This keeps the helper shared but private.
- `go.work` at the repo root ties all modules together for local development.
  Because the root is not a module, `go test ./...` does not work there. Use the
  `Taskfile.yml` to run across every module — `task test` / `task check` / `task
  lint` / `task tidy` (each discovers all `go.mod` dirs and runs `GOWORK=off` per
  module, matching CI) — or `go -C <module> test ./...` for a single one. Append
  live-service runs with zenv: `zenv task test`.
- Each tool module requires `github.com/gollem-dev/tools/internal` with a
  dev-time `replace => ../internal`. **Before publishing**, tag `internal/vX`,
  point the requires at the real version, and drop the replace directives.
- There is no module at the repo root; the root only holds docs and `go.work`.

## Tool package conventions

Every tool package follows the same shape:

- Export a concrete struct named `ToolSet` whose fields are **all unexported**.
  It implements `gollem.ToolSet` (`Specs`, `Run`) plus `Ping`.
  - Return the concrete `*ToolSet` from `New`, never an interface
    ("accept interfaces, return structs").
- `New(opts ...Option) (*ToolSet, error)`:
  - Responsible **only** for constructing the struct and local, in-memory
    validation (required field presence, parsing values like `"10GB"`, loading
    local config files).
  - Must NOT take `ctx`, perform network I/O, or create remote clients.
  - Returns an error only for static misconfiguration.
- `Ping(ctx context.Context) error`: performs connectivity / credential
  verification against the backend. This is where the network parts of warren's
  old `Configure` live. Callers may skip it.
- Configuration is via the functional option pattern (`Option func(*ToolSet)`).
  - **All tools must provide `WithLogger(*slog.Logger)`** (default
    `slog.Default()`; a nil argument keeps the default).
  - HTTP-based tools provide `WithBaseURL(string)` and
    `WithHTTPClient(*http.Client)` for testing and customization.
- Backend clients (BigQuery, GCS, GitHub, ...) are created inside `Run`/`Ping`
  using their `ctx`, not stored from construction time.

## File naming

- The base file of every package is `toolset.go` (uniform across all packages).
  `New`/`Option`/`Ping`/`Specs`/`Run` live there.
- Single-file tools are just `toolset.go`. Multi-file tools (github, bigquery,
  webfetch) keep the base in `toolset.go` and split sub-features into
  feature-named files (`search.go`, `run.go`, ...).
- Tests: `toolset_test.go` for the base, `xyz.go` -> `xyz_test.go` for the rest.
  Use external test packages (`package otx_test`).

## Testing

Two layers, both mandatory:

1. **Mock tests (always run)** — `httptest` for HTTP tools; injected mock clients
   for github/bigquery. No external dependency; must pass in CI.
2. **Live-service tests (one per tool, conditional)** — hit the real external
   service. Credentials/targets come from `TEST_...` environment variables read
   with `os.LookupEnv`; if the variable is unset (`ok == false`), `t.Skip` at the
   top of the test. This env-var-presence check is the ONLY acceptable use of
   `t.Skip`.
   - Document every `TEST_...` variable with a sample in `.env.example.hcl`
     (zenv HCL format; mark secrets with `KEY { value = "..."; secret = true }`).

## CI (GitHub Actions)

Workflows live in `.github/workflows/` and mirror the gollem project's setup,
adapted for the multi-module layout:

- `test.yml`, `lint.yml`, `gosec.yml` each start with a `discover` job that finds
  every `go.mod` in the repo and feeds the directory list into a build matrix.
  **New tool modules are picked up automatically** — no workflow edits needed.
- Matrix jobs run with `GOWORK=off` so each module is built/tested/linted through
  its own `go.mod` (and `replace`), exactly as an external consumer would.
- `trivy.yml` runs a single filesystem scan (covers all `go.mod`/`go.sum`).
- `integrity.yml` runs `.github/scripts/check-invisible-chars.sh` (Trojan-source /
  invisible-Unicode guard).
- Lint config is the root `.golangci.yml`; golangci-lint discovers it from each
  module subdirectory.

## README maintenance

**The `README.md` MUST contain an up-to-date list of all tools with a one-line
description each.** Whenever a tool is added, removed, or its purpose changes,
update the tool list in `README.md` in the same change. Treat this as part of the
implementation, not an afterthought.
