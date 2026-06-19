---
name: tag-modules
description: For this multi-module Go repo, compare HEAD against existing tags to find modules that have changes since their own last tag but are not yet tagged. Propose SemVer version candidates with reasons, and create/push `<module>/vX.Y.Z` tags ONLY after the user approves. Use for release work such as "tag the modules", "cut a release", or "check for untagged modules".
---

# Tag Modules (per-module SemVer release)

This repository is a **multi-module workspace**: every tool directory (`otx/`,
`bigquery/`, ...) is its own independent Go module. Versions are **per-module
and fully independent**; there is no repo-wide `vX.Y.Z` tag. This skill detects
modules that have real changes since their own last tag but are not yet tagged,
proposes SemVer candidates, and applies tags **only after approval**.

The full versioning policy lives in `CLAUDE.md` under "Versioning & tags". This
skill exists to execute that policy mechanically.

## Hard rules (never violate)

- **Never create or push a tag without approval.** Always present candidates and
  reasons in Step 3 and obtain explicit user approval before Step 4.
- **Never bump a module that did not change.** Empty "version-alignment" tags
  (the "boy who cried wolf" tag) are prohibited.
- **Tag format is always `<module>/vX.Y.Z`.** Never create a bare repo-root
  `vX.Y.Z` tag (it is meaningless to Go's module resolution).
- Decide a version against **that module's own last tag**. It is unrelated to
  the numbers of neighbouring modules.

## Step 1: Detect (scan)

From the repo root, run the bundled script to get each module's status.

```bash
bash .claude/skills/tag-modules/scripts/scan.sh
```

How to read the output:

- `HEAD\t<sha>\t<subject>` — the current HEAD.
- `UPTODATE\t<dir>\t<last tag>` — no changes since the last tag. **Out of scope.**
- A `MODULE` block (`MODULE`/`LAST`/`COUNT`/`COMMIT...`/`FILES`/`END`) — a module
  with changes since its last tag. This is a **tagging candidate**.
  - If `LAST` is `(none)`, the module has never been tagged (initial-tag
    candidate).

If there are no candidates, report that everything is tagged up to HEAD and
stop. Do not tag anything.

## Step 2: Decide the SemVer version

For each candidate module, determine the bump level from the `COMMIT` subjects
(Semantic Commit types), the `FILES`, and, if needed, the contents of
`git log -p <last>..HEAD -- <dir>`. Bump from **that module's own last tag**.

- **major** (`X+1.0.0`): a backward-incompatible API change. Removal/signature
  change of an exported symbol (`New`/`Option`/`ToolSet` fields or methods, the
  `Specs` input/output contract, ...) or a breaking behavior change. Also major
  if a commit carries `!` or `BREAKING CHANGE`.
- **minor** (`X.Y+1.0`): a backward-compatible feature addition. Mostly `feat:`
  — a new Option, a new tool, a new field — that does not break existing users.
- **patch** (`X.Y.Z+1`): bug fixes, internal refactors, docs, tests, or CI only
  — nothing that affects the API. Mostly `fix:`/`refactor:`/`docs:`/`test:`/
  `chore:`/`ci:`.
- **initial tag** (`LAST` is `(none)`): follow the convention of the other
  modules and propose `v0.1.0` by default (adjust with a stated reason only if
  there is a special circumstance).

When a change is ambiguous (it looks like it might touch the public API), err on
the safe side and pick the higher level, and state that reasoning in the
candidate proposal. When multiple types are mixed, take the most impactful one
(e.g. `feat` + `fix` → minor).

> Note: under v0.x.y, SemVer technically allows breaking changes below a minor
> bump, but this repo bumps the minor even on v0.x for breaking changes
> (`0.2.0` → `0.3.0`) to protect consumers. When in doubt, confirm with the user
> with a stated reason.

## Step 3: Present candidates and reasons (await approval)

**Before** tagging anything, present the candidates to the user as a table. Each
row should include:

- module name
- current tag (or `(none)`)
- proposed new version
- bump level (major/minor/patch/initial)
- **reason** (quote the commit subjects that drove the decision, e.g.
  "`feat: add WithRetry option` — backward-compatible feature addition → minor")

Example presentation:

| module | current | → new | level | reason |
|--------|---------|-------|-------|--------|
| otx | otx/v0.1.0 | otx/v0.2.0 | minor | `feat: add pulse search` |
| whois | whois/v0.1.0 | whois/v0.1.1 | patch | `fix: handle empty response` |

After presenting, clearly ask whether to apply these tags. If the user wants to
change a version or the target set, reflect it and present again. **Do not
proceed to Step 4 until approval is given.**

## Step 4: Create and push tags (only after approval)

For each approved module, settle on the commit to tag. The default is HEAD, but
tagging the module's latest relevant commit is more precise (the top `COMMIT`
sha from `scan.sh`, usually identical to HEAD).

Create all approved tags, then push:

```bash
# example (only the approved ones)
git tag otx/v0.2.0 <commit>
git tag whois/v0.1.1 <commit>

# push ONLY the newly created tags (leave existing tags untouched)
git push origin otx/v0.2.0 whois/v0.1.1
```

Notes:

- Do not bulk-push every tag with `git push --tags`. Push **only the tags you
  created**, explicitly.
- After tagging, report the list of created/pushed tags and finish.
- Do not create an aggregate GitHub Release (per policy).

## Additional notes

- If HEAD contains commits not yet pushed, the push will fail because the
  commit a tag points to is missing on the remote. If needed, confirm with the
  user about pushing the branch/commit first.
- `scan.sh` auto-discovers directories that contain a `go.mod`, so new tool
  modules become candidates with no configuration change.
