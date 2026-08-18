#!/usr/bin/env bash
set -euo pipefail

tag="${1:-${GITHUB_REF_NAME:-}}"
mode="${2:-}"
semver='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*)|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.((0|[1-9][0-9]*)|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$'

if [[ ! "$tag" =~ $semver ]]; then
  printf 'invalid release tag %q: expected strict SemVer vMAJOR.MINOR.PATCH with optional prerelease and no build metadata\n' "$tag" >&2
  exit 1
fi

if [[ "$mode" == "--syntax-only" ]]; then
  exit 0
fi

revision="$(git rev-parse --verify "${tag}^{commit}")"
if [[ ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
  printf 'tag %s did not resolve to a full commit SHA\n' "$tag" >&2
  exit 1
fi

if [[ "$mode" == "--require-main" ]]; then
  git rev-parse --verify 'origin/main^{commit}' >/dev/null
  if ! git merge-base --is-ancestor "$revision" origin/main; then
    printf 'tag %s resolves to %s, which is not an ancestor of origin/main\n' "$tag" "$revision" >&2
    exit 1
  fi
elif [[ -n "$mode" ]]; then
  printf 'unknown option: %s\n' "$mode" >&2
  exit 2
fi

source_date_epoch="$(git show -s --format=%ct "$revision")"
build_time="$(perl -MPOSIX=strftime -e 'print strftime("%Y-%m-%dT%H:%M:%SZ", gmtime(shift))' "$source_date_epoch")"
prerelease=false
[[ "$tag" == *-* ]] && prerelease=true

printf 'version=%s\nrevision=%s\nbuild_time=%s\nsource_date_epoch=%s\nprerelease=%s\n' \
  "$tag" "$revision" "$build_time" "$source_date_epoch" "$prerelease"
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  printf 'version=%s\nrevision=%s\nbuild_time=%s\nsource_date_epoch=%s\nprerelease=%s\n' \
    "$tag" "$revision" "$build_time" "$source_date_epoch" "$prerelease" >>"$GITHUB_OUTPUT"
fi
