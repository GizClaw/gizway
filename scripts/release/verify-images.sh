#!/usr/bin/env bash
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
catalog="$root/release/images.json"
output_dir="${RELEASE_OUTPUT_DIR:-$root/tmp/release/images}"
oras="${ORAS:-oras}"
[[ -f "$output_dir/metadata.env" ]] || { printf 'missing %s/metadata.env\n' "$output_dir" >&2; exit 1; }
version=''; revision=''; build_time=''
# shellcheck disable=SC1090,SC1091
source "$output_dir/metadata.env"
while IFS=$'\t' read -r key image expected_command; do
  layout="$output_dir/$key.oci.tar"
  ref="$layout:$version"
  descriptor="$($oras manifest fetch --descriptor --oci-layout "$ref")"
  digest="$(jq -er '.digest | select(test("^sha256:[0-9a-f]{64}$"))' <<<"$descriptor")"
  config="$($oras manifest fetch-config --oci-layout "$ref")"
  jq -e --arg version "$version" --arg revision "$revision" --arg build_time "$build_time" \
    '.os == "linux" and .architecture == "amd64" and .created == $build_time
     and .config.Labels["org.opencontainers.image.source"] == "https://github.com/idy/gizway"
     and .config.Labels["org.opencontainers.image.version"] == $version
     and .config.Labels["org.opencontainers.image.revision"] == $revision
     and .config.Labels["org.opencontainers.image.created"] == $build_time' <<<"$config" >/dev/null
  jq -e --arg command "$expected_command" '((.config.Entrypoint // []) + (.config.Cmd // [])) | join(" ") | contains($command)' <<<"$config" >/dev/null
  printf '%s\n' "$digest" >"$output_dir/$key.digest"
  printf 'verified %s@%s\n' "$image" "$digest"
done < <(jq -r '.images[] | [.key,.image,.command] | @tsv' "$catalog")
