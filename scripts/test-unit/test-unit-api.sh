#!/bin/sh
set -u

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
compose="${repository_root}/tests/e2e/compose.yaml"
results="$(mktemp "${TMPDIR:-/tmp}/gizway-m04-api.XXXXXX")"
project="gizway-m04-api-$$"

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
        echo "${command} is required to run API contracts" >&2
        exit 1
    fi
done

stories="$(find "${repository_root}/tests/api/stories/24-milestone-03" "${repository_root}/tests/api/stories/25-milestone-04" "${repository_root}/tests/api/stories/26-admin" -type f -name '*.hurl' -print | sort)"
for story in ${stories}; do
    if hurlfmt --check "${story}"; then
        printf '%s\tPARSE_PASS\n' "${story#${repository_root}/}" >>"${results}"
    else
        printf '%s\tPARSE_FAIL\n' "${story#${repository_root}/}" >>"${results}"
    fi
done

if "${script_dir}/test-unit-api-openapi.sh" && "${script_dir}/test-unit-api-contracts.sh" && "${script_dir}/check-e2e-api-seed.sh"; then
    printf 'openapi\tPASS\n' >>"${results}"
else
    printf 'openapi\tFAIL\n' >>"${results}"
fi

if docker compose --project-name "${project}" -f "${compose}" --profile milestone-03-api up --build --detach; then
    bootstrap_id="$(docker compose --project-name "${project}" -f "${compose}" ps --all --quiet bootstrap-milestone-03)"
else
    bootstrap_id=""
fi

migrations_ready=true
for migration in gizpay-migrate gizway-cn-migrate gizway-global-migrate; do
    migration_id="$(docker compose --project-name "${project}" -f "${compose}" ps --all --quiet "${migration}")"
    if [ -z "${migration_id}" ] || ! docker wait "${migration_id}" >/dev/null \
       || [ "$(docker inspect --format '{{.State.ExitCode}}' "${migration_id}")" -ne 0 ]; then
        migrations_ready=false
        printf '%s\tFAIL\n' "${migration}" >>"${results}"
    else
        printf '%s\tPASS\n' "${migration}" >>"${results}"
    fi
done

migration_replay=false
if [ "${migrations_ready}" = true ]; then
    gizpay_before="$(docker compose --project-name "${project}" -f "${compose}" exec -T postgres-gizpay \
        psql -At -U postgres -d gizpay -c "SELECT service,version,applied_at FROM schema_migrations ORDER BY service,version; SELECT id,identity_issuer,identity_subject,email,display_name,status FROM users WHERE id='usr_platform'; SELECT id,owner_user_id,status FROM accounts WHERE id='acct_platform'; SELECT id,coalesce(owner_account_id,''),asset_code,status FROM ledger_accounts WHERE id IN ('led_acct_platform','led_clearing') ORDER BY id")"
    cn_before="$(docker compose --project-name "${project}" -f "${compose}" exec -T postgres-cn \
        psql -At -U postgres -d gizway -c 'SELECT service,version,applied_at FROM gizway.schema_migrations ORDER BY service,version')"
    global_before="$(docker compose --project-name "${project}" -f "${compose}" exec -T postgres-global \
        psql -At -U postgres -d gizway -c 'SELECT service,version,applied_at FROM gizway.schema_migrations ORDER BY service,version')"

    if docker compose --project-name "${project}" -f "${compose}" run --rm --no-deps gizpay-migrate \
       && docker compose --project-name "${project}" -f "${compose}" run --rm --no-deps gizway-cn-migrate \
       && docker compose --project-name "${project}" -f "${compose}" run --rm --no-deps gizway-global-migrate; then
        gizpay_after="$(docker compose --project-name "${project}" -f "${compose}" exec -T postgres-gizpay \
            psql -At -U postgres -d gizpay -c "SELECT service,version,applied_at FROM schema_migrations ORDER BY service,version; SELECT id,identity_issuer,identity_subject,email,display_name,status FROM users WHERE id='usr_platform'; SELECT id,owner_user_id,status FROM accounts WHERE id='acct_platform'; SELECT id,coalesce(owner_account_id,''),asset_code,status FROM ledger_accounts WHERE id IN ('led_acct_platform','led_clearing') ORDER BY id")"
        cn_after="$(docker compose --project-name "${project}" -f "${compose}" exec -T postgres-cn \
            psql -At -U postgres -d gizway -c 'SELECT service,version,applied_at FROM gizway.schema_migrations ORDER BY service,version')"
        global_after="$(docker compose --project-name "${project}" -f "${compose}" exec -T postgres-global \
            psql -At -U postgres -d gizway -c 'SELECT service,version,applied_at FROM gizway.schema_migrations ORDER BY service,version')"
        if [ "${gizpay_before}" = "${gizpay_after}" ] && [ "${cn_before}" = "${cn_after}" ] && [ "${global_before}" = "${global_after}" ]; then
            migration_replay=true
        fi
    fi
fi
if [ "${migration_replay}" = true ]; then
    printf 'migration-replay\tPASS\n' >>"${results}"
else
    printf 'migration-replay\tFAIL\n' >>"${results}"
fi

stack_ready=false
if [ "${migrations_ready}" = true ] && [ -n "${bootstrap_id}" ] && docker wait "${bootstrap_id}" >/dev/null \
   && [ "$(docker inspect --format '{{.State.ExitCode}}' "${bootstrap_id}")" -eq 0 ]; then
    stack_ready=true
fi

for story in ${stories}; do
    name="$(basename "${story}" .hurl)"
    if [ -n "${MILESTONE_STORY_FILTER:-${MILESTONE03_STORY_FILTER:-}}" ] && [ "${name}" != "${MILESTONE_STORY_FILTER:-${MILESTONE03_STORY_FILTER:-}}" ]; then
        continue
    fi
    if [ "${stack_ready}" = true ] && docker compose --project-name "${project}" -f "${compose}" --profile milestone-03-api run --rm --no-deps \
        hurl-api --test --variables-file /fixtures/m03.vars --secret "admin_key=$(docker compose --project-name "${project}" -f "${compose}" exec -T gizpay cat /fixtures/admin-key)" "/workspace/${story#${repository_root}/}"; then
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
	docker compose --project-name "${project}" -f "${compose}" logs --tail 200 --no-log-prefix gizpay-migrate gizway-cn-migrate gizway-global-migrate gizpay gizway-cn gizway-global credit-spy oauth-spy bootstrap-milestone-03 || true
    docker compose --project-name "${project}" -f "${compose}" exec -T oauth-spy curl --fail --silent http://localhost:19500/test/stats || true
    exit 1
fi
