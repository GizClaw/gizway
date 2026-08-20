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
    *.gizclaw.com|*.gizclaw.test) ;;
    *) echo "$name must be a gizclaw.com or gizclaw.test host" >&2; exit 2 ;;
  esac
done

for path in "$TLS_CERT_FILE" "$TLS_KEY_FILE"; do
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
