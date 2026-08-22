#!/usr/bin/env bash
set -euo pipefail
root="$(git rev-parse --show-toplevel)"; catalog="$root/release/images.json"
output_dir="${RELEASE_OUTPUT_DIR:-$root/tmp/release/images}"; manifest="${RELEASE_MANIFEST:-$root/tmp/release/release-manifest.json}"
sdk_output_dir="${RELEASE_SDK_OUTPUT_DIR:-$root/tmp/release/sdk}"
version=''; revision=''; build_time=''
# shellcheck disable=SC1090,SC1091
source "$output_dir/metadata.env"
images='{}'
while IFS=$'\t' read -r key image base instances; do
  value="$(jq -n --arg ref "$image@$(<"$output_dir/$key.digest")" --arg base "$base" --argjson instances "$instances" '{ref:$ref,base:$base,instances:$instances}')"
  images="$(jq --arg key "$key" --argjson value "$value" '. + {($key):$value}' <<<"$images")"
done < <(jq -r '.images[] | [.key,.image,.base,(.instances|tojson)] | @tsv' "$catalog")
identity="https://github.com/idy/gizway/.github/workflows/release.yml@refs/tags/$version"
sdk_asset="gizway-web-sdk-$version.tgz"
sdk_checksum_file="$sdk_output_dir/$sdk_asset.sha256"
[[ -f "$sdk_output_dir/$sdk_asset" && -f "$sdk_checksum_file" ]] || { printf 'SDK release artifact and checksum are required\n' >&2; exit 2; }
read -r sdk_sha256 sdk_checksum_asset <"$sdk_checksum_file"
[[ "$sdk_checksum_asset" == "$sdk_asset" && "$sdk_sha256" =~ ^[0-9a-f]{64}$ ]] || { printf 'SDK checksum sidecar is invalid\n' >&2; exit 2; }
[[ "$(shasum -a 256 "$sdk_output_dir/$sdk_asset" | awk '{print $1}')" == "$sdk_sha256" ]] || { printf 'SDK checksum does not match artifact\n' >&2; exit 2; }
mkdir -p "$(dirname "$manifest")"
jq -nS --arg version "$version" --arg revision "$revision" --arg build_time "$build_time" --argjson images "$images" --arg identity "$identity" --arg sdk_asset "$sdk_asset" --arg sdk_sha256 "$sdk_sha256" \
  '{version:$version,revision:$revision,build_time:$build_time,platform:"linux/amd64",images:$images,sdk:{asset:$sdk_asset,sha256:$sdk_sha256},signing:{scheme:"cosign-keyless",oidc_issuer:"https://token.actions.githubusercontent.com",certificate_identity:$identity}}' >"$manifest"
jq -e '(.images | length == 6) and ([.images[].instances[]] | length == 11) and (.sdk.sha256 | test("^[0-9a-f]{64}$"))' "$manifest" >/dev/null
printf '%s\n' "$manifest"
