#!/bin/sh
set -eu

# Run the complete modernize analyzer suite over every module package. The only
# permitted exclusion is a file carrying Go's exact generated-code marker.
# Package and directory allowlists are intentionally unsupported: handwritten
# production, test, command, fixture, and migration-embedding code all belong
# to this gate.
go_command="${GO:-go}"
diagnostics="$(mktemp "${TMPDIR:-/tmp}/gizway-modernize.XXXXXX")"
trap 'rm -f "${diagnostics}"' EXIT INT TERM

if "${go_command}" tool modernize ./... >"${diagnostics}" 2>&1; then
    exit 0
fi

has_failure=false
while IFS= read -r diagnostic || [ -n "${diagnostic}" ]; do
    source_file="$(printf '%s\n' "${diagnostic}" | sed -E 's/^(.+):[0-9]+:[0-9]+: .*/\1/')"
    if [ "${source_file}" != "${diagnostic}" ] && [ -f "${source_file}" ] && \
        grep -q '^// Code generated .* DO NOT EDIT\.$' "${source_file}"; then
        continue
    fi
    printf '%s\n' "${diagnostic}" >&2
    has_failure=true
done <"${diagnostics}"

if [ "${has_failure}" = true ]; then
    exit 1
fi
