#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
old_owner=idy

required=(LICENSE README.md SECURITY.md CONTRIBUTING.md THIRD_PARTY_NOTICES.md)
for path in "${required[@]}"; do
  [[ -s "$root/$path" ]] || {
    printf 'required open-source file is missing or empty: %s\n' "$path" >&2
    exit 1
  }
done

old_pattern="github\\.com/${old_owner}/gizway|ghcr\\.io/${old_owner}/gizway|@${old_owner}/gizway-browser-sdk"
if git -C "$root" grep -nE "$old_pattern" -- . \
  ':(exclude)scripts/test-unit/test-unit-repository-contracts.sh'; then
  printf 'tracked files still reference the retired personal namespace\n' >&2
  exit 1
fi

jq -e '
  .platform == "linux/amd64" and
  (.images | length == 6) and
  (all(.images[]; .image | startswith("ghcr.io/gizclaw/gizway-"))) and
  ([.images[].instances[]] | length == 11)
' "$root/release/images.json" >/dev/null

jq -e '
  .name == "@gizclaw/gizway-browser-sdk" and
  .license == "BSD-3-Clause" and
  .repository.url == "git+https://github.com/GizClaw/gizway.git" and
  .publishConfig.registry == "https://npm.pkg.github.com"
' "$root/sdk/web/package.json" >/dev/null

release_workflow="$root/.github/workflows/release.yml"
review_workflow="$root/.github/workflows/codex-review.yml"
grep -Fq 'https://github.com/GizClaw/gizway/.github/workflows/release.yml@refs/tags/' "$release_workflow"
grep -Fq 'github.event.comment.author_association' "$review_workflow"
grep -Fq "github.event.comment.body == '@codex'" "$review_workflow"
grep -Fq 'github.event.pull_request.head.repo.full_name == github.repository' "$review_workflow"
grep -Fq 'github.event.issue.author_association' "$review_workflow"

printf 'validated open-source files, organization namespaces, and trusted review gates\n'
