#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
cd "${repository_root}"
. "${script_dir}/common/postgresql.sh"
start_test_postgresql
trap stop_test_postgresql EXIT INT TERM

for required_command in hurl curl go lsof openssl; do
    if ! command -v "${required_command}" >/dev/null 2>&1; then
        echo "${required_command} is required to run API stories" >&2
        exit 1
    fi
done

"${script_dir}/test-unit-api-openapi.sh"
"${script_dir}/test-unit-api-contracts.sh"
"${script_dir}/test-unit-api-recovery.sh"

if [ -n "${GIZWAY_TEST_STORY:-}" ]; then
    if [ ! -f "${GIZWAY_TEST_STORY}" ]; then
        echo "GIZWAY_TEST_STORY does not name a Hurl story: ${GIZWAY_TEST_STORY}" >&2
        exit 1
    fi
    story_list="${GIZWAY_TEST_STORY}"
else
    story_list="$(find tests/api/stories -type f -name '*.hurl' -print | sort)"
fi
if [ -z "${story_list}" ]; then
    echo "no Hurl story files found under tests/api/stories" >&2
    exit 1
fi

run_root="$(mktemp -d)"
pay_pid=""
way_pid=""
fake_pid=""
catalog_fake_pid=""
payment_pid=""
risk_pid=""
pay_schema=""
way_schema=""

mkdir -p "${run_root}/bin" "${run_root}/pki"
go build -o "${run_root}/bin/gizpay" ./cmd/gizpay
go build -o "${run_root}/bin/gizway" ./cmd/gizway
go build -o "${run_root}/bin/e2e-bootstrap" ./tests/e2e/bootstrap
go build -o "${run_root}/bin/fake-ai-provider" ./cmd/fake-ai-provider
go build -o "${run_root}/bin/fake-payment-provider" ./cmd/fake-payment-provider
go build -o "${run_root}/bin/fake-risk-provider" ./cmd/fake-risk-provider
sh tests/e2e/pki/generate.sh "${run_root}/pki"

assert_port_free() {
    port="$1"
    if lsof -nP -iTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1; then
        echo "test port ${port} is already occupied; refusing to contact a stale process" >&2
        exit 1
    fi
}

cleanup_processes() {
    for variable in pay_pid way_pid fake_pid catalog_fake_pid payment_pid risk_pid; do
        eval "pid=\${${variable}}"
        if [ -n "${pid}" ]; then
            kill "${pid}" 2>/dev/null || true
            wait "${pid}" 2>/dev/null || true
            eval "${variable}=''"
        fi
    done
}

cleanup_story() {
    cleanup_processes
    if [ -n "${pay_schema}" ]; then
        drop_test_postgresql_schema "${pay_schema}"
        pay_schema=""
    fi
    if [ -n "${way_schema}" ]; then
        drop_test_postgresql_schema "${way_schema}"
        way_schema=""
    fi
}

cleanup_all() {
    cleanup_story
    stop_test_postgresql
    if [ -d "${run_root}" ]; then
        rm -rf "${run_root}"
    fi
}
trap cleanup_all EXIT INT TERM

wait_ready() {
    url="$1"
    pid="$2"
    log="$3"
    ca_option="$4"
    attempt=0
    until curl --fail --silent ${ca_option} "${url}/healthz" >/dev/null; do
        attempt=$((attempt + 1))
        if ! kill -0 "${pid}" 2>/dev/null || [ "${attempt}" -ge 120 ]; then
            echo "service did not become ready at ${url}" >&2
            sed -n '1,240p' "${log}" >&2
            exit 1
        fi
        sleep 0.1
    done
}

story_number=0
failed_stories=""
for story_file in ${story_list}; do
    story_number=$((story_number + 1))
    pay_port=$((${GIZWAY_TEST_PORT_BASE:-18080} + (story_number - 1) * 6))
    way_port=$((pay_port + 1))
    fake_port=$((pay_port + 2))
    payment_port=$((pay_port + 3))
    risk_port=$((pay_port + 4))
    catalog_fake_port=$((pay_port + 5))
    for port in "${pay_port}" "${way_port}" "${fake_port}" "${payment_port}" "${risk_port}" "${catalog_fake_port}"; do
        assert_port_free "${port}"
    done

    pay_url="https://127.0.0.1:${pay_port}"
    way_url="http://127.0.0.1:${way_port}"
    fake_url="http://127.0.0.1:${fake_port}"
    payment_url="http://127.0.0.1:${payment_port}"
    risk_url="http://127.0.0.1:${risk_port}"
    catalog_fake_url="http://127.0.0.1:${catalog_fake_port}"
    story_dir="${run_root}/story-${story_number}"
    mkdir -p "${story_dir}"

    pay_schema="gizpay_story_$$_${story_number}"
    way_schema="gizway_story_$$_${story_number}"
    create_test_postgresql_schema "${pay_schema}"
    create_test_postgresql_schema "${way_schema}"
    pay_dsn="$(test_postgresql_schema_dsn "${pay_schema}")"
    way_dsn="$(test_postgresql_schema_dsn "${way_schema}")"

    "${run_root}/bin/fake-ai-provider" -address "127.0.0.1:${fake_port}" -credential story-provider-key >"${story_dir}/fake-ai.log" 2>&1 &
    fake_pid=$!
    "${run_root}/bin/fake-ai-provider" -address "127.0.0.1:${catalog_fake_port}" -credential story-catalog-provider-key -callback-secret story-ai-callback-secret >"${story_dir}/catalog-fake-ai.log" 2>&1 &
    catalog_fake_pid=$!
    "${run_root}/bin/fake-payment-provider" -address "127.0.0.1:${payment_port}" -callback-secret story-callback-secret -fixed-now 2026-08-12T12:00:00Z -callback-ca "${run_root}/pki/ca.crt" >"${story_dir}/fake-payment.log" 2>&1 &
    payment_pid=$!
    "${run_root}/bin/fake-risk-provider" -address "127.0.0.1:${risk_port}" -credential story-risk-key >"${story_dir}/fake-risk.log" 2>&1 &
    risk_pid=$!

    GIZPAY_PAYMENT_PROVIDER_CREDENTIAL=story-payment-key \
    GIZPAY_PAYMENT_CALLBACK_SECRET=story-callback-secret \
    GIZPAY_RISK_PROVIDER_CREDENTIAL=story-risk-key \
    "${run_root}/bin/gizpay" \
        -address "127.0.0.1:${pay_port}" \
        -postgres-dsn "${pay_dsn}" \
        -story-test-mode \
        -payment-provider-base-url "${payment_url}" \
        -checkout-base-url "${pay_url}" \
        -risk-provider-base-url "${risk_url}" \
        -tls-cert-file "${run_root}/pki/gizpay.crt" \
        -tls-key-file "${run_root}/pki/gizpay.key" \
        -gateway-client-ca-file "${run_root}/pki/ca.crt" \
        >"${story_dir}/gizpay.log" 2>&1 &
    pay_pid=$!
    wait_ready "${pay_url}" "${pay_pid}" "${story_dir}/gizpay.log" "--cacert ${run_root}/pki/ca.crt"

    # Story fixtures own all product rows. Register only the two mTLS Gateway
    # identities needed by the Internal API; the full product E2E bootstrap
    # would add an unrelated customer and make story isolation nondeterministic.
    "${run_root}/bin/e2e-bootstrap" -mode central-nodes -postgres-dsn "${pay_dsn}" -pki-dir "${run_root}/pki"

    GIZWAY_AI_PROVIDER_CREDENTIAL=story-provider-key \
    GIZWAY_AI_PROVIDER_CALLBACK_SECRET=story-ai-callback-secret \
    GIZPAY_INTERNAL_BASE_URL="${pay_url}" \
    GIZPAY_MTLS_CERT_FILE="${run_root}/pki/gizway-global.crt" \
    GIZPAY_MTLS_KEY_FILE="${run_root}/pki/gizway-global.key" \
    GIZPAY_MTLS_CA_FILE="${run_root}/pki/ca.crt" \
    GIZWAY_NODE_ID=gw-global-e2e \
    GIZWAY_REGION=global \
    "${run_root}/bin/gizway" \
        -address "127.0.0.1:${way_port}" \
        -postgres-dsn "${way_dsn}" \
        -story-test-mode \
        -ai-provider-base-url "${fake_url}" \
        -ai-provider-callback-url "${way_url}" \
        >"${story_dir}/gizway.log" 2>&1 &
    way_pid=$!
    wait_ready "${way_url}" "${way_pid}" "${story_dir}/gizway.log" ""

    echo "Running ${story_file}"
    if ! hurl --test \
        --cacert "${run_root}/pki/ca.crt" \
        --cert "${run_root}/pki/gizway-global.crt" \
        --key "${run_root}/pki/gizway-global.key" \
        --variable "pay_url=${pay_url}" \
        --variable "way_url=${way_url}" \
        --variable "fake_url=${fake_url}" \
        --variable "catalog_fake_url=${catalog_fake_url}" \
        --variable "payment_url=${payment_url}" \
        --variable "checkout_url=${pay_url}" \
        --variable "risk_url=${risk_url}" \
        --variable "active_user_one_key=gzs_story_user_active_1" \
        --variable "active_user_two_key=gzs_story_user_active_2" \
        --variable "suspended_user_key=gzs_story_user_suspended" \
        --variable "gateway_api_key=giz_story_user_active_1" \
        --variable "gateway_api_key_two=giz_story_user_active_2" \
        --variable "admin_api_key=gizadm_story_admin" \
        "${story_file}"; then
        sed -n '1,240p' "${story_dir}/gizpay.log" >&2
        sed -n '1,240p' "${story_dir}/gizway.log" >&2
        failed_stories="${failed_stories}
${story_file}"
    fi

    cleanup_story
    for port in "${pay_port}" "${way_port}" "${fake_port}" "${payment_port}" "${risk_port}" "${catalog_fake_port}"; do
        assert_port_free "${port}"
    done
done

if [ -n "${failed_stories}" ]; then
    printf 'API stories failed:%s\n' "${failed_stories}" >&2
    exit 1
fi
