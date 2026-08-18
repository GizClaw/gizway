#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
valid=(v0.0.0 v1.2.3 v1.2.3-alpha v1.2.3-alpha.1 v1.2.3-0.3.7 v1.2.3-x-y-z)
invalid=(1.2.3 v1.2 v1.2.3.4 v01.2.3 v1.02.3 v1.2.03 v1.2.3+build v1.2.3-01 v1.2.3- v1.2.3-alpha..1)

for tag in "${valid[@]}"; do
  "$root/scripts/release/validate-tag.sh" "$tag" --syntax-only
done
for tag in "${invalid[@]}"; do
  if "$root/scripts/release/validate-tag.sh" "$tag" --syntax-only >/dev/null 2>&1; then
    printf 'invalid tag unexpectedly accepted: %s\n' "$tag" >&2
    exit 1
  fi
done
printf 'validated %d accepted and %d rejected release tags\n' "${#valid[@]}" "${#invalid[@]}"

repository="$(mktemp -d)"
cleanup() { rm -rf "$repository"; }
trap cleanup EXIT
git -C "$repository" init --quiet --initial-branch=main
git -C "$repository" config user.name release-test
git -C "$repository" config user.email release-test@example.test
printf 'main\n' >"$repository/file"
git -C "$repository" add file
git -C "$repository" commit --quiet --message main
git -C "$repository" tag --annotate v1.0.0 --message v1.0.0
git -C "$repository" update-ref refs/remotes/origin/main HEAD
(cd "$repository" && "$root/scripts/release/validate-tag.sh" v1.0.0 --require-main >/dev/null)
git -C "$repository" switch --quiet --create side
printf 'side\n' >>"$repository/file"
git -C "$repository" commit --quiet --all --message side
git -C "$repository" tag v1.0.1
if (cd "$repository" && "$root/scripts/release/validate-tag.sh" v1.0.1 --require-main >/dev/null 2>&1); then
  printf 'non-main tag unexpectedly accepted\n' >&2
  exit 1
fi
printf 'validated annotated tag resolution and main ancestry rejection\n'

if RELEASE_REVISION=0000000000000000000000000000000000000000 \
  "$root/scripts/release/build-images.sh" v1.0.0 >/dev/null 2>&1; then
  printf 'mismatched release revision unexpectedly accepted\n' >&2
  exit 1
fi
if SOURCE_DATE_EPOCH=1 "$root/scripts/release/build-images.sh" v1.0.0 >/dev/null 2>&1; then
  printf 'mismatched source date epoch unexpectedly accepted\n' >&2
  exit 1
fi
if RELEASE_BUILD_TIME=1970-01-01T00:00:01Z \
  "$root/scripts/release/build-images.sh" v1.0.0 >/dev/null 2>&1; then
  printf 'mismatched release build time unexpectedly accepted\n' >&2
  exit 1
fi
printf 'validated release metadata mismatch rejection\n'
