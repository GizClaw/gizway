#!/bin/sh
set -eu

# Instrument the entire module even when an individual package's tests execute
# the code. This prevents a small allow-list of easy packages from presenting a
# misleading percentage while core API, app, store, Gateway, and binaries stay
# untested.
go_command="${GO:-go}"
profile="$(mktemp "${TMPDIR:-/tmp}/gizway-cover.XXXXXX")"
script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
. "${script_dir}/common/postgresql.sh"
start_test_postgresql
trap 'rm -f "${profile}"; stop_test_postgresql' EXIT INT TERM

"${go_command}" test ./... -count=1 -coverpkg=./... -coverprofile="${profile}"
coverage="$("${go_command}" tool cover -func="${profile}" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"

awk -v value="${coverage}" 'BEGIN {
    if (value <= 80) {
        printf "Go module coverage %s%% does not exceed required 80%%\n", value > "/dev/stderr"
        exit 1
    }
    printf "Go module coverage gate passed: %s%%\n", value
}'
