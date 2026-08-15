#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
cd "${repository_root}"

# The Go contract checker binds every Milestone 03 document-qualified operation
# to a real request against its owning service. Provider-compatible protocols
# use stable Hurl case IDs, so an empty protocol inventory is a failure.
for command in rg sed sort comm mktemp; do
    if ! command -v "${command}" >/dev/null 2>&1; then
        echo "${command} is required to check API contracts" >&2
        exit 1
    fi
done

work="$(mktemp -d "${TMPDIR:-/tmp}/gizway-contracts.XXXXXX")"
trap 'rm -rf "${work}"' EXIT INT TERM

# Provider-compatible requirements live only in Hurl. Stable protocol-case IDs
# replace a separate manifest, preventing two sources of truth while still
# giving CI a machine-readable, duplicate-checked behavior inventory.
rg -l '^# protocol covers:' tests/api -g '*.hurl' | sort >"${work}/protocol-files"
rg -l '^# protocol-case:' tests/api -g '*.hurl' | sort >"${work}/protocol-case-files"
rg -o '^# protocol-case:[[:space:]]+[a-z0-9][a-z0-9._-]*' tests/api -g '*.hurl' \
    | sed -E 's/.*# protocol-case:[[:space:]]*//' | sort >"${work}/protocol-cases"

missing_protocol_case="$(comm -23 "${work}/protocol-files" "${work}/protocol-case-files")"
duplicate_protocol_cases="$(uniq -d "${work}/protocol-cases")"
if [ ! -s "${work}/protocol-cases" ] || [ -n "${missing_protocol_case}" ] || [ -n "${duplicate_protocol_cases}" ]; then
	[ -s "${work}/protocol-cases" ] || echo "Hurl protocol-case inventory must not be empty" >&2
    [ -z "${missing_protocol_case}" ] || printf 'protocol story missing protocol-case ID:\n%s\n' "${missing_protocol_case}" >&2
    [ -z "${duplicate_protocol_cases}" ] || printf 'duplicate protocol-case ID:\n%s\n' "${duplicate_protocol_cases}" >&2
    exit 1
fi

protocol_count="$(wc -l <"${work}/protocol-cases" | tr -d ' ')"
echo "API protocol inventory gate passed: ${protocol_count} Hurl protocol cases"
