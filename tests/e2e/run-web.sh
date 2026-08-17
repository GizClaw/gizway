#!/bin/sh
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
compose="${root}/tests/e2e/compose.yaml"
web_compose="${root}/tests/e2e/compose.web.yaml"
project="gizway-m04-web-$$"
fixture="$(mktemp "${TMPDIR:-/tmp}/gizway-m04-web.XXXXXX")"

cleanup() {
    status=$?
    if [ "${M04_KEEP_E2E_STACK:-0}" = 1 ]; then
        echo "M04 E2E stack retained as ${project}" >&2
    else
        docker compose --project-name "${project}" -f "${compose}" -f "${web_compose}" --profile '*' down --volumes --remove-orphans >/dev/null 2>&1 || true
    fi
    rm -f "${fixture}" || true
    return "${status}"
}
trap cleanup EXIT INT TERM

if ! docker compose --project-name "${project}" -f "${compose}" -f "${web_compose}" --profile powersync --profile web up --build --detach; then
    docker compose --project-name "${project}" -f "${compose}" -f "${web_compose}" --profile '*' logs --tail 200 --no-log-prefix
    exit 1
fi
bootstrap_id="$(docker compose --project-name "${project}" -f "${compose}" -f "${web_compose}" ps --all --quiet bootstrap-milestone-03)"
docker wait "${bootstrap_id}" >/dev/null
if [ "$(docker inspect --format '{{.State.ExitCode}}' "${bootstrap_id}")" -ne 0 ]; then
    docker compose --project-name "${project}" -f "${compose}" -f "${web_compose}" --profile '*' logs --tail 200 --no-log-prefix
    exit 1
fi

ready=false
for attempt in $(seq 1 60); do
    if curl --fail --silent http://global.localhost:3000/ >/dev/null; then
        ready=true
        break
    fi
    sleep 1
done
if [ "${ready}" != true ]; then
    docker compose --project-name "${project}" -f "${compose}" -f "${web_compose}" --profile '*' logs --tail 200 --no-log-prefix
    exit 1
fi

docker compose --project-name "${project}" -f "${compose}" -f "${web_compose}" exec --no-TTY gizway-global cat /fixtures/m03.vars >"${fixture}"
. "${fixture}"

cd "${root}/web/apps/gizway"
M04_REAL_E2E=1 \
M04_E2E_COMPOSE_PROJECT="${project}" \
M04_WEB_EXTERNAL=1 \
M04_WEB_PORT=3000 \
M04_E2E_USERNAME="${human_username}" \
M04_E2E_PASSWORD="${human_password}" \
M04_E2E_NEW_USERNAME="${web_first_login_username}" \
M04_E2E_NEW_PASSWORD="${web_first_login_password}" \
npm run test:e2e -- e2e/real-auth-and-sync.spec.ts
