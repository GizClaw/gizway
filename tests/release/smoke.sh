#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
output_dir="${RELEASE_OUTPUT_DIR:-$root/tmp/release/images}"
compose="$root/tests/release/compose.yaml"
oras="${ORAS:-oras}"
version=''; revision=''; build_time=''
# shellcheck disable=SC1090,SC1091
source "$output_dir/metadata.env"

export GIZPAY_IMAGE="ghcr.io/idy/gizway-gizpay:$version"
export GIZWAY_IMAGE="ghcr.io/idy/gizway-gateway:$version"
export WEB_IMAGE="ghcr.io/idy/gizway-web:$version"
RELEASE_FIXTURES_DIR="$(mktemp -d)"
export RELEASE_FIXTURES_DIR
project="gizway-release-$RANDOM-$$"
registry_container="${project}-registry"

cleanup() {
  docker compose --project-name "$project" --file "$compose" down --volumes --remove-orphans >/dev/null 2>&1 || true
  docker rm --force "$registry_container" >/dev/null 2>&1 || true
  rm -rf "$RELEASE_FIXTURES_DIR"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

docker run --detach --name "$registry_container" --publish 127.0.0.1::5000 \
  registry:3.0.0@sha256:6c5666b861f3505b116bb9aa9b25175e71210414bd010d92035ff64018f9457e >/dev/null
registry_port="$(docker port "$registry_container" 5000/tcp | awk -F: '{print $NF}')"
for _ in {1..30}; do
  curl --fail --silent "http://127.0.0.1:$registry_port/v2/" >/dev/null 2>&1 && break
  sleep 1
done

for key in gizpay gizway web; do
  "$oras" cp --from-oci-layout --to-plain-http --no-tty \
    "$output_dir/$key.oci.tar:$version" "127.0.0.1:$registry_port/$key:$version"
  docker pull "127.0.0.1:$registry_port/$key:$version" >/dev/null
done
export GIZPAY_IMAGE="127.0.0.1:$registry_port/gizpay:$version"
export GIZWAY_IMAGE="127.0.0.1:$registry_port/gizway:$version"
export WEB_IMAGE="127.0.0.1:$registry_port/web:$version"

openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$RELEASE_FIXTURES_DIR/machine.pem" >/dev/null 2>&1
chmod 600 "$RELEASE_FIXTURES_DIR/machine.pem"
printf '%s\n' 'release-smoke-hmac-secret' >"$RELEASE_FIXTURES_DIR/hmac"
printf '%s\n' 'release-smoke-action-signing-key' >"$RELEASE_FIXTURES_DIR/action-key"

printf '%s\n' \
  'version: 1' \
  'server:' '  name: gizpay.release.test' '  listen_address: 0.0.0.0:8081' '  shutdown_timeout: 5s' \
  'database:' '  dsn: postgres://postgres:postgres@postgres-gizpay:5432/gizpay?sslmode=disable' '  schema: public' \
  'authentication:' '  zitadel:' '    issuer: https://identity.release.test' '    jwks_url: https://identity.release.test/oauth/v2/keys' \
  '    human_audience: human' '    service_audience: service' '    management_client:' '      token_url: https://identity.release.test/oauth/v2/token' \
  '      subject: smoke' '      key_id: smoke' '      private_key_file: /release-fixtures/machine.pem' '    action_signing_key_file: /release-fixtures/action-key' \
  'subscription_keys:' '  hmac:' '    secret_file: /release-fixtures/hmac' \
  'payg_charges:' '  platform_fee_bps: 0' 'tls:' '  enabled: false' >"$RELEASE_FIXTURES_DIR/gizpay.yaml"

printf '%s\n' \
  'version: 1' \
  'server:' '  name: gizway.release.test' '  listen_address: 0.0.0.0:8080' '  shutdown_timeout: 5s' \
  'database:' '  dsn: postgres://postgres:postgres@postgres-gizway:5432/gizway?sslmode=disable' '  schema: gizway' \
  'authentication:' '  zitadel:' '    issuer: https://identity.release.test' '    jwks_url: https://identity.release.test/oauth/v2/keys' '    human_audience: human' \
  '  service_account:' '    token_url: https://identity.release.test/oauth/v2/token' '    subject: smoke' '    key_id: smoke' \
  '    private_key_file: /release-fixtures/machine.pem' '    audience: service' '    requested_scopes: [openid]' '    required_roles: [credit_check, charge]' \
  'subscription_keys:' '  hmac:' '    secret_file: /release-fixtures/hmac' \
  'gizpay:' '  service_dsn: http://gizpay:8081' \
  'bifrost:' '  config_store:' '    type: postgresql' '    dsn: postgres://postgres:postgres@postgres-gizway:5432/gizway?sslmode=disable' '    schema: bifrost_config' \
  '  log_store:' '    type: postgresql' '    dsn: postgres://postgres:postgres@postgres-gizway:5432/gizway?sslmode=disable' '    schema: bifrost_logs' \
  'tls:' '  enabled: false' >"$RELEASE_FIXTURES_DIR/gizway.yaml"
chmod 755 "$RELEASE_FIXTURES_DIR"
chmod 644 "$RELEASE_FIXTURES_DIR"/*

docker compose --project-name "$project" --file "$compose" up --detach

assert_health() {
  local service="$1" port="$2" expected="$3" host_port body
  host_port="$(docker compose --project-name "$project" --file "$compose" port "$service" "$port" | awk -F: '{print $NF}')"
  for _ in {1..90}; do
    if body="$(curl --fail --silent "http://127.0.0.1:$host_port/healthz" 2>/dev/null)"; then
      jq -e --arg service "$expected" --arg version "$version" --arg revision "$revision" --arg build_time "$build_time" \
        '.status == "healthy" and .service == $service and .version == $version and .revision == $revision and .build_time == $build_time
         and (.server_time | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$"))' <<<"$body" >/dev/null
      return
    fi
    sleep 1
  done
  docker compose --project-name "$project" --file "$compose" logs "$service" >&2
  return 1
}

assert_health gizpay 8081 gizpay
assert_health gizway 8080 gizway
assert_health web 3000 gizway-web

[[ "$(docker image inspect "$GIZPAY_IMAGE" --format '{{.Config.User}}')" == '65532:65532' ]]
[[ "$(docker image inspect "$GIZWAY_IMAGE" --format '{{.Config.User}}')" == '65532:65532' ]]
[[ "$(docker image inspect "$WEB_IMAGE" --format '{{.Config.User}}')" == 'node' ]]
docker run --rm --entrypoint /bin/sh "$GIZPAY_IMAGE" -c '! command -v go && ! command -v hurl && ! command -v fake-ai-provider'
docker run --rm --entrypoint /bin/sh "$GIZWAY_IMAGE" -c '! command -v go && ! command -v hurl && ! command -v e2e-bootstrap'
docker run --rm --entrypoint /bin/sh "$WEB_IMAGE" -c 'test ! -d /app/dist/standalone/node_modules/@playwright && test ! -d /app/dist/standalone/node_modules/typescript && ! command -v wrangler'

if docker run --rm "$GIZPAY_IMAGE" --config=/does/not/exist >/dev/null 2>&1; then
  printf 'gizpay unexpectedly accepted a missing configuration\n' >&2
  exit 1
fi

gizpay_container="$(docker compose --project-name "$project" --file "$compose" ps --quiet gizpay)"
docker kill --signal TERM "$gizpay_container" >/dev/null
[[ "$(docker wait "$gizpay_container")" == 0 ]]
printf 'release image smoke passed for %s (%s)\n' "$version" "$revision"
