#!/bin/sh
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
compose="${root}/tests/e2e/compose.yaml"
active_project=""

cleanup() {
    if [ -n "${active_project}" ]; then
        docker compose --project-name "${active_project}" -f "${compose}" \
            --profile quota --profile product --profile failure --profile failure-post \
            down --volumes --remove-orphans >/dev/null 2>&1 || true
        active_project=""
    fi
}
trap cleanup EXIT INT TERM

start_profile() {
    profile="$1"
    active_project="gizway-refactor-e2e-${profile}-$$"
    if ! docker compose --project-name "${active_project}" -f "${compose}" --profile "${profile}" up --build --detach; then
        docker compose --project-name "${active_project}" -f "${compose}" --profile "${profile}" logs --no-log-prefix
        return 1
    fi
}

wait_hurl() {
    profile="$1"
    service="$2"
    set +e
    docker compose --project-name "${active_project}" -f "${compose}" --profile "${profile}" wait "${service}"
    status=$?
    set -e
    docker compose --project-name "${active_project}" -f "${compose}" --profile "${profile}" logs --no-log-prefix "${service}"
    return "${status}"
}

assert_zero_rows() {
    database_service="$1"
    description="$2"
    query="$3"
    rows="$(docker compose --project-name "${active_project}" -f "${compose}" \
        exec -T "${database_service}" psql -U postgres -d gizway -Atqc "${query}")"
    if [ "${rows}" != "0" ]; then
        echo "E2E final database audit failed: ${description} (${rows} rows)" >&2
        return 1
    fi
}

verify_database_invariants() {
    # End-state acceptance is independent of HTTP status assertions: every
    # posted transaction balances, every UCGID is single-charge, only the
    # intentional overdraft story account may be negative, and no legacy AI
    # credit-reservation structure or ledger vocabulary has returned.
    assert_zero_rows postgres-gizpay "unbalanced posted ledger transactions" \
        "SELECT count(*) FROM (SELECT lt.id FROM ledger_transactions lt LEFT JOIN ledger_entries le ON le.transaction_id = lt.id WHERE lt.status = 'posted' GROUP BY lt.id HAVING count(le.id) < 2 OR COALESCE(SUM(CASE le.direction WHEN 'debit' THEN le.amount_microcredits WHEN 'credit' THEN -le.amount_microcredits ELSE 0 END), 0) <> 0) AS unbalanced"
    assert_zero_rows postgres-gizpay "duplicate received Usage UCGIDs" \
        "SELECT count(*) FROM (SELECT ucgid FROM gateway_usage_records GROUP BY ucgid HAVING count(*) > 1) AS duplicate_usage"
    assert_zero_rows postgres-gizpay "unexpected negative account balances" \
        "SELECT count(*) FROM account_balances WHERE balance_microcredits < 0 AND account_id <> 'e2e-account'"
    assert_zero_rows postgres-gizpay "AI credit reservation tables" \
        "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name ILIKE '%reservation%'"
    assert_zero_rows postgres-cn "CN AI credit reservation tables" \
        "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name ILIKE '%reservation%'"
    assert_zero_rows postgres-global "Global AI credit reservation tables" \
        "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name ILIKE '%reservation%'"
    assert_zero_rows postgres-gizpay "AI credit reservation ledger records" \
        "SELECT count(*) FROM ledger_transactions WHERE transaction_type ILIKE '%reservation%' OR COALESCE(reference_type, '') ILIKE '%reservation%' OR description ILIKE '%reservation%'"
}

verify_logs_do_not_leak_secrets() {
    logs="$(docker compose --project-name "${active_project}" -f "${compose}" logs --no-color --no-log-prefix \
        gizpay gizway-cn gizway-global bootstrap-central bootstrap-cn bootstrap-global \
        fake-provider-cn fake-provider-global fake-payment-provider fake-risk-provider)"

    # Never print a matching line: doing so would repeat the leaked credential
    # in CI output. Prefixes catch dynamically-created user/admin secrets, while
    # literals cover provider and callback credentials used by this fixture.
    if printf '%s' "${logs}" | grep -Eq 'giz(adm)?_|gzs_|BEGIN (RSA |EC )?PRIVATE KEY'; then
        echo "E2E final log audit failed: API key or certificate private key material was logged" >&2
        return 1
    fi
    for credential in story-provider-key story-payment-key e2e-risk-key e2e-callback-secret; do
        case "${logs}" in
            *"${credential}"*)
                echo "E2E final log audit failed: provider credential was logged" >&2
                return 1
                ;;
        esac
    done
}

verify_final_state() {
    verify_database_invariants
    verify_logs_do_not_leak_secrets
}

run_standard_profile() {
    profile="$1"
    service="$2"
    start_profile "${profile}"
    wait_hurl "${profile}" "${service}"
    verify_final_state
    cleanup
}

run_failure_profile() {
    start_profile failure
    wait_hurl failure hurl-failure-pre

    # Restart only the regional process. Its PostgreSQL and GizPay remain up,
    # proving quota is re-queried instead of restored from local persistence.
    docker compose --project-name "${active_project}" -f "${compose}" restart gizway-global >/dev/null
    attempt=0
    until docker compose --project-name "${active_project}" -f "${compose}" exec -T gizway-global \
        curl --fail --silent http://localhost:8080/readyz >/dev/null; do
        attempt=$((attempt + 1))
        if [ "${attempt}" -ge 60 ]; then
            docker compose --project-name "${active_project}" -f "${compose}" logs --no-log-prefix gizway-global
            return 1
        fi
        sleep 1
    done
    docker compose --project-name "${active_project}" -f "${compose}" --profile failure-post \
        run --rm --no-deps hurl-failure-post
    verify_final_state
    cleanup
}

profiles="${*:-quota product failure}"
for profile in ${profiles}; do
    case "${profile}" in
        quota) run_standard_profile quota hurl-quota ;;
        product) run_standard_profile product hurl-product ;;
        failure) run_failure_profile ;;
        *) echo "unknown E2E profile: ${profile}" >&2; exit 2 ;;
    esac
done
