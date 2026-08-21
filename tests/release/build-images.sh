#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
temporary="$(mktemp -d)"
cleanup() { rm -rf "$temporary"; }
trap cleanup EXIT

mkdir -p "$temporary/bin" "$temporary/output"
# The single-quoted lines intentionally defer expansion to the generated mock.
# shellcheck disable=SC2016
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -euo pipefail' \
  '[[ "${1:-}" == buildx && "${2:-}" == build ]]' \
  'for ((index = 1; index <= $#; index++)); do' \
  '  if [[ "${!index}" == --output ]]; then' \
  '    next=$((index + 1))' \
  '    printf "%s\\n" "${!next}" >>"${MOCK_DOCKER_LOG:?}"' \
  '    exit 0' \
  '  fi' \
  'done' \
  'exit 2' >"$temporary/bin/docker"
chmod +x "$temporary/bin/docker"

MOCK_DOCKER_LOG="$temporary/docker.log" \
PATH="$temporary/bin:$PATH" \
RELEASE_OUTPUT_DIR="$temporary/output" \
  "$root/scripts/release/build-images.sh" v1.2.3 >/dev/null

[[ "$(wc -l <"$temporary/docker.log" | tr -d ' ')" == 7 ]]
for key in gizpay gizway web entry zitadel zitadel-login powersync; do
  grep -Fx "type=oci,dest=$temporary/output/$key.oci.tar,rewrite-timestamp=true" \
    "$temporary/docker.log" >/dev/null
done
printf 'validated reproducible OCI timestamp rewriting for all release images\n'
