#!/bin/sh
set -u

root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
compose="${root}/tests/e2e/compose.yaml"
results="$(mktemp "${TMPDIR:-/tmp}/gizway-m02-e2e.XXXXXX")"
active_project=""

cleanup_stack() {
    if [ -n "${active_project}" ]; then
        docker compose --project-name "${active_project}" -f "${compose}" --profile '*' \
            down --volumes --remove-orphans >/dev/null 2>&1 || true
        active_project=""
    fi
}

cleanup() {
    cleanup_stack
    rm -f "${results}"
}
trap cleanup EXIT INT TERM

record_case() {
    name="$1"
    shift
    if "$@"; then
        printf '%s\tPASS\n' "${name}" >>"${results}"
    else
        status=$?
        printf '%s\tFAIL(%s)\n' "${name}" "${status}" >>"${results}"
    fi
}

record_selected_case() {
    name="$1"
    shift
    if [ -z "${MILESTONE02_E2E_FILTER:-}" ] || [ "${MILESTONE02_E2E_FILTER}" = "${name}" ]; then
        record_case "${name}" "$@"
    fi
}

start_stack() {
    name="$1"
    active_project="gizway-m02-e2e-${name}-$$"
    if docker compose --project-name "${active_project}" -f "${compose}" --profile milestone-02 up --build --detach; then
        bootstrap_id="$(docker compose --project-name "${active_project}" -f "${compose}" ps --all --quiet bootstrap-milestone-02)"
        [ -n "${bootstrap_id}" ] || return 1
        docker wait "${bootstrap_id}" >/dev/null || return 1
        [ "$(docker inspect --format '{{.State.ExitCode}}' "${bootstrap_id}")" -eq 0 ]
        return $?
    fi
    docker compose --project-name "${active_project}" -f "${compose}" --profile milestone-02 logs --no-log-prefix || true
    return 1
}

show_logs() {
    docker compose --project-name "${active_project}" -f "${compose}" --profile '*' logs --tail 200 --no-log-prefix || true
}

run_hurl_service() {
    service="$1"
    docker compose --project-name "${active_project}" -f "${compose}" --profile milestone-02 \
        run --rm --no-deps "${service}"
}

central_database_audit() {
    docker compose --project-name "${active_project}" -f "${compose}" exec -T postgres-gizpay \
        psql -v ON_ERROR_STOP=1 -U postgres -d gizpay \
        <"${root}/tests/e2e/sql/milestone-02-ledger-contract.sql"
}

regional_database_audit() {
    service="$1"
    docker compose --project-name "${active_project}" -f "${compose}" exec -T "${service}" \
        psql -v ON_ERROR_STOP=1 -U postgres -d gizway \
        <"${root}/tests/e2e/sql/milestone-02-database-contract.sql"
}

secret_log_audit() {
    logs="$(docker compose --project-name "${active_project}" -f "${compose}" logs --no-color --no-log-prefix 2>/dev/null)"
    if printf '%s' "${logs}" | grep -Eq 'gzs_|gizrt_|BEGIN (RSA |EC )?PRIVATE KEY|story-provider-key'; then
        echo "Milestone 02 E2E log audit found credential material" >&2
        return 1
    fi
}

empty_api_case() {
    active_project="gizway-m02-e2e-empty-api-$$"
    status=0
    docker compose --project-name "${active_project}" -f "${compose}" --profile empty-api \
        up --build --detach gizway-empty || status=1
    if [ "${status}" -eq 0 ]; then
        empty_id="$(docker compose --project-name "${active_project}" -f "${compose}" ps --quiet gizway-empty)"
        attempt=0
        until [ "$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "${empty_id}")" = healthy ]; do
            attempt=$((attempt + 1))
            if [ "${attempt}" -ge 120 ] || [ "$(docker inspect --format '{{.State.Status}}' "${empty_id}")" != running ]; then
                status=1
                break
            fi
            sleep 1
        done
    fi
    [ "${status}" -ne 0 ] || run_hurl_service hurl-empty-api-closure || status=1
    [ "${status}" -ne 0 ] || central_database_audit || status=1
    [ "${status}" -ne 0 ] || regional_database_audit postgres-cn || status=1
    [ "${status}" -ne 0 ] || secret_log_audit || status=1
    finish_case "${status}"
}

run_common_audits() {
    central_database_audit &&
        regional_database_audit postgres-cn &&
        regional_database_audit postgres-global &&
        secret_log_audit
}

finish_case() {
    status="$1"
    if [ "${status}" -ne 0 ]; then
        show_logs
    fi
    cleanup_stack
    return "${status}"
}

regional_isolation_case() {
    start_stack regional-isolation || { cleanup_stack; return 1; }
    status=0
    run_hurl_service hurl-regional-isolation || status=1
    [ "${status}" -ne 0 ] || run_common_audits || status=1
    finish_case "${status}"
}

restart_credit_case() {
    start_stack restart-credit || { cleanup_stack; return 1; }
    status=0
    # Warm only Credit state; this creates no AI Order, Outbox row, or Charge.
    run_hurl_service hurl-restart-credit-warmup || status=1
    [ "${status}" -ne 0 ] || docker compose --project-name "${active_project}" -f "${compose}" restart gizway-global || status=1
    [ "${status}" -ne 0 ] || run_hurl_service hurl-restart-rechecks-credit || status=1
    [ "${status}" -ne 0 ] || run_common_audits || status=1
    finish_case "${status}"
}

outbox_response_loss_case() {
    start_stack outbox-response-loss || { cleanup_stack; return 1; }
    status=0
    run_hurl_service hurl-outbox-response-loss || status=1
    [ "${status}" -ne 0 ] || run_common_audits || status=1
    finish_case "${status}"
}

health_case() {
    start_stack health || { cleanup_stack; return 1; }
    status=0
    run_hurl_service hurl-health-no-remote-fanout || status=1
    [ "${status}" -ne 0 ] || docker compose --project-name "${active_project}" -f "${compose}" stop postgres-global || status=1
    [ "${status}" -ne 0 ] || run_hurl_service hurl-health-database-down || status=1
    [ "${status}" -ne 0 ] || secret_log_audit || status=1
    finish_case "${status}"
}

record_selected_case regional-isolation regional_isolation_case
record_selected_case restart-credit restart_credit_case
record_selected_case outbox-response-loss outbox_response_loss_case
record_selected_case health health_case
record_selected_case empty-api empty_api_case

cat "${results}"
if grep -q 'FAIL' "${results}"; then
    exit 1
fi
