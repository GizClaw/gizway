#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
cd "${repository_root}"
. "${script_dir}/common/postgresql.sh"
start_test_postgresql
# Standalone recovery runs may own the disposable database. Install the narrow
# lifecycle guard before dependency checks and replace it with full cleanup
# after the per-run schema and process state have been initialized.
trap stop_test_postgresql EXIT INT TERM

# This harness supplies the one thing a single Hurl process cannot express:
# an expected TCP disconnect caused by an actual process exit, followed by a
# restart on the same database. All post-restart business assertions remain in
# the heavily commented Hurl contract beside this script.
for required_command in hurl curl go lsof; do
    if ! command -v "${required_command}" >/dev/null 2>&1; then
        echo "${required_command} is required for AI crash recovery" >&2
        exit 1
    fi
done

run_root="$(mktemp -d)"
api_port="${GIZWAY_CRASH_TEST_PORT_BASE:-19880}"
fake_port=$((api_port + 1))
api_url="http://127.0.0.1:${api_port}"
fake_url="http://127.0.0.1:${fake_port}"
database_schema="gizway_recovery_$$"
create_test_postgresql_schema "${database_schema}"
database_dsn="$(test_postgresql_schema_dsn "${database_schema}")"
crash_marker="${run_root}/provider-succeeded.marker"
server_log="${run_root}/gizway.log"
server_pid=""
fake_pid=""

assert_port_free() {
    port="$1"
    if lsof -nP -iTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1; then
        echo "crash-recovery port ${port} is already occupied" >&2
        lsof -nP -iTCP:"${port}" -sTCP:LISTEN >&2 || true
        exit 1
    fi
}

cleanup() {
    if [ -n "${server_pid}" ]; then
        kill "${server_pid}" 2>/dev/null || true
        wait "${server_pid}" 2>/dev/null || true
    fi
    if [ -n "${fake_pid}" ]; then
        kill "${fake_pid}" 2>/dev/null || true
        wait "${fake_pid}" 2>/dev/null || true
    fi
    drop_test_postgresql_schema "${database_schema}"
    stop_test_postgresql
    rm -rf "${run_root}"
}
trap cleanup EXIT INT TERM

assert_port_free "${api_port}"
assert_port_free "${fake_port}"

go build -o "${run_root}/gizway" ./cmd/gizway
go build -o "${run_root}/fake-ai-provider" ./cmd/fake-ai-provider

"${run_root}/fake-ai-provider" -address "127.0.0.1:${fake_port}" >"${run_root}/fake-ai.log" 2>&1 &
fake_pid=$!

start_server() {
    mode="$1"
    GIZWAY_AI_PROVIDER_CREDENTIAL=story-provider-key \
    GIZWAY_AI_PROVIDER_CALLBACK_SECRET=story-ai-callback-secret \
    GIZWAY_STORY_CRASH_AFTER_PROVIDER_FILE="${crash_marker}" \
    "${run_root}/gizway" -address "127.0.0.1:${api_port}" \
        -postgres-dsn "${database_dsn}" "${mode}" \
        -ai-provider-base-url "${fake_url}" >>"${server_log}" 2>&1 &
    server_pid=$!
}

wait_ready() {
    attempt=0
    until curl --fail --silent "${api_url}/healthz" >/dev/null; do
        attempt=$((attempt + 1))
        if ! kill -0 "${server_pid}" 2>/dev/null || [ "${attempt}" -ge 100 ]; then
            echo "Gizway did not become ready during crash recovery" >&2
            sed -n '1,240p' "${server_log}" >&2
            exit 1
        fi
        sleep 0.1
    done
}

start_server -story-test-mode
wait_ready

# A successful provider response triggers os.Exit(86), so curl must observe a
# broken connection rather than any fabricated HTTP success.
set +e
curl --silent --show-error --max-time 10 \
    -H "Authorization: Bearer giz_story_user_active_1" \
    -H "Idempotency-Key: story-crash-after-provider-success" \
    -H "Content-Type: application/json" \
    --data '{"model":"story-text","messages":[{"role":"user","content":"recover after provider success"}],"stream":false}' \
    "${api_url}/v1/chat/completions" >"${run_root}/crash-response" 2>"${run_root}/curl-error"
curl_status=$?
wait "${server_pid}"
process_status=$?
set -e
server_pid=""

if [ "${curl_status}" -eq 0 ] || [ "${process_status}" -ne 86 ] || [ ! -f "${crash_marker}" ]; then
    echo "expected provider-success crash did not occur (curl=${curl_status}, process=${process_status})" >&2
    sed -n '1,240p' "${server_log}" >&2
    exit 1
fi

# The post-restart Hurl advances the controllable business clock beyond the
# story lease. No wall-clock sleep is needed for a deterministic state test.
start_server -story-resume-mode
wait_ready

hurl --test \
    --variable "base_url=${api_url}" \
    --variable "fake_url=${fake_url}" \
    --variable "gateway_api_key=giz_story_user_active_1" \
    --variable "active_user_one_key=gzs_story_user_active_1" \
    --variable "admin_api_key=gizadm_story_admin" \
    tests/api/recovery/01-provider-success-crash-recovery.hurl

cleanup
server_pid=""
fake_pid=""
assert_port_free "${api_port}"
assert_port_free "${fake_port}"
