#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
catalog="$root/release/images.json"
version="${RELEASE_VERSION:-${1:-}}"
output_dir="${RELEASE_OUTPUT_DIR:-$root/tmp/release/images}"
build_command=(docker buildx build)
[[ -n "${BUILDX_BUILDER:-}" ]] && build_command+=(--builder "$BUILDX_BUILDER")
[[ -n "$version" ]] || { printf 'RELEASE_VERSION or a tag argument is required\n' >&2; exit 2; }
"$root/scripts/release/validate-tag.sh" "$version" --syntax-only
[[ "$(jq '.images | length' "$catalog")" == 7 ]] || { printf 'release catalog must contain exactly 7 images\n' >&2; exit 2; }

revision="${RELEASE_REVISION:-$(git rev-parse HEAD)}"
[[ "$revision" =~ ^[0-9a-f]{40}$ ]] || { printf 'RELEASE_REVISION must be a full lowercase commit SHA\n' >&2; exit 2; }
head_revision="$(git rev-parse HEAD)"
[[ "$revision" == "$head_revision" ]] || { printf 'RELEASE_REVISION %s does not match checked-out HEAD %s\n' "$revision" "$head_revision" >&2; exit 2; }
source_date_epoch="$(git show -s --format=%ct "$revision")"
[[ -z "${SOURCE_DATE_EPOCH:-}" || "$SOURCE_DATE_EPOCH" == "$source_date_epoch" ]] || { printf 'SOURCE_DATE_EPOCH does not match the release commit timestamp\n' >&2; exit 2; }
build_time="$(perl -MPOSIX=strftime -e 'print strftime("%Y-%m-%dT%H:%M:%SZ", gmtime(shift))' "$source_date_epoch")"
[[ -z "${RELEASE_BUILD_TIME:-}" || "$RELEASE_BUILD_TIME" == "$build_time" ]] || { printf 'RELEASE_BUILD_TIME does not match the release commit timestamp\n' >&2; exit 2; }
mkdir -p "$output_dir"

while IFS=$'\t' read -r key image dockerfile; do
  layout="$output_dir/$key.oci.tar"
  rm -f "$layout" "$output_dir/$key.digest"
  SOURCE_DATE_EPOCH="$source_date_epoch" "${build_command[@]}" \
    --platform "$(jq -r .platform "$catalog")" --file "$root/$dockerfile" --tag "$image:$version" \
    --build-arg "RELEASE_VERSION=$version" --build-arg "RELEASE_REVISION=$revision" \
    --build-arg "RELEASE_BUILD_TIME=$build_time" --build-arg "SOURCE_DATE_EPOCH=$source_date_epoch" \
    --provenance=false --sbom=false --output "type=oci,dest=$layout,rewrite-timestamp=true" "$root"
done < <(jq -r '.images[] | [.key,.image,.dockerfile] | @tsv' "$catalog")

printf 'version=%s\nrevision=%s\nbuild_time=%s\nsource_date_epoch=%s\n' "$version" "$revision" "$build_time" "$source_date_epoch" >"$output_dir/metadata.env"
