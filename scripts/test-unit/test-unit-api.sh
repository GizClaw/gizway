#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
cd "${repository_root}"
. "${script_dir}/common/postgresql.sh"
start_test_postgresql
# Keep the disposable database owned by this runner recoverable even when a
# prerequisite or contract check fails before the full process cleanup exists.
trap stop_test_postgresql EXIT INT TERM

# Each Hurl file is an independent executable product specification. This
# runner deliberately starts a new process and isolated PostgreSQL schema for
# every file, preventing state or ordering dependencies between stories.
for required_command in hurl curl go lsof; do
    if ! command -v "${required_command}" >/dev/null 2>&1; then
        echo "${required_command} is required to run API stories" >&2
        exit 1
    fi
done

# Contract structure and route conformance fail before any story process is
# started, producing a focused error for a broken OpenAPI document.
"${script_dir}/test-unit-api-openapi.sh"
"${script_dir}/test-unit-api-contracts.sh"

# A single Hurl process cannot treat an expected server-side os.Exit as a
# successful request, so the process-level recovery contract has a dedicated
# harness. It still uses Hurl for every post-restart business assertion.
"${script_dir}/test-unit-api-recovery.sh"

story_list="$(find tests/api/stories -type f -name '*.hurl' -print | sort)"
if [ -z "${story_list}" ]; then
    echo "no Hurl story files found under tests/api/stories" >&2
    exit 1
fi

run_root="$(mktemp -d)"
server_pid=""
fake_pid=""
catalog_fake_pid=""
payment_pid=""
risk_pid=""
database_schema=""

mkdir -p "${run_root}/bin"
go build -o "${run_root}/bin/gizway" ./cmd/gizway
go build -o "${run_root}/bin/fake-ai-provider" ./cmd/fake-ai-provider
go build -o "${run_root}/bin/fake-payment-provider" ./cmd/fake-payment-provider
go build -o "${run_root}/bin/fake-risk-provider" ./cmd/fake-risk-provider

assert_port_free() {
    port="$1"
    if lsof -nP -iTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1; then
        echo "test port ${port} is already occupied; refusing to contact a stale process" >&2
        lsof -nP -iTCP:"${port}" -sTCP:LISTEN >&2 || true
        exit 1
    fi
}

cleanup_server() {
    if [ -n "${server_pid}" ]; then
        kill "${server_pid}" 2>/dev/null || true
        wait "${server_pid}" 2>/dev/null || true
        server_pid=""
    fi
	if [ -n "${fake_pid}" ]; then
        kill "${fake_pid}" 2>/dev/null || true
        wait "${fake_pid}" 2>/dev/null || true
        fake_pid=""
	fi
	if [ -n "${catalog_fake_pid}" ]; then
		kill "${catalog_fake_pid}" 2>/dev/null || true
		wait "${catalog_fake_pid}" 2>/dev/null || true
		catalog_fake_pid=""
	fi
    if [ -n "${payment_pid}" ]; then
        kill "${payment_pid}" 2>/dev/null || true
        wait "${payment_pid}" 2>/dev/null || true
        payment_pid=""
    fi
    if [ -n "${risk_pid}" ]; then
        kill "${risk_pid}" 2>/dev/null || true
        wait "${risk_pid}" 2>/dev/null || true
        risk_pid=""
    fi
}

cleanup_all() {
    cleanup_server
    if [ -n "${database_schema}" ]; then
        drop_test_postgresql_schema "${database_schema}"
        database_schema=""
    fi
    stop_test_postgresql
    rm -rf "${run_root}"
}
trap cleanup_all EXIT INT TERM

story_number=0
for story_file in ${story_list}; do
    story_number=$((story_number + 1))
    api_port=$((${GIZWAY_TEST_PORT_BASE:-18080} + story_number - 1))
    api_url="http://127.0.0.1:${api_port}"
    fake_port=$((19000 + story_number - 1))
	fake_url="http://127.0.0.1:${fake_port}"
	catalog_fake_port=$((19200 + story_number - 1))
	catalog_fake_url="http://127.0.0.1:${catalog_fake_port}"
    payment_port=$((19100 + story_number - 1))
    payment_url="http://127.0.0.1:${payment_port}"
    risk_port=$((19300 + story_number - 1))
    risk_url="http://127.0.0.1:${risk_port}"
	assert_port_free "${api_port}"
	assert_port_free "${fake_port}"
	assert_port_free "${catalog_fake_port}"
	assert_port_free "${payment_port}"
	assert_port_free "${risk_port}"
    story_dir="${run_root}/story-${story_number}"
    mkdir -p "${story_dir}"
    database_schema="gizway_story_$$_${story_number}"
    create_test_postgresql_schema "${database_schema}"
    database_dsn="$(test_postgresql_schema_dsn "${database_schema}")"
    server_log="${story_dir}/server.log"

    "${run_root}/bin/fake-ai-provider" -address "127.0.0.1:${fake_port}" \
        -callback-secret "story-ai-callback-secret" \
        >"${story_dir}/fake-ai.log" 2>&1 &
	fake_pid=$!
	"${run_root}/bin/fake-ai-provider" -address "127.0.0.1:${catalog_fake_port}" \
		-credential "story-catalog-provider-key" \
		-callback-secret "story-ai-callback-secret" \
		>"${story_dir}/catalog-fake-ai.log" 2>&1 &
	catalog_fake_pid=$!

    "${run_root}/bin/fake-payment-provider" -address "127.0.0.1:${payment_port}" \
		-callback-secret "story-callback-secret" \
		-fixed-now "2026-08-11T12:00:00Z" >"${story_dir}/fake-payment.log" 2>&1 &
    payment_pid=$!

    "${run_root}/bin/fake-risk-provider" -address "127.0.0.1:${risk_port}" \
        -credential "story-risk-key" >"${story_dir}/fake-risk.log" 2>&1 &
    risk_pid=$!

    GIZWAY_AI_PROVIDER_CREDENTIAL=story-provider-key \
    GIZWAY_AI_PROVIDER_CALLBACK_SECRET=story-ai-callback-secret \
    GIZWAY_PAYMENT_PROVIDER_CREDENTIAL=story-payment-key \
    GIZWAY_PAYMENT_CALLBACK_SECRET=story-callback-secret \
    GIZWAY_RISK_PROVIDER_CREDENTIAL=story-risk-key "${run_root}/bin/gizway" \
        -address "127.0.0.1:${api_port}" \
        -postgres-dsn "${database_dsn}" \
        -story-test-mode \
        -ai-provider-base-url "${fake_url}" \
		-ai-provider-callback-url "${api_url}" \
        -payment-provider-base-url "${payment_url}" \
		-checkout-base-url "${api_url}" \
        -risk-provider-base-url "${risk_url}" \
        >"${server_log}" 2>&1 &
    server_pid=$!

    attempt=0
    until curl --fail --silent "${api_url}/healthz" >/dev/null; do
        attempt=$((attempt + 1))
        if ! kill -0 "${server_pid}" 2>/dev/null || [ "${attempt}" -ge 100 ]; then
            echo "Gizway did not become ready for ${story_file}" >&2
            sed -n '1,200p' "${server_log}" >&2
            exit 1
        fi
        sleep 0.1
    done

    echo "Running ${story_file}"
    if ! hurl --test \
        --variable "base_url=${api_url}" \
		--variable "fake_url=${fake_url}" \
		--variable "catalog_fake_url=${catalog_fake_url}" \
		--variable "risk_url=${risk_url}" \
		--variable "payment_url=${payment_url}" \
		--variable "checkout_url=${api_url}" \
        --variable "active_user_one_key=gzs_story_user_active_1" \
        --variable "active_user_two_key=gzs_story_user_active_2" \
        --variable "suspended_user_key=gzs_story_user_suspended" \
        --variable "gateway_api_key=giz_story_user_active_1" \
        --variable "gateway_api_key_two=giz_story_user_active_2" \
        --variable "admin_api_key=gizadm_story_admin" \
        "${story_file}"; then
        sed -n '1,200p' "${server_log}" >&2
        exit 1
    fi

    cleanup_server
	drop_test_postgresql_schema "${database_schema}"
	database_schema=""
	assert_port_free "${api_port}"
	assert_port_free "${fake_port}"
	assert_port_free "${catalog_fake_port}"
	assert_port_free "${payment_port}"
	assert_port_free "${risk_port}"
done
