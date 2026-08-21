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
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
while IFS=$'\t' read -r key image expected_command expected_base; do
  layout="$output_dir/$key.oci.tar"
  ref="$layout:$version"
  descriptor="$($oras manifest fetch --descriptor --oci-layout "$ref")"
  digest="$(jq -er '.digest | select(test("^sha256:[0-9a-f]{64}$"))' <<<"$descriptor")"
  config="$($oras manifest fetch-config --oci-layout "$ref")"
  jq -e --arg version "$version" --arg revision "$revision" --arg build_time "$build_time" --arg base "$expected_base" \
    '.os == "linux" and .architecture == "amd64" and .created == $build_time
     and (.config.User | test("^[1-9][0-9]*(:[0-9]+)?$"))
     and .config.Labels["org.opencontainers.image.source"] == "https://github.com/idy/gizway"
     and .config.Labels["org.opencontainers.image.version"] == $version
     and .config.Labels["org.opencontainers.image.revision"] == $revision
     and .config.Labels["org.opencontainers.image.created"] == $build_time
     and .config.Labels["org.opencontainers.image.base.name"] == $base
     and ((.config | tostring) | test("giz_sk_|BEGIN (RSA |EC )?PRIVATE KEY|subscription-key-hmac|action-signing-key"; "i") | not)' <<<"$config" >/dev/null
  jq -e --arg command "$expected_command" '((.config.Entrypoint // []) + (.config.Cmd // [])) | join(" ") | contains($command)' <<<"$config" >/dev/null
  manifest="$($oras manifest fetch --oci-layout "$ref")"
  jq -r '.layers[].digest' <<<"$manifest" | while read -r layer_digest; do
    layer="$temporary/${key}-${layer_digest#sha256:}.tar.gz"
    "$oras" blob fetch --oci-layout --output "$layer" "$layout@$layer_digest"
    while IFS= read -r forbidden_path; do
      [[ "$forbidden_path" == */ ]] && continue
      if tar -xOzf "$layer" "$forbidden_path" | grep -q .; then
        echo "$key image layer contains non-empty forbidden path $forbidden_path" >&2
        exit 1
      fi
    done < <(tar -tzf "$layer" | grep -Ei '(^|/)(secrets?/|\.env$|\.env\.[^/]+$|id_(rsa|dsa|ecdsa|ed25519)$|tls\.key$|private[^/]*\.key$|credentials?\.json$|service[-_]?account[^/]*\.json$)' || true)
  done
  printf '%s\n' "$digest" >"$output_dir/$key.digest"
  printf 'verified %s@%s\n' "$image" "$digest"
done < <(jq -r '.images[] | [.key,.image,.command,.base] | @tsv' "$catalog")
