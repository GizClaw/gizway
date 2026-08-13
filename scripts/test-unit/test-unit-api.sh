#!/bin/sh
set -u

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
compose="${repository_root}/tests/e2e/compose.yaml"
results="$(mktemp "${TMPDIR:-/tmp}/gizway-m02-api.XXXXXX")"
active_project=""

cleanup() {
    if [ -n "${active_project}" ]; then
        docker compose --project-name "${active_project}" -f "${compose}" --profile '*' \
            down --volumes --remove-orphans >/dev/null 2>&1 || true
    fi
    rm -f "${results}"
}
trap cleanup EXIT INT TERM

for command in docker hurl hurlfmt go; do
    if ! command -v "${command}" >/dev/null 2>&1; then
        echo "${command} is required to run Milestone 02 API contracts" >&2
        exit 1
    fi
done

# Parsing is independent from implementation state and must always complete.
for story in $(find "${repository_root}/tests/api/stories/23-milestone-02" -type f -name '*.hurl' -print | sort); do
    if hurlfmt --check "${story}"; then
        printf '%s\tPARSE_PASS\n' "${story#${repository_root}/}" >>"${results}"
    else
        printf '%s\tPARSE_FAIL\n' "${story#${repository_root}/}" >>"${results}"
    fi
done

if "${script_dir}/test-unit-api-openapi.sh" && "${script_dir}/test-unit-api-contracts.sh"; then
    printf 'openapi\tPASS\n' >>"${results}"
else
    printf 'openapi\tFAIL\n' >>"${results}"
fi

story_number=0
for story in $(find "${repository_root}/tests/api/stories/23-milestone-02" -type f -name '*.hurl' -print | sort); do
    if [ -n "${MILESTONE02_STORY_FILTER:-}" ] \
       && [ "$(basename "${story}" .hurl)" != "${MILESTONE02_STORY_FILTER}" ]; then
        continue
    fi
    story_number=$((story_number + 1))
    active_project="gizway-m02-api-$$_${story_number}"
    MILESTONE02_STORY="$(basename "${story}" .hurl)"
    export MILESTONE02_STORY
    if docker compose --project-name "${active_project}" -f "${compose}" --profile milestone-02-api up --build --detach; then
        bootstrap_id="$(docker compose --project-name "${active_project}" -f "${compose}" ps --all --quiet bootstrap-milestone-02)"
    else
        bootstrap_id=""
    fi
    if [ -n "${bootstrap_id}" ] \
       && docker wait "${bootstrap_id}" >/dev/null \
       && [ "$(docker inspect --format '{{.State.ExitCode}}' "${bootstrap_id}")" -eq 0 ] \
       && docker compose --project-name "${active_project}" -f "${compose}" --profile milestone-02-api run --rm --no-deps \
            hurl-api --test --variables-file /fixtures/e2e.vars "/workspace/${story#${repository_root}/}"; then
        printf '%s\tPASS\n' "${story#${repository_root}/}" >>"${results}"
    else
        printf '%s\tFAIL\n' "${story#${repository_root}/}" >>"${results}"
    fi
    docker compose --project-name "${active_project}" -f "${compose}" --profile '*' \
        down --volumes --remove-orphans >/dev/null 2>&1 || true
    active_project=""
done

cat "${results}"
if grep -Eq 'PARSE_FAIL|[[:space:]]FAIL$' "${results}"; then
    exit 1
fi
