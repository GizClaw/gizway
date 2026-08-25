#!/bin/sh
set -u

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
compose="$root/tests/e2e/compose.yaml"
mode="${1:-}"
case "$mode" in all|api|sdk|powersync|gizway-sdk) ;; *) echo "usage: $0 all|api|sdk|powersync|gizway-sdk" >&2; exit 2;; esac
for tls_input in TLS_CERT_FILE TLS_KEY_FILE; do
  eval "tls_path=\${$tls_input:-}"
  if [ -z "$tls_path" ] || [ ! -r "$tls_path" ]; then
    echo "$tls_input must point to a readable external E2E TLS file" >&2
    exit 2
  fi
done
for command in docker openssl curl; do
  command -v "$command" >/dev/null 2>&1 || { echo "$command is required" >&2; exit 2; }
done
openssl x509 -in "$TLS_CERT_FILE" -noout -checkend 60 >/dev/null || { echo "TLS certificate must remain valid for the E2E run" >&2; exit 2; }
certificate_sans="$(openssl x509 -in "$TLS_CERT_FILE" -noout -text)"
for host in global.e2e.gizclaw.test cn.e2e.gizclaw.test identity.e2e.gizclaw.test pay.e2e.gizclaw.test; do
  printf '%s\n' "$certificate_sans" | grep -F "DNS:$host" >/dev/null || { echo "TLS certificate lacks SAN $host" >&2; exit 2; }
done
certificate_key="$(openssl x509 -in "$TLS_CERT_FILE" -pubkey -noout | openssl pkey -pubin -outform DER | openssl dgst -sha256)"
private_key="$(openssl pkey -in "$TLS_KEY_FILE" -pubout -outform DER | openssl dgst -sha256)"
[ "$certificate_key" = "$private_key" ] || { echo "TLS private key does not match certificate" >&2; exit 2; }
tls_spki="$(openssl x509 -in "$TLS_CERT_FILE" -pubkey -noout | openssl pkey -pubin -outform DER | openssl dgst -sha256 -binary | openssl base64 -A)"
project="gizway-e2e-${mode}-$$"
global_entry_port="${GIZWAY_E2E_GLOBAL_PORT:-3000}"
cn_entry_port="${GIZWAY_E2E_CN_PORT:-3001}"
export GIZWAY_E2E_GLOBAL_PORT="$global_entry_port" GIZWAY_E2E_CN_PORT="$cn_entry_port"
fixture="$(mktemp "${TMPDIR:-/tmp}/gizway-e2e.XXXXXX")"
results="$(mktemp "${TMPDIR:-/tmp}/gizway-e2e-results.XXXXXX")"
postgres_tls_directory="$(mktemp -d "${TMPDIR:-/tmp}/gizway-postgres-tls.XXXXXX")"

cleanup() {
  status=$?
  if [ "${GIZWAY_KEEP_E2E_STACK:-0}" = 1 ]; then
    echo "E2E stack retained as $project" >&2
  else
    docker compose --project-name "$project" -f "$compose" --profile '*' down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -f "$fixture" "$results"
  rm -rf "$postgres_tls_directory"
  return "$status"
}
trap cleanup EXIT INT TERM

openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=postgres-gizway-e2e' \
  -addext 'subjectAltName=DNS:postgres-gizpay,DNS:postgres-global,DNS:postgres-cn,DNS:postgres-powersync' \
  -keyout "$postgres_tls_directory/server.key" -out "$postgres_tls_directory/server.crt" >/dev/null 2>&1
chmod 0600 "$postgres_tls_directory/server.key"
chmod 0644 "$postgres_tls_directory/server.crt"
POSTGRES_TLS_CERT_FILE="$postgres_tls_directory/server.crt"
POSTGRES_TLS_KEY_FILE="$postgres_tls_directory/server.key"
export POSTGRES_TLS_CERT_FILE POSTGRES_TLS_KEY_FILE

fail_with_logs() {
  docker compose --project-name "$project" -f "$compose" --profile '*' logs --tail 250 --no-log-prefix >&2 || true
  exit 1
}

wait_job() {
  service="$1"
  container="$(docker compose --project-name "$project" -f "$compose" ps --all --quiet "$service")"
  [ -n "$container" ] || return 1
  docker wait "$container" >/dev/null
  [ "$(docker inspect --format '{{.State.ExitCode}}' "$container")" -eq 0 ]
}

wait_healthy() {
  service="$1"
  container="$(docker compose --project-name "$project" -f "$compose" ps --quiet "$service")"
  [ -n "$container" ] || return 1
  attempt=0
  while [ "$attempt" -lt 120 ]; do
    health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}missing{{end}}' "$container")"
    [ "$health" = healthy ] && return 0
    [ "$health" = unhealthy ] && return 1
    attempt=$((attempt + 1))
    sleep 1
  done
  return 1
}

wait_postgres_tls_role() {
  service="$1"
  database="$2"
  role="$3"
  case "$role" in
    *[!a-z0-9_]*)
      echo "invalid PostgreSQL role name for TLS assertion" >&2
      return 1
      ;;
  esac
  attempt=0
  while [ "$attempt" -lt 60 ]; do
    connected="$(docker compose --project-name "$project" -f "$compose" exec -T "$service" \
      psql -U postgres -d "$database" -Atq \
      -c "SELECT EXISTS (SELECT 1 FROM pg_stat_activity AS a JOIN pg_stat_ssl AS s USING (pid) WHERE a.usename = '$role' AND s.ssl);" 2>/dev/null || true)"
    [ "$connected" = t ] && return 0
    attempt=$((attempt + 1))
    sleep 1
  done
  echo "$role did not establish a TLS connection through $service" >&2
  return 1
}

routed_fingerprint() {
  host="$1"
  port="$2"
  path="$3"
  curl --noproxy '*' --silent --show-error --cacert "$TLS_CERT_FILE" \
    --resolve "$host:$port:127.0.0.1" --output /dev/null \
    --write-out '%{http_code}\t%{content_type}' "https://$host:$port$path"
}

direct_fingerprint() {
  url="$1"
  host="$2"
  port="$3"
  docker run --rm --network "${project}_default" curlimages/curl:8.15.0 \
    --silent --show-error --header "Host: $host:$port" \
    --header "X-Forwarded-Host: $host:$port" --header "X-Forwarded-Port: $port" \
    --header 'X-Forwarded-Proto: https' \
    --output /dev/null --write-out '%{http_code}\t%{content_type}' "$url"
}

assert_same_route_fingerprint() {
  name="$1"
  direct_url="$2"
  host="$3"
  port="$4"
  path="$5"
  expected="$(direct_fingerprint "$direct_url" "$host" "$port")"
  actual="$(routed_fingerprint "$host" "$port" "$path")"
  if [ "$actual" != "$expected" ]; then
    echo "$name response fingerprint did not match the intended direct upstream: expected '$expected', got '$actual'" >&2
    return 1
  fi
}

run_case() {
  name="$1"; shift
  if "$@"; then printf '%s\tPASS\n' "$name" >>"$results"; else status=$?; printf '%s\tFAIL(%s)\n' "$name" "$status" >>"$results"; fi
}

docker compose --project-name "$project" -f "$compose" up --build --detach || fail_with_logs
wait_job business-resources || fail_with_logs
for job in gizpay-init gizway-global-init gizway-cn-init business-resources; do
  docker compose --project-name "$project" -f "$compose" run --rm --no-deps "$job" || fail_with_logs
done
for entry in entry-central entry-global entry-cn; do
	wait_healthy "$entry" || fail_with_logs
done
wait_postgres_tls_role postgres-gizpay gizpay powersync_pay_source || fail_with_logs
wait_postgres_tls_role postgres-global gizway powersync_global_source || fail_with_logs
wait_postgres_tls_role postgres-cn gizway powersync_cn_source || fail_with_logs
wait_postgres_tls_role postgres-powersync postgres powersync_pay_storage || fail_with_logs
wait_postgres_tls_role postgres-powersync postgres powersync_global_storage || fail_with_logs
wait_postgres_tls_role postgres-powersync postgres powersync_cn_storage || fail_with_logs

assert_same_route_fingerprint login-exact http://zitadel-login:3000/ui/v2/login identity.e2e.gizclaw.test 18080 /ui/v2/login || fail_with_logs
assert_same_route_fingerprint login-descendant http://zitadel-login:3000/ui/v2/login/healthy identity.e2e.gizclaw.test 18080 /ui/v2/login/healthy || fail_with_logs
assert_same_route_fingerprint login-lookalike http://zitadel:8080/ui/v2/loginx identity.e2e.gizclaw.test 18080 /ui/v2/loginx || fail_with_logs
assert_same_route_fingerprint pay-sync-exact http://powersync-pay:8080/ global.e2e.gizclaw.test "$global_entry_port" /_sync/gizpay || fail_with_logs
assert_same_route_fingerprint pay-sync-descendant 'http://powersync-pay:8080/sync/stream?probe=route' global.e2e.gizclaw.test "$global_entry_port" '/_sync/gizpay/sync/stream?probe=route' || fail_with_logs
assert_same_route_fingerprint way-sync-exact http://powersync-global:8080/ global.e2e.gizclaw.test "$global_entry_port" /_sync/gizway || fail_with_logs
for path in / /marketplace /_sync/gizpayx /_sync/gizwayx; do
  [ "$(routed_fingerprint global.e2e.gizclaw.test "$global_entry_port" "$path" | cut -f1)" = 404 ] || { echo "API-only Entry routed unexpected path $path" >&2; fail_with_logs; }
done
expected_auth_status=''
expected_auth_code=''
for cors_origin in http://127.0.0.1:4173 https://consumer.example.test; do
  cors_headers="$(mktemp "${TMPDIR:-/tmp}/gizway-cors.XXXXXX")"
  cors_body="$(mktemp "${TMPDIR:-/tmp}/gizway-cors-body.XXXXXX")"
  curl --noproxy '*' --silent --show-error --cacert "$TLS_CERT_FILE" --resolve "global.e2e.gizclaw.test:$global_entry_port:127.0.0.1" \
    --request OPTIONS --header "Origin: $cors_origin" --header 'Access-Control-Request-Method: POST' \
    --header 'Access-Control-Request-Headers: authorization,content-type,x-user-agent' --dump-header "$cors_headers" --output /dev/null \
    "https://global.e2e.gizclaw.test:$global_entry_port/auth/runtime-config" || fail_with_logs
  tr -d '\r' <"$cors_headers" >"$cors_headers.normalized"
  grep -Eiq '^Access-Control-Allow-Origin: \*$' "$cors_headers.normalized" || { echo "CORS preflight for $cors_origin omitted wildcard origin" >&2; fail_with_logs; }
  grep -Eiq '^Access-Control-Allow-Methods: .*GET.*POST.*PUT.*PATCH.*DELETE.*OPTIONS' "$cors_headers.normalized" || { echo "CORS preflight for $cors_origin omitted fixed methods" >&2; fail_with_logs; }
  grep -Eiq '^Access-Control-Allow-Headers: .*X-User-Agent' "$cors_headers.normalized" || { echo "CORS preflight for $cors_origin omitted X-User-Agent allowance" >&2; fail_with_logs; }
  if grep -Eiq '^Access-Control-Allow-Credentials:|^Vary:.*Origin' "$cors_headers.normalized"; then echo "CORS preflight for $cors_origin enabled credentials or varied by Origin" >&2; fail_with_logs; fi

  auth_status="$(curl --noproxy '*' --silent --show-error --cacert "$TLS_CERT_FILE" --resolve "global.e2e.gizclaw.test:$global_entry_port:127.0.0.1" \
    --header "Origin: $cors_origin" --dump-header "$cors_headers" --output "$cors_body" --write-out '%{http_code}' \
    "https://global.e2e.gizclaw.test:$global_entry_port/openai/v1/models")" || fail_with_logs
  tr -d '\r' <"$cors_headers" >"$cors_headers.normalized"
  grep -Eiq '^Access-Control-Allow-Origin: \*$' "$cors_headers.normalized" || { echo "actual CORS response for $cors_origin omitted wildcard origin" >&2; fail_with_logs; }
  grep -Eiq '^Access-Control-Expose-Headers: Retry-After$' "$cors_headers.normalized" || { echo "actual CORS response for $cors_origin omitted Retry-After exposure" >&2; fail_with_logs; }
  if grep -Eiq '^Access-Control-Allow-Credentials:|^Vary:.*Origin' "$cors_headers.normalized"; then echo "actual CORS response for $cors_origin enabled credentials or varied by Origin" >&2; fail_with_logs; fi
  if [ -z "$expected_auth_status" ]; then
    [ "$auth_status" -ge 400 ] || { echo "unauthenticated API request unexpectedly succeeded with $auth_status" >&2; fail_with_logs; }
    expected_auth_status="$auth_status"
    expected_auth_code="$(sed -n 's/.*"code"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$cors_body")"
    [ -n "$expected_auth_code" ] || { echo 'unauthenticated API response omitted its error code' >&2; fail_with_logs; }
  elif [ "$auth_status" != "$expected_auth_status" ] || ! grep -Eq '"code"[[:space:]]*:[[:space:]]*"'"$expected_auth_code"'"' "$cors_body"; then
    echo "API authorization result changed with Origin: expected $expected_auth_status" >&2
    fail_with_logs
  fi
  rm -f "$cors_headers" "$cors_headers.normalized" "$cors_body"
done
if curl --noproxy '*' --fail --silent --show-error --cacert "$TLS_CERT_FILE" \
  --resolve "invalid.e2e.gizclaw.test:$global_entry_port:127.0.0.1" "https://invalid.e2e.gizclaw.test:$global_entry_port/healthz" >/dev/null 2>&1; then
  echo "Entry accepted a certificate/SNI mismatch" >&2
  fail_with_logs
fi
if curl --noproxy '*' --insecure --fail --silent --show-error \
  --resolve "invalid.e2e.gizclaw.test:$global_entry_port:127.0.0.1" "https://invalid.e2e.gizclaw.test:$global_entry_port/healthz" >/dev/null 2>&1; then
  echo "Entry routed an unknown host" >&2
  fail_with_logs
fi

docker run --rm -v "${project}_fixtures:/fixtures:ro" busybox:1.37.0 cat /fixtures/m03.vars >"$fixture"
# shellcheck disable=SC1090
. "$fixture"

run_api() {
  "$root/scripts/test-unit/test-unit-api-openapi.sh" && "$root/scripts/test-unit/test-unit-api-contracts.sh" && "$root/scripts/test-unit/check-e2e-api-seed.sh" || return
  admin_key="$(docker run --rm -v "${project}_fixtures:/fixtures:ro" busybox:1.37.0 cat /fixtures/admin-key)"
  stories="$(find "$root/tests/api/stories/24-milestone-03" "$root/tests/api/stories/25-milestone-04" "$root/tests/api/stories/26-admin" -type f -name '*.hurl' -print | sort)"
  for story in $stories; do
    docker compose --project-name "$project" -f "$compose" --profile test run --rm --no-deps hurl-api \
      --cacert /tls/tls.crt \
      --test --variables-file /fixtures/m03.vars --secret "admin_key=$admin_key" \
      --variable pay_url=https://global.e2e.gizclaw.test:8443 \
      --variable way_url=https://cn.e2e.gizclaw.test:8443 \
      --variable pay_internal_url=http://gizpay:8081 \
      --variable way_internal_url=http://gizway-cn:8080 \
      --variable global_url=https://global.e2e.gizclaw.test:8443 \
      --variable cn_url=https://cn.e2e.gizclaw.test:8443 "/workspace/${story#"$root"/}" || return
  done
}

# Variables below are loaded from the generated fixture file.
# shellcheck disable=SC2154
run_sdk() {
  docker compose --project-name "$project" -f "$compose" --profile test run --rm --no-deps \
    -e M03_PAY_URL=https://global.e2e.gizclaw.test:8443 \
    -e M03_CN_URL=https://cn.e2e.gizclaw.test:8443 \
    -e M03_GLOBAL_URL=https://global.e2e.gizclaw.test:8443 \
    -e 'M03_GLOBAL_DSN=postgres://postgres:postgres@postgres-global:5432/gizway?sslmode=disable&search_path=gizway' \
    -e 'M03_PAY_DSN=postgres://postgres:postgres@postgres-gizpay:5432/gizpay?sslmode=disable' \
    -e M03_PROVIDER_URL=http://fake-provider-global:19000 \
    -e M03_SUBSCRIPTION_KEY="$raw_subscription_key" -e M03_REVOKED_SUBSCRIPTION_KEY="$revoked_subscription_key" \
    -e M03_GLOBAL_MODEL="$seeded_model_name" -e M03_CN_MODEL="$seeded_model_name" -e M03_ACCOUNT_ID="$account_id" \
    -e M03_HUMAN_TOKEN="$human_token" -e M03_ZERO_PRICE_MODEL="$zero_price_model" -e M03_INACTIVE_MODEL=story-text-inactive \
    -e M03_PROVIDER_KEY_ID="$global_provider_key_id" sdk-test
}

# shellcheck disable=SC2154
run_powersync() {
  docker compose --project-name "$project" -f "$compose" --profile test run --rm --no-deps \
    -e M03_POWERSYNC_GIZPAY_ENDPOINT=https://global.e2e.gizclaw.test:8443/_sync/gizpay \
    -e M03_POWERSYNC_CN_ENDPOINT=https://cn.e2e.gizclaw.test:8443/_sync/gizway \
    -e M03_POWERSYNC_GLOBAL_ENDPOINT=https://global.e2e.gizclaw.test:8443/_sync/gizway \
    -e M03_POWERSYNC_TOKEN="$human_token" -e M03_POWERSYNC_TOKEN_TWO="$human_token_two" \
    -e M03_POWERSYNC_INVALID_AUDIENCE_TOKEN="$wrong_audience_admin_token" \
    -e M04_POWERSYNC_CN_CATALOG_TOKEN="$cn_catalog_token" -e M04_POWERSYNC_GLOBAL_CATALOG_TOKEN="$global_catalog_token" \
    -e M03_HUMAN_TOKEN="$human_token" -e M03_PAY_URL=https://global.e2e.gizclaw.test:8443 \
    -e M03_SUBSCRIPTION_KEY="$raw_subscription_key" \
    -e M03_GLOBAL_URL=https://global.e2e.gizclaw.test:8443 -e M03_CN_URL=https://cn.e2e.gizclaw.test:8443 powersync-test
}

# shellcheck disable=SC2154
run_browser_sdk() {
  (cd "$root/sdk/web" && M04_REAL_E2E=1 M04_E2E_COMPOSE_PROJECT="$project" \
    M04_TLS_SPKI="$tls_spki" \
    M04_ENTRY_PORT="$global_entry_port" M04_ENTRY_CN_PORT="$cn_entry_port" M04_BROWSER_CLIENT_ID="$browser_client_id" \
    M04_E2E_USERNAME="$human_username" M04_E2E_PASSWORD="$human_password" \
    npm run test:e2e -- e2e/browser-client.spec.ts)
}

case "$mode" in
  all) run_case api run_api; run_case sdk run_sdk; run_case powersync run_powersync; run_case gizway-sdk run_browser_sdk ;;
  api) run_case api run_api ;;
  sdk) run_case sdk run_sdk ;;
  powersync) run_case powersync run_powersync ;;
  gizway-sdk) run_case gizway-sdk run_browser_sdk ;;
esac
cat "$results"
grep -q 'FAIL' "$results" && exit 1
exit 0
