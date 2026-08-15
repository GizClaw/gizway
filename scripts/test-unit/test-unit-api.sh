#!/bin/sh
set -u

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
compose="${repository_root}/tests/e2e/compose.yaml"
results="$(mktemp "${TMPDIR:-/tmp}/gizway-m03-api.XXXXXX")"
project="gizway-m03-api-$$"

cleanup() {
    status=$?
    docker compose --project-name "${project}" -f "${compose}" --profile '*' \
        down --volumes --remove-orphans >/dev/null 2>&1 || true
    rm -f "${results}" || true
    return "${status}"
}
trap cleanup EXIT INT TERM

for command in docker hurlfmt go; do
    if ! command -v "${command}" >/dev/null 2>&1; then
        echo "${command} is required to run Milestone 03 API contracts" >&2
        exit 1
    fi
done

stories="$(find "${repository_root}/tests/api/stories/24-milestone-03" -type f -name '*.hurl' -print | sort)"
for story in ${stories}; do
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

if docker compose --project-name "${project}" -f "${compose}" --profile milestone-03-api up --build --detach; then
    bootstrap_id="$(docker compose --project-name "${project}" -f "${compose}" ps --all --quiet bootstrap-milestone-03)"
else
    bootstrap_id=""
fi

stack_ready=false
if [ -n "${bootstrap_id}" ] && docker wait "${bootstrap_id}" >/dev/null \
   && [ "$(docker inspect --format '{{.State.ExitCode}}' "${bootstrap_id}")" -eq 0 ]; then
    stack_ready=true
fi

for story in ${stories}; do
    name="$(basename "${story}" .hurl)"
    if [ -n "${MILESTONE03_STORY_FILTER:-}" ] && [ "${name}" != "${MILESTONE03_STORY_FILTER}" ]; then
        continue
    fi
    if [ "${stack_ready}" = true ] && docker compose --project-name "${project}" -f "${compose}" --profile milestone-03-api run --rm --no-deps \
        hurl-api --test --variables-file /fixtures/m03.vars "/workspace/${story#${repository_root}/}"; then
        printf '%s\tPASS\n' "${story#${repository_root}/}" >>"${results}"
    else
        printf '%s\tFAIL\n' "${story#${repository_root}/}" >>"${results}"
    fi
done

if [ "${stack_ready}" = true ] && docker compose --project-name "${project}" -f "${compose}" exec -T postgres-gizpay \
    psql -v ON_ERROR_STOP=1 -U postgres -d gizpay <"${repository_root}/tests/e2e/sql/milestone-03-gizpay-contract.sql"; then
    printf 'gizpay-database-audit\tPASS\n' >>"${results}"
else
    printf 'gizpay-database-audit\tFAIL\n' >>"${results}"
fi

for database in postgres-cn postgres-global; do
    if [ "${stack_ready}" = true ] && docker compose --project-name "${project}" -f "${compose}" exec -T "${database}" \
        psql -v ON_ERROR_STOP=1 -U postgres -d gizway <"${repository_root}/tests/e2e/sql/milestone-03-gizway-contract.sql"; then
        printf '%s-database-audit\tPASS\n' "${database}" >>"${results}"
    else
        printf '%s-database-audit\tFAIL\n' "${database}" >>"${results}"
    fi
done

logs="$(docker compose --project-name "${project}" -f "${compose}" logs --no-color --no-log-prefix 2>/dev/null || true)"
if printf '%s' "${logs}" | grep -Eq 'giz_sk_|cn-provider-secret|global-provider-secret|BEGIN (RSA |EC )?PRIVATE KEY'; then
    printf 'credential-log-audit\tFAIL\n' >>"${results}"
else
    printf 'credential-log-audit\tPASS\n' >>"${results}"
fi

if [ "${stack_ready}" != true ]; then
    docker compose --project-name "${project}" -f "${compose}" --profile '*' logs --tail 200 --no-log-prefix || true
fi
cat "${results}"
if grep -Eq 'PARSE_FAIL|[[:space:]]FAIL$' "${results}"; then
    exit 1
fi
