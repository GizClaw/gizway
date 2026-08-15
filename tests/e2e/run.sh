#!/bin/sh
set -u

root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
results="$(mktemp "${TMPDIR:-/tmp}/gizway-m03-e2e.XXXXXX")"
cleanup() {
    status=$?
    rm -f "${results}" || true
    return "${status}"
}
trap cleanup EXIT INT TERM

run_case() {
    name="$1"
    shift
    if "$@"; then
        printf '%s\tPASS\n' "${name}" >>"${results}"
    else
        status=$?
        printf '%s\tFAIL(%s)\n' "${name}" "${status}" >>"${results}"
    fi
}

run_case api "${root}/scripts/test-unit/test-unit-api.sh"
run_case official-sdk "${root}/tests/e2e/run-sdk.sh"
run_case powersync "${root}/tests/e2e/run-powersync.sh"

cat "${results}"
if grep -q 'FAIL' "${results}"; then
    exit 1
fi
