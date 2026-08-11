#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
cd "${repository_root}"

# Hurl is the executable source of business requirements. Each Gizway-owned
# OpenAPI operation must appear in a leading `# covers:` declaration, while a
# stale declaration must fail as soon as its operation disappears.
for command in rg sed sort comm mktemp; do
    if ! command -v "${command}" >/dev/null 2>&1; then
        echo "${command} is required to check API contracts" >&2
        exit 1
    fi
done

work="$(mktemp -d "${TMPDIR:-/tmp}/gizway-contracts.XXXXXX")"
trap 'rm -rf "${work}"' EXIT INT TERM

rg -o '^\s*operationId:\s*\S+' api/openapi/*.yaml \
    | sed -E 's/.*operationId:[[:space:]]*//' | sort >"${work}/operations"
rg -o '# covers:.*' tests/api/stories -g '*.hurl' \
    | sed -E 's/.*# covers:[[:space:]]*//' | tr ' ' '\n' \
    | sed '/^$/d' | sort -u >"${work}/stories"

if [ ! -s "${work}/operations" ] || [ ! -s "${work}/stories" ]; then
    echo "OpenAPI operations and Hurl coverage declarations must not be empty" >&2
    exit 1
fi

duplicate_operations="$(uniq -d "${work}/operations")"
missing="$(comm -23 "${work}/operations" "${work}/stories")"
stale="$(comm -13 "${work}/operations" "${work}/stories")"
if [ -n "${duplicate_operations}" ] || [ -n "${missing}" ] || [ -n "${stale}" ]; then
    [ -z "${duplicate_operations}" ] || printf 'duplicate operationId:\n%s\n' "${duplicate_operations}" >&2
    [ -z "${missing}" ] || printf 'operations missing Hurl coverage:\n%s\n' "${missing}" >&2
    [ -z "${stale}" ] || printf 'stale Hurl coverage names:\n%s\n' "${stale}" >&2
    exit 1
fi

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

count="$(wc -l <"${work}/operations" | tr -d ' ')"
protocol_count="$(wc -l <"${work}/protocol-cases" | tr -d ' ')"
echo "API contract coverage gate passed: ${count} operationIds, ${protocol_count} Hurl protocol cases"
