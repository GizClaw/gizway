#!/usr/bin/env bash
set -euo pipefail
root="$(git rev-parse --show-toplevel)"; catalog="$root/release/images.json"
output_dir="${RELEASE_OUTPUT_DIR:-$root/tmp/release/images}"; compose="$root/tests/release/compose.yaml"; oras="${ORAS:-oras}"
version=''; revision=''
# shellcheck disable=SC1090,SC1091
source "$output_dir/metadata.env"
temporary="$(mktemp -d)"; project="gizway-release-$RANDOM-$$"; registry_container="${project}-registry"
cleanup() { docker compose --project-name "$project" --file "$compose" down --volumes --remove-orphans >/dev/null 2>&1 || true; docker rm --force "$registry_container" >/dev/null 2>&1 || true; rm -rf "$temporary"; }
trap cleanup EXIT

openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=postgres.test' -addext 'subjectAltName=DNS:postgres.test' \
  -keyout "$temporary/postgres-ca.key" -out "$temporary/postgres-ca.crt" >/dev/null 2>&1
printf '%s\n' 'not a certificate' >"$temporary/malformed-ca.pem"

docker run --detach --name "$registry_container" --publish 127.0.0.1::5000 registry:3.0.0@sha256:6c5666b861f3505b116bb9aa9b25175e71210414bd010d92035ff64018f9457e >/dev/null
registry_port="$(docker port "$registry_container" 5000/tcp | awk -F: '{print $NF}')"
for _ in {1..30}; do curl --fail --silent "http://127.0.0.1:$registry_port/v2/" >/dev/null 2>&1 && break; sleep 1; done

while IFS=$'\t' read -r key _image; do
  local_ref="127.0.0.1:$registry_port/$key:$version"
  "$oras" cp --from-oci-layout --to-plain-http --no-tty "$output_dir/$key.oci.tar:$version" "$local_ref"
  docker pull "$local_ref" >/dev/null
  variable="$(tr '[:lower:]-' '[:upper:]_' <<<"${key}_IMAGE")"
  export "$variable=$local_ref"
done < <(jq -r '.images[] | [.key,.image] | @tsv' "$catalog")

docker compose --project-name "$project" --file "$compose" create >/dev/null
[[ "$(docker compose --project-name "$project" --file "$compose" ps --all --quiet | wc -l | tr -d ' ')" == 7 ]]
while IFS=$'\t' read -r key _ expected; do
  container="$(docker compose --project-name "$project" --file "$compose" ps --all --quiet "$key")"
  [[ -n "$container" ]]
  docker inspect --format '{{json .Config.Entrypoint}} {{json .Config.Cmd}}' "$container" | grep -F "$expected" >/dev/null
done < <(jq -r '.images[] | [.key,.image,.command] | @tsv' "$catalog")

entry_env=(
  -e PROFILE=global -e TLS_CERT_FILE=/etc/hosts -e TLS_KEY_FILE=/etc/hosts
  -e GLOBAL_HOST=global.e2e.gizclaw.test -e GIZPAY_UPSTREAM=http://gizpay:8081
  -e GIZWAY_UPSTREAM=http://gizway:8080 -e WEB_UPSTREAM=http://web:8080
  -e POWERSYNC_PAY_UPSTREAM=http://powersync-pay:8080 -e POWERSYNC_GIZWAY_UPSTREAM=http://powersync-global:8080
)
powersync_common_env=(
  -e POWERSYNC_CONFIG_PATH=/etc/gizway/powersync/pay/service.yaml
  -e PS_SOURCE_URI=postgres://source:source@postgres.test:5432/source
  -e PS_STORAGE_URI=postgres://storage:storage@postgres.test:5432/storage
  -e PS_JWKS_URI=https://identity.test/oauth/v2/keys -e PS_AUDIENCE=test-audience
)
powersync_disable_env=(
  "${powersync_common_env[@]}"
  -e PS_SOURCE_SSL_MODE=disable -e PS_STORAGE_SSL_MODE=disable
)
powersync_verify_env=(
  "${powersync_common_env[@]}"
  -e PS_SOURCE_SSL_MODE=verify-full -e PS_SOURCE_CA_FILE=/run/secrets/postgres-ca.crt
  -e PS_STORAGE_SSL_MODE=verify-full -e PS_STORAGE_CA_FILE=/run/secrets/postgres-ca.crt
  -v "$temporary/postgres-ca.crt:/run/secrets/postgres-ca.crt:ro"
)
assert_native_entrypoint_executes() {
  local key="$1" image="$2" status=0
  if [[ "$key" == entry ]]; then
    timeout 10 docker run --rm "${entry_env[@]}" "$image" --help >"$temporary/$key.log" 2>&1 || status=$?
  elif [[ "$key" == powersync ]]; then
    timeout 10 docker run --rm "${powersync_disable_env[@]}" "$image" --help >"$temporary/$key.log" 2>&1 || status=$?
  else
    timeout 10 docker run --rm "$image" --help >"$temporary/$key.log" 2>&1 || status=$?
  fi
  if [[ "$key" == zitadel-login && "$status" == 124 ]] && grep -F 'Ready' "$temporary/$key.log" >/dev/null; then
    return 0
  fi
  if [[ "$key" =~ ^giz(pay|way)$ && "$status" == 1 ]] && grep -Fx 'unsupported command "--help": want serve or init' "$temporary/$key.log" >/dev/null; then
    return 0
  fi
  if [[ "$status" != 0 ]]; then
    printf '%s native entrypoint did not complete its probe (status %s)\n' "$key" "$status" >&2
    cat "$temporary/$key.log" >&2
    return 1
  fi
}
while IFS=$'\t' read -r key _image; do
  variable="$(tr '[:lower:]-' '[:upper:]_' <<<"${key}_IMAGE")"
  assert_native_entrypoint_executes "$key" "${!variable}"
done < <(jq -r '.images[] | [.key,.image] | @tsv' "$catalog")

docker cp "$(docker compose --project-name "$project" --file "$compose" ps --all --quiet web):/etc/caddy/Caddyfile" "$temporary/Caddyfile"
docker cp "$(docker compose --project-name "$project" --file "$compose" ps --all --quiet entry):/etc/gizway" "$temporary/entry"
docker cp "$(docker compose --project-name "$project" --file "$compose" ps --all --quiet powersync):/etc/gizway/powersync" "$temporary/powersync"
grep -F 'try_files {path} /index.html' "$temporary/Caddyfile" >/dev/null
# shellcheck disable=SC2016
grep -F 'PathPrefix(`/openai/`)' "$temporary/entry/routes-global.yml.template" >/dev/null
# shellcheck disable=SC2016
grep -F 'Path(`/ui/v2/login`) || PathPrefix(`/ui/v2/login/`)' "$temporary/entry/routes-central.yml.template" >/dev/null
# shellcheck disable=SC2016
grep -F 'Path(`/_sync/gizpay`) || PathPrefix(`/_sync/gizpay/`)' "$temporary/entry/routes-central.yml.template" >/dev/null
for routes in "$temporary/entry/routes-global.yml.template" "$temporary/entry/routes-cn.yml.template"; do
  # shellcheck disable=SC2016
  grep -F 'Path(`/_sync/gizpay`) || PathPrefix(`/_sync/gizpay/`)' "$routes" >/dev/null
  # shellcheck disable=SC2016
  grep -F 'Path(`/_sync/gizway`) || PathPrefix(`/_sync/gizway/`)' "$routes" >/dev/null
done
test -f "$temporary/powersync/pay/service.yaml" -a -f "$temporary/powersync/global/service.yaml" -a -f "$temporary/powersync/cn/service.yaml"

if docker run --rm "$GIZPAY_IMAGE" init --config=/missing >/dev/null 2>&1; then echo 'gizpay init accepted missing config' >&2; exit 1; fi
if docker run --rm "$GIZWAY_IMAGE" init --config=/missing >/dev/null 2>&1; then echo 'gizway init accepted missing config' >&2; exit 1; fi
if docker run --rm "$ENTRY_IMAGE" >/dev/null 2>&1; then echo 'entry accepted missing runtime inputs' >&2; exit 1; fi
if docker run --rm "${entry_env[@]}" -e 'GLOBAL_HOST=global.e2e.gizclaw.test|inject' "$ENTRY_IMAGE" >/dev/null 2>&1; then
  echo 'entry accepted template metacharacters in a host' >&2; exit 1
fi
if docker run --rm "${entry_env[@]}" -e 'WEB_UPSTREAM=http://web:8080|inject' "$ENTRY_IMAGE" >/dev/null 2>&1; then
  echo 'entry accepted template metacharacters in an upstream' >&2; exit 1
fi

entry_cn_env=(
  -e PROFILE=cn -e TLS_CERT_FILE=/etc/hosts -e TLS_KEY_FILE=/etc/hosts
  -e CN_HOST=cn.gizway.com -e GIZPAY_UPSTREAM=http://gizpay:8081
  -e GIZWAY_UPSTREAM=http://gizway:8080 -e WEB_UPSTREAM=http://web:8080
  -e POWERSYNC_PAY_UPSTREAM=http://powersync-pay:8080 -e POWERSYNC_GIZWAY_UPSTREAM=http://powersync-cn:8080
)
entry_central_env=(
  -e PROFILE=central -e TLS_CERT_FILE=/etc/hosts -e TLS_KEY_FILE=/etc/hosts
  -e AUTH_HOST=pay.gizway.com -e PAY_HOST=pay.gizway.com
  -e GIZPAY_UPSTREAM=http://gizpay:8081 -e ZITADEL_UPSTREAM=h2c://zitadel:8080
  -e ZITADEL_LOGIN_UPSTREAM=http://zitadel-login:3000 -e POWERSYNC_PAY_UPSTREAM=http://powersync-pay:8080
)
timeout 10 docker run --rm "${entry_env[@]}" -e GLOBAL_HOST=global.gizway.com "$ENTRY_IMAGE" --help >/dev/null
timeout 10 docker run --rm "${entry_cn_env[@]}" "$ENTRY_IMAGE" --help >/dev/null
timeout 10 docker run --rm "${entry_central_env[@]}" "$ENTRY_IMAGE" --help >/dev/null
if docker run --rm "${entry_env[@]}" -e GLOBAL_HOST=other.gizway.com "$ENTRY_IMAGE" --help >/dev/null 2>&1; then
  echo 'entry accepted an unlisted gizway.com host' >&2; exit 1
fi
if docker run --rm "${entry_env[@]}" -e GIZWAY_UPSTREAM=h2c://gizway:8080 "$ENTRY_IMAGE" --help >/dev/null 2>&1; then
  echo 'entry accepted h2c for a non-ZITADEL upstream' >&2; exit 1
fi

timeout 10 docker run --rm "${powersync_disable_env[@]}" "$POWERSYNC_IMAGE" --help >/dev/null
timeout 10 docker run --rm "${powersync_verify_env[@]}" "$POWERSYNC_IMAGE" --help >/dev/null
timeout 10 docker run --rm "${powersync_verify_env[@]}" -e PS_SOURCE_SSL_MODE=verify-ca "$POWERSYNC_IMAGE" --help >/dev/null
if docker run --rm "${powersync_common_env[@]}" -e PS_STORAGE_SSL_MODE=disable "$POWERSYNC_IMAGE" --help >/dev/null 2>&1; then
  echo 'powersync accepted a missing source SSL mode' >&2; exit 1
fi
if docker run --rm "${powersync_disable_env[@]}" -e PS_SOURCE_SSL_MODE=require "$POWERSYNC_IMAGE" --help >/dev/null 2>&1; then
  echo 'powersync accepted an unsupported SSL mode' >&2; exit 1
fi
if docker run --rm "${powersync_disable_env[@]}" -e PS_SOURCE_CA_FILE=/ca.pem "$POWERSYNC_IMAGE" --help >/dev/null 2>&1; then
  echo 'powersync accepted a CA file with SSL disabled' >&2; exit 1
fi
if docker run --rm "${powersync_verify_env[@]}" -e PS_SOURCE_CA_FILE=relative.pem "$POWERSYNC_IMAGE" --help >/dev/null 2>&1; then
  echo 'powersync accepted a relative CA file' >&2; exit 1
fi
if docker run --rm "${powersync_common_env[@]}" \
  -e PS_SOURCE_SSL_MODE=verify-full -e PS_SOURCE_CA_FILE=/run/secrets/missing-ca.pem \
  -e PS_STORAGE_SSL_MODE=disable "$POWERSYNC_IMAGE" --help >/dev/null 2>&1; then
  echo 'powersync accepted a missing CA file' >&2; exit 1
fi
if docker run --rm "${powersync_verify_env[@]}" -e PS_SOURCE_CA_FILE=/run/secrets/malformed-ca.pem \
  -v "$temporary/malformed-ca.pem:/run/secrets/malformed-ca.pem:ro" "$POWERSYNC_IMAGE" --help >/dev/null 2>&1; then
  echo 'powersync accepted a malformed CA file' >&2; exit 1
fi
uri_secret='uri-secret-must-not-leak'
if docker run --rm "${powersync_disable_env[@]}" \
  -e "PS_SOURCE_URI=postgres://source:$uri_secret@postgres.test:5432/source?sslmode=disable" \
  "$POWERSYNC_IMAGE" --help >"$temporary/powersync-uri-conflict.log" 2>&1; then
  echo 'powersync accepted TLS parameters in a PostgreSQL URI' >&2; exit 1
fi
if grep -F "$uri_secret" "$temporary/powersync-uri-conflict.log" >/dev/null; then
  echo 'powersync leaked a PostgreSQL URI credential' >&2; exit 1
fi
if docker run --rm "${powersync_disable_env[@]}" \
  -e "PS_SOURCE_URI=postgres://source:$uri_secret@postgres.test:5432/source?%53sLmOdE=disable" \
  "$POWERSYNC_IMAGE" --help >"$temporary/powersync-encoded-uri-conflict.log" 2>&1; then
  echo 'powersync accepted a percent-decoded mixed-case TLS URI key' >&2; exit 1
fi
if docker run --rm "${powersync_disable_env[@]}" \
  -e "PS_SOURCE_URI=postgres://source:$uri_secret@postgres.test:5432/source#tls" \
  "$POWERSYNC_IMAGE" --help >"$temporary/powersync-uri-fragment.log" 2>&1; then
  echo 'powersync accepted a PostgreSQL URI fragment' >&2; exit 1
fi
if grep -F "$uri_secret" "$temporary"/powersync-*uri*.log >/dev/null; then
  echo 'powersync leaked a PostgreSQL URI credential' >&2; exit 1
fi
if grep -F 'BEGIN CERTIFICATE' "$temporary"/*.log >/dev/null; then
  echo 'powersync leaked CA PEM content' >&2; exit 1
fi

web_container="$(docker run --detach --publish 127.0.0.1::8080 "$WEB_IMAGE")"
trap 'docker rm --force "$web_container" >/dev/null 2>&1 || true; cleanup' EXIT
web_port="$(docker port "$web_container" 8080/tcp | awk -F: '{print $NF}')"
for _ in {1..30}; do curl --fail --silent "http://127.0.0.1:$web_port/healthz" >/dev/null 2>&1 && break; sleep 1; done
curl --fail --silent "http://127.0.0.1:$web_port/healthz" >/dev/null
docker rm --force "$web_container" >/dev/null
trap cleanup EXIT
printf 'release image smoke passed for all seven images at %s (%s)\n' "$version" "$revision"
