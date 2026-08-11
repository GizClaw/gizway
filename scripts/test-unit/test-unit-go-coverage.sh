#!/bin/sh
set -eu

# Go tests own internal correctness, failure paths and transaction boundaries.
# Hurl owns business behavior. This gate deliberately measures the whole Go
# module, including cmd wiring, and requires the agreed strict >80% threshold.
go_command="${GO:-go}"
profile="$(mktemp "${TMPDIR:-/tmp}/gizway-cover.XXXXXX")"
trap 'rm -f "${profile}"' EXIT INT TERM

"${go_command}" test ./... -coverpkg=./... -coverprofile="${profile}"
coverage="$("${go_command}" tool cover -func="${profile}" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"

awk -v value="${coverage}" 'BEGIN {
    if (value <= 80) {
        printf "Go coverage %s%% does not exceed required 80%%\n", value > "/dev/stderr"
        exit 1
    }
    printf "Go coverage gate passed: %s%%\n", value
}'
