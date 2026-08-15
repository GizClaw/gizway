#!/bin/sh
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
compose="${root}/tests/e2e/compose.yaml"
project="gizway-m03-powersync-$$"
fixture="$(mktemp "${TMPDIR:-/tmp}/gizway-m03-powersync.XXXXXX")"

cleanup() {
    status=$?
    docker compose --project-name "${project}" -f "${compose}" --profile '*' down --volumes --remove-orphans >/dev/null 2>&1 || true
    rm -f "${fixture}" || true
    return "${status}"
}
trap cleanup EXIT INT TERM

cd "${root}/tests/powersync"
npm run typecheck
npm test

docker compose --project-name "${project}" -f "${compose}" --profile powersync up --build --detach
bootstrap_id="$(docker compose --project-name "${project}" -f "${compose}" ps --all --quiet bootstrap-milestone-03)"
docker wait "${bootstrap_id}" >/dev/null
if [ "$(docker inspect --format '{{.State.ExitCode}}' "${bootstrap_id}")" -ne 0 ]; then
    docker compose --project-name "${project}" -f "${compose}" --profile powersync logs --tail 200 --no-log-prefix
    exit 1
fi

docker run --rm -v "${project}_fixtures:/fixtures:ro" busybox:1.37.0 cat /fixtures/m03.vars >"${fixture}"
. "${fixture}"

pay_address="$(docker compose --project-name "${project}" -f "${compose}" port gizpay 8081)"
cn_address="$(docker compose --project-name "${project}" -f "${compose}" port gizway-cn 8080)"
global_address="$(docker compose --project-name "${project}" -f "${compose}" port gizway-global 8080)"
ps_pay_address="$(docker compose --project-name "${project}" -f "${compose}" port powersync-gizpay 8080)"
ps_cn_address="$(docker compose --project-name "${project}" -f "${compose}" port powersync-cn 8080)"
ps_global_address="$(docker compose --project-name "${project}" -f "${compose}" port powersync-global 8080)"

for address in "${ps_pay_address}" "${ps_cn_address}" "${ps_global_address}"; do
    ready=false
    for attempt in $(seq 1 60); do
        if curl --fail --silent "http://${address}/probes/readiness" >/dev/null; then
            ready=true
            break
        fi
        sleep 1
    done
    if [ "${ready}" != true ]; then
        docker compose --project-name "${project}" -f "${compose}" --profile powersync logs --tail 200 --no-log-prefix
        exit 1
    fi
done

export M03_POWERSYNC_GIZPAY_ENDPOINT="http://${ps_pay_address}"
export M03_POWERSYNC_CN_ENDPOINT="http://${ps_cn_address}"
export M03_POWERSYNC_GLOBAL_ENDPOINT="http://${ps_global_address}"
export M03_POWERSYNC_TOKEN="${human_token}"
export M03_POWERSYNC_TOKEN_TWO="${human_token_two}"
export M03_POWERSYNC_INVALID_AUDIENCE_TOKEN="${wrong_audience_admin_token}"
export M03_HUMAN_TOKEN="${human_token}"
export M03_PAY_URL="http://${pay_address}"
export M03_GLOBAL_URL="http://${global_address}"

# Produce real owner-visible financial and usage rows in both regions before
# connecting the clients. Empty result sets cannot prove row ownership or
# regional isolation.
for address in "${global_address}" "${cn_address}"; do
    curl --fail --silent --show-error \
        -H "Authorization: Bearer ${raw_subscription_key}" \
        -H 'Content-Type: application/json' \
        --data "{\"model\":\"${seeded_model_name}\",\"messages\":[{\"role\":\"user\",\"content\":\"powersync acceptance\"}]}" \
        "http://${address}/v1/chat/completions" >/dev/null
done

npm test
