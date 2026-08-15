#!/bin/sh
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
compose="${root}/tests/e2e/compose.yaml"
project="gizway-m03-sdk-$$"
fixture="$(mktemp "${TMPDIR:-/tmp}/gizway-m03-sdk.XXXXXX")"

cleanup() {
    status=$?
    docker compose --project-name "${project}" -f "${compose}" --profile '*' down --volumes --remove-orphans >/dev/null 2>&1 || true
    rm -f "${fixture}" || true
    return "${status}"
}
trap cleanup EXIT INT TERM

docker compose --project-name "${project}" -f "${compose}" up --build --detach
bootstrap_id="$(docker compose --project-name "${project}" -f "${compose}" ps --all --quiet bootstrap-milestone-03)"
docker wait "${bootstrap_id}" >/dev/null
if [ "$(docker inspect --format '{{.State.ExitCode}}' "${bootstrap_id}")" -ne 0 ]; then
    docker compose --project-name "${project}" -f "${compose}" logs --tail 200 --no-log-prefix
    exit 1
fi

docker run --rm -v "${project}_fixtures:/fixtures:ro" busybox:1.37.0 cat /fixtures/m03.vars >"${fixture}"
. "${fixture}"

pay_address="$(docker compose --project-name "${project}" -f "${compose}" port gizpay 8081)"
cn_address="$(docker compose --project-name "${project}" -f "${compose}" port gizway-cn 8080)"
global_address="$(docker compose --project-name "${project}" -f "${compose}" port gizway-global 8080)"
global_db_address="$(docker compose --project-name "${project}" -f "${compose}" port postgres-global 5432)"
pay_db_address="$(docker compose --project-name "${project}" -f "${compose}" port postgres-gizpay 5432)"
provider_address="$(docker compose --project-name "${project}" -f "${compose}" port fake-provider-global 19000)"

export M03_PAY_URL="http://${pay_address}"
export M03_CN_URL="http://${cn_address}"
export M03_GLOBAL_URL="http://${global_address}"
export M03_GLOBAL_DSN="postgres://postgres:postgres@${global_db_address}/gizway?sslmode=disable&search_path=gizway"
export M03_PAY_DSN="postgres://postgres:postgres@${pay_db_address}/gizpay?sslmode=disable"
export M03_PROVIDER_URL="http://${provider_address}"
export M03_SUBSCRIPTION_KEY="${raw_subscription_key}"
export M03_REVOKED_SUBSCRIPTION_KEY="${revoked_subscription_key}"
export M03_GLOBAL_MODEL="${seeded_model_name}"
export M03_CN_MODEL="${seeded_model_name}"
export M03_ACCOUNT_ID="${account_id}"
export M03_HUMAN_TOKEN="${human_token}"
export M03_ZERO_PRICE_MODEL="${zero_price_model}"
export M03_INACTIVE_MODEL="story-text-inactive"
export M03_PROVIDER_KEY_ID="${global_provider_key_id}"

cd "${root}/tests/sdk"
go test -count=1 ./...
