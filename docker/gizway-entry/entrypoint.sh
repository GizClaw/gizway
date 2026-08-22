#!/bin/sh
set -eu

required='PROFILE TLS_CERT_FILE TLS_KEY_FILE'
for name in $required; do
  eval "value=\${$name:-}"
  if [ -z "$value" ]; then
    echo "missing required entry configuration: $name" >&2
    exit 2
  fi
done

case "$PROFILE" in
  global) required='GLOBAL_HOST GIZPAY_UPSTREAM GIZWAY_UPSTREAM WEB_UPSTREAM POWERSYNC_PAY_UPSTREAM POWERSYNC_GIZWAY_UPSTREAM' ;;
  cn) required='CN_HOST GIZPAY_UPSTREAM GIZWAY_UPSTREAM WEB_UPSTREAM POWERSYNC_PAY_UPSTREAM POWERSYNC_GIZWAY_UPSTREAM' ;;
  central) required='AUTH_HOST PAY_HOST GIZPAY_UPSTREAM ZITADEL_UPSTREAM ZITADEL_LOGIN_UPSTREAM POWERSYNC_PAY_UPSTREAM' ;;
  *) echo "PROFILE must be one of global, cn, central" >&2; exit 2 ;;
esac
for name in $required; do
  eval "value=\${$name:-}"
  if [ -z "$value" ]; then
    echo "missing required $PROFILE entry configuration: $name" >&2
    exit 2
  fi
done

for name in GLOBAL_HOST CN_HOST AUTH_HOST PAY_HOST; do
  eval "value=\${$name:-}"
  case "$value" in
    '') ;;
    *[!a-z0-9.-]*) echo "$name contains invalid host characters" >&2; exit 2 ;;
    *.gizclaw.com|*.gizclaw.test|pay.gizway.com|global.gizway.com|cn.gizway.com) ;;
    *) echo "$name must be an allowed GizClaw subdomain or fixed GizWay host" >&2; exit 2 ;;
  esac
done

for name in GIZPAY_UPSTREAM GIZWAY_UPSTREAM WEB_UPSTREAM ZITADEL_UPSTREAM ZITADEL_LOGIN_UPSTREAM POWERSYNC_PAY_UPSTREAM POWERSYNC_GIZWAY_UPSTREAM; do
  eval "value=\${$name:-}"
  case "$value" in
    '') ;;
    http://*|https://*)
      if ! printf '%s\n' "$value" | grep -Eq '^https?://[A-Za-z0-9._-]+(:[0-9]+)?$'; then
        echo "$name must be an origin URL without a path, query, fragment, or template metacharacters" >&2
        exit 2
      fi
      ;;
    h2c://*)
      if [ "$name" != ZITADEL_UPSTREAM ] || ! printf '%s\n' "$value" | grep -Eq '^h2c://[A-Za-z0-9._-]+(:[0-9]+)?$'; then
        echo "$name may use h2c:// only for an authority-only ZITADEL upstream" >&2
        exit 2
      fi
      ;;
    *) echo "$name must use an allowed origin scheme" >&2; exit 2 ;;
  esac
done

for path in "$TLS_CERT_FILE" "$TLS_KEY_FILE"; do
  case "$path" in *[!A-Za-z0-9._/-]*) echo "TLS input path contains invalid template characters" >&2; exit 2;; esac
  if [ ! -r "$path" ]; then
    echo "TLS input is not readable: $path" >&2
    exit 2
  fi
done

mkdir -p /tmp/gizway-entry
sed \
  -e "s|@@GLOBAL_HOST@@|${GLOBAL_HOST:-}|g" \
  -e "s|@@CN_HOST@@|${CN_HOST:-}|g" \
  -e "s|@@AUTH_HOST@@|${AUTH_HOST:-}|g" \
  -e "s|@@GIZPAY_UPSTREAM@@|${GIZPAY_UPSTREAM:-}|g" \
  -e "s|@@PAY_HOST@@|${PAY_HOST:-}|g" \
  -e "s|@@GIZWAY_UPSTREAM@@|${GIZWAY_UPSTREAM:-}|g" \
  -e "s|@@WEB_UPSTREAM@@|${WEB_UPSTREAM:-}|g" \
  -e "s|@@ZITADEL_UPSTREAM@@|${ZITADEL_UPSTREAM:-}|g" \
  -e "s|@@ZITADEL_LOGIN_UPSTREAM@@|${ZITADEL_LOGIN_UPSTREAM:-}|g" \
  -e "s|@@POWERSYNC_PAY_UPSTREAM@@|${POWERSYNC_PAY_UPSTREAM:-}|g" \
  -e "s|@@POWERSYNC_GIZWAY_UPSTREAM@@|${POWERSYNC_GIZWAY_UPSTREAM:-}|g" \
  -e "s|@@TLS_CERT_FILE@@|$TLS_CERT_FILE|g" \
  -e "s|@@TLS_KEY_FILE@@|$TLS_KEY_FILE|g" \
  "/etc/gizway/routes-$PROFILE.yml.template" > /tmp/gizway-entry/routes.yml

exec traefik "$@"
