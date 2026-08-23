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
  global) required='GLOBAL_HOST GIZPAY_UPSTREAM GIZWAY_UPSTREAM POWERSYNC_PAY_UPSTREAM POWERSYNC_GIZWAY_UPSTREAM BROWSER_ALLOWED_ORIGINS' ;;
  cn) required='CN_HOST GIZPAY_UPSTREAM GIZWAY_UPSTREAM POWERSYNC_PAY_UPSTREAM POWERSYNC_GIZWAY_UPSTREAM BROWSER_ALLOWED_ORIGINS' ;;
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

for name in GIZPAY_UPSTREAM GIZWAY_UPSTREAM ZITADEL_UPSTREAM ZITADEL_LOGIN_UPSTREAM POWERSYNC_PAY_UPSTREAM POWERSYNC_GIZWAY_UPSTREAM; do
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

browser_allowed_origins_yaml=''
if [ "$PROFILE" = global ] || [ "$PROFILE" = cn ]; then
  case "$BROWSER_ALLOWED_ORIGINS" in
    ''|,*|*,|*,,*|*[[:space:]]*|*'*'*|*'?'*|*'['*|*']'*|*'{'*|*'}'*|*'`'*|*'$'*|*'|'*|*';'*|*'&'*)
      echo 'BROWSER_ALLOWED_ORIGINS must be a non-empty comma-separated list without whitespace, wildcards, or template characters' >&2
      exit 2
      ;;
  esac
  old_ifs=$IFS
  IFS=,
  seen=','
  for origin in $BROWSER_ALLOWED_ORIGINS; do
    case "$origin" in
      https://*) authority=${origin#https://} ;;
      http://localhost|http://localhost:*|http://127.0.0.1|http://127.0.0.1:*) authority=${origin#http://} ;;
      *) echo "BROWSER_ALLOWED_ORIGINS contains an unsafe origin: $origin" >&2; exit 2 ;;
    esac
    host=${authority%%:*}
    if ! printf '%s\n' "$host" | grep -Eq '^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$'; then
      echo "BROWSER_ALLOWED_ORIGINS contains an invalid origin: $origin" >&2
      exit 2
    fi
    case "$authority" in
      *:*)
        port=${authority#*:}
        case "$port" in ''|*[!0-9]*) echo "BROWSER_ALLOWED_ORIGINS contains an invalid port: $origin" >&2; exit 2;; esac
        if [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then echo "BROWSER_ALLOWED_ORIGINS contains an invalid port: $origin" >&2; exit 2; fi
        ;;
    esac
    case "$seen" in *",$origin,"*) echo "BROWSER_ALLOWED_ORIGINS contains a duplicate origin: $origin" >&2; exit 2;; esac
    seen="$seen$origin,"
    if [ -n "$browser_allowed_origins_yaml" ]; then browser_allowed_origins_yaml="$browser_allowed_origins_yaml, "; fi
    browser_allowed_origins_yaml="$browser_allowed_origins_yaml\"$origin\""
  done
  IFS=$old_ifs
fi

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
  -e "s|@@BROWSER_ALLOWED_ORIGINS@@|$browser_allowed_origins_yaml|g" \
  -e "s|@@ZITADEL_UPSTREAM@@|${ZITADEL_UPSTREAM:-}|g" \
  -e "s|@@ZITADEL_LOGIN_UPSTREAM@@|${ZITADEL_LOGIN_UPSTREAM:-}|g" \
  -e "s|@@POWERSYNC_PAY_UPSTREAM@@|${POWERSYNC_PAY_UPSTREAM:-}|g" \
  -e "s|@@POWERSYNC_GIZWAY_UPSTREAM@@|${POWERSYNC_GIZWAY_UPSTREAM:-}|g" \
  -e "s|@@TLS_CERT_FILE@@|$TLS_CERT_FILE|g" \
  -e "s|@@TLS_KEY_FILE@@|$TLS_KEY_FILE|g" \
  "/etc/gizway/routes-$PROFILE.yml.template" > /tmp/gizway-entry/routes.yml

exec traefik "$@"
