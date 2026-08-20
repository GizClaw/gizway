#!/usr/bin/env bash
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
catalog="$root/release/images.json"
output_dir="${RELEASE_OUTPUT_DIR:-$root/tmp/release/images}"
oras="${ORAS:-oras}"
version=''; revision=''
# shellcheck disable=SC1090,SC1091
source "$output_dir/metadata.env"
remote_digest() {
  local ref="$1" output
  if output="$("$oras" manifest fetch --descriptor "$ref" 2>&1)"; then jq -er '.digest | select(test("^sha256:[0-9a-f]{64}$"))' <<<"$output"; return; fi
  grep -Eqi 'not found|404|manifest unknown|MANIFEST_UNKNOWN' <<<"$output" && return 0
  printf 'failed to resolve remote reference %s: %s\n' "$ref" "$output" >&2; return 1
}
while IFS=$'\t' read -r key image; do
  layout="$output_dir/$key.oci.tar"; candidate="$(<"$output_dir/$key.digest")"
  existing_version="$(remote_digest "$image:$version")"; existing_revision="$(remote_digest "$image:sha-$revision")"
  for resolved in "$existing_version" "$existing_revision"; do
    [[ -z "$resolved" || "$resolved" == "$candidate" ]] || { printf 'refusing to overwrite %s: remote digest %s differs from candidate %s\n' "$image" "$resolved" "$candidate" >&2; exit 1; }
  done
  if [[ "$existing_version" == "$candidate" && "$existing_revision" == "$candidate" ]]; then printf '%s already publishes %s at both immutable tags\n' "$image" "$candidate"; continue; fi
  "$oras" cp --from-oci-layout --no-tty "$layout:$version" "$image:$version,sha-$revision"
  [[ "$(remote_digest "$image:$version")" == "$candidate" && "$(remote_digest "$image:sha-$revision")" == "$candidate" ]]
done < <(jq -r '.images[] | [.key,.image] | @tsv' "$catalog")
