#!/bin/sh
set -eu

# Instrument the entire module even when another package's tests execute the
# code. Deterministic OpenAPI generated packages are excluded from the
# denominator; their source contract and regeneration drift have a separate
# gate. Milestone 02 deliberately puts most business coverage in isolated Hurl
# and PostgreSQL contracts, which Go's cover profile cannot observe across the
# Compose process boundary. This floor catches accidental loss of the focused
# Go tests; test-unit.sh separately requires every API story and SQL contract.
go_command="${GO:-go}"
profile="$(mktemp "${TMPDIR:-/tmp}/gizway-cover.XXXXXX")"
script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
. "${script_dir}/common/postgresql.sh"
start_test_postgresql
trap 'rm -f "${profile}"; stop_test_postgresql' EXIT INT TERM

cover_packages="$(${go_command} list ./... | grep -v '/internal/generated/' | paste -sd, -)"
"${go_command}" test ./... -count=1 -coverpkg="${cover_packages}" -coverprofile="${profile}"
coverage="$("${go_command}" tool cover -func="${profile}" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"

awk -v value="${coverage}" 'BEGIN {
    if (value <= 25) {
        printf "Go-instrumented coverage %s%% does not exceed required 25%% floor\n", value > "/dev/stderr"
        exit 1
    }
    printf "Go-instrumented coverage floor passed: %s%% (black-box coverage is enforced separately)\n", value
}'
