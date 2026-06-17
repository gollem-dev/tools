#!/usr/bin/env bash
#
# Scan every Go module in this multi-module repo and report which ones have
# real changes since their own last tag. Per the repo policy, versions are
# per-module and independent; a module is a tagging candidate ONLY when it has
# commits touching its directory since its highest existing tag.
#
# Output is machine-friendly blocks, one per candidate module:
#
#   MODULE\t<dir>
#   LAST\t<last tag or "(none)">
#   COUNT\t<number of commits since last tag>
#   COMMIT\t<sha>\t<subject>
#   ... (one COMMIT line per commit, newest first)
#   FILES\t<files changed under the module dir, comma-separated, truncated>
#   END
#
# Up-to-date modules (0 commits since last tag) are reported on a single
# UPTODATE line so the caller can summarize what was skipped.

set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

HEAD_SHA="$(git rev-parse --short HEAD)"
echo -e "HEAD\t${HEAD_SHA}\t$(git log -1 --format='%s' HEAD)"

# Discover module directories: any dir with a go.mod, excluding the .claude
# worktree copies and any nested vendor trees. The repo root has no go.mod.
modules="$(find . -name go.mod \
  -not -path '*/.claude/*' \
  -not -path '*/worktrees/*' \
  -not -path '*/vendor/*' \
  | sed 's|/go.mod$||; s|^\./||' | sort)"

for dir in $modules; do
  last="$(git tag --list "${dir}/v*" | sort -V | tail -1)"
  # Empty-tree hash is the diff base for an untagged module (compare against
  # "nothing" so every file in the module counts as added).
  empty_tree="$(git hash-object -t tree /dev/null)"
  if [ -z "$last" ]; then
    range="HEAD"
    base="$empty_tree"
    last_disp="(none)"
  else
    range="${last}..HEAD"
    base="$last"
    last_disp="$last"
  fi

  count="$(git log --oneline "$range" -- "$dir" | wc -l | tr -d ' ')"

  if [ "$count" = "0" ]; then
    echo -e "UPTODATE\t${dir}\t${last_disp}"
    continue
  fi

  echo -e "MODULE\t${dir}"
  echo -e "LAST\t${last_disp}"
  echo -e "COUNT\t${count}"
  git log "$range" --format='COMMIT%x09%h%x09%s' -- "$dir"
  files="$(git diff --name-only "${base}"..HEAD -- "$dir" 2>/dev/null | head -40 | paste -sd ',' - || true)"
  echo -e "FILES\t${files}"
  echo "END"
done
