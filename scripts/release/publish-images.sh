#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
output_dir="${RELEASE_OUTPUT_DIR:-$root/tmp/release/images}"
oras="${ORAS:-oras}"
version=''; revision=''
# shellcheck disable=SC1090,SC1091
source "$output_dir/metadata.env"

remote_digest() {
  local ref="$1" output
  if output="$("$oras" manifest fetch --descriptor "$ref" 2>&1)"; then
    jq -er '.digest | select(test("^sha256:[0-9a-f]{64}$"))' <<<"$output"
    return
  fi
  if grep -Eqi 'not found|404|manifest unknown|MANIFEST_UNKNOWN' <<<"$output"; then
    return 0
  fi
  printf 'failed to resolve remote reference %s: %s\n' "$ref" "$output" >&2
  return 1
}

publish_one() {
  local key="$1" image="$2" layout candidate existing_version existing_revision
  layout="$output_dir/$key.oci.tar"
  candidate="$(<"$output_dir/$key.digest")"
  existing_version="$(remote_digest "$image:$version")"
  existing_revision="$(remote_digest "$image:sha-$revision")"
  for resolved in "$existing_version" "$existing_revision"; do
    if [[ -n "$resolved" && "$resolved" != "$candidate" ]]; then
      printf 'refusing to overwrite %s: remote digest %s differs from candidate %s\n' "$image" "$resolved" "$candidate" >&2
      exit 1
    fi
  done
  if [[ "$existing_version" == "$candidate" && "$existing_revision" == "$candidate" ]]; then
    printf '%s already publishes %s at both immutable tags\n' "$image" "$candidate"
    return
  fi
  "$oras" cp --from-oci-layout --no-tty "$layout:$version" "$image:$version,sha-$revision"
  [[ "$(remote_digest "$image:$version")" == "$candidate" ]]
  [[ "$(remote_digest "$image:sha-$revision")" == "$candidate" ]]
}

publish_one gizpay ghcr.io/idy/gizway-gizpay
publish_one gizway ghcr.io/idy/gizway-gateway
publish_one web ghcr.io/idy/gizway-web
