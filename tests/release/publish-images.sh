#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
fixture="$root/tests/release/fixtures/mock-oras.sh"
output_dir="$(mktemp -d)"
cleanup() { rm -rf "$output_dir"; }
trap cleanup EXIT

digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
printf 'version=v1.2.3\nrevision=0123456789abcdef0123456789abcdef01234567\nbuild_time=2026-08-18T00:00:00Z\nsource_date_epoch=1787011200\n' >"$output_dir/metadata.env"
for key in gizpay gizway web entry zitadel zitadel-login powersync; do
  printf '%s\n' "$digest" >"$output_dir/$key.digest"
  : >"$output_dir/$key.oci.tar"
done

run_publish() {
  MOCK_ORAS_MODE="$1" MOCK_ORAS_DIGEST="$digest" MOCK_ORAS_LOG="$output_dir/oras.log" \
    MOCK_ORAS_STATE="$output_dir/oras.state" \
    RELEASE_OUTPUT_DIR="$output_dir" ORAS="$fixture" "$root/scripts/release/publish-images.sh"
}

: >"$output_dir/oras.log"
rm -f "$output_dir/oras.state"
run_publish publish >/dev/null
[[ "$(wc -l <"$output_dir/oras.log" | tr -d ' ')" == 7 ]]

: >"$output_dir/oras.log"
run_publish same >/dev/null
[[ ! -s "$output_dir/oras.log" ]]

for mode in conflict network; do
  : >"$output_dir/oras.log"
  if run_publish "$mode" >/dev/null 2>&1; then
    printf 'publish unexpectedly accepted %s remote state\n' "$mode" >&2
    exit 1
  fi
  [[ ! -s "$output_dir/oras.log" ]]
done
printf 'validated immutable publish, idempotency, conflict, and fail-closed lookup behavior\n'
