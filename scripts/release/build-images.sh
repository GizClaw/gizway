#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
version="${RELEASE_VERSION:-${1:-}}"
output_dir="${RELEASE_OUTPUT_DIR:-$root/tmp/release/images}"
builder_args=()
[[ -n "${BUILDX_BUILDER:-}" ]] && builder_args=(--builder "$BUILDX_BUILDER")
if [[ -z "$version" ]]; then
  printf 'RELEASE_VERSION or a tag argument is required\n' >&2
  exit 2
fi
"$root/scripts/release/validate-tag.sh" "$version" --syntax-only

revision="${RELEASE_REVISION:-$(git rev-parse HEAD)}"
if [[ ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
  printf 'RELEASE_REVISION must be a full lowercase commit SHA\n' >&2
  exit 2
fi
head_revision="$(git rev-parse HEAD)"
if [[ "$revision" != "$head_revision" ]]; then
  printf 'RELEASE_REVISION %s does not match checked-out HEAD %s\n' "$revision" "$head_revision" >&2
  exit 2
fi
source_date_epoch="$(git show -s --format=%ct "$revision")"
if [[ -n "${SOURCE_DATE_EPOCH:-}" && "$SOURCE_DATE_EPOCH" != "$source_date_epoch" ]]; then
  printf 'SOURCE_DATE_EPOCH does not match the release commit timestamp\n' >&2
  exit 2
fi
build_time="$(perl -MPOSIX=strftime -e 'print strftime("%Y-%m-%dT%H:%M:%SZ", gmtime(shift))' "$source_date_epoch")"
if [[ -n "${RELEASE_BUILD_TIME:-}" && "$RELEASE_BUILD_TIME" != "$build_time" ]]; then
  printf 'RELEASE_BUILD_TIME does not match the release commit timestamp\n' >&2
  exit 2
fi
mkdir -p "$output_dir"

build_one() {
  local key="$1" image="$2" dockerfile="$3" layout
  layout="$output_dir/$key.oci.tar"
  rm -f "$layout" "$output_dir/$key.digest"
  SOURCE_DATE_EPOCH="$source_date_epoch" docker buildx build "${builder_args[@]}" \
    --platform linux/amd64 \
    --file "$root/$dockerfile" \
    --tag "$image:$version" \
    --build-arg "RELEASE_VERSION=$version" \
    --build-arg "RELEASE_REVISION=$revision" \
    --build-arg "RELEASE_BUILD_TIME=$build_time" \
    --build-arg "SOURCE_DATE_EPOCH=$source_date_epoch" \
    --provenance=false --sbom=false \
    --output "type=oci,dest=$layout" \
    "$root"
  printf '%s\n' "$layout"
}

build_one gizpay ghcr.io/idy/gizway-gizpay docker/gizpay/Dockerfile
build_one gizway ghcr.io/idy/gizway-gateway docker/gizway/Dockerfile
build_one web ghcr.io/idy/gizway-web docker/gizway-web/Dockerfile

printf 'version=%s\nrevision=%s\nbuild_time=%s\nsource_date_epoch=%s\n' \
  "$version" "$revision" "$build_time" "$source_date_epoch" >"$output_dir/metadata.env"
