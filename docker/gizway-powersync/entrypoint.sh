#!/bin/sh
set -eu

fail() {
  printf 'invalid PowerSync TLS configuration: %s\n' "$1" >&2
  exit 2
}

normalize_uri_credentials() {
  prefix="$1"
  uri_name="PS_${prefix}_URI"
  eval "uri_value=\${$uri_name:-}"
  [ -n "$uri_value" ] || fail "$uri_name is required"

  if ! encoded_credentials="$(PS_VALIDATE_URI="$uri_value" node - <<'NODE'
const value = process.env.PS_VALIDATE_URI;
let uri;
try {
  uri = new URL(value);
} catch {
  process.exit(1);
}
if (!['postgres:', 'postgresql:'].includes(uri.protocol) || uri.hash !== '') {
  process.exit(1);
}
const explicitTlsKeys = new Set(['cacert', 'tls_servername', 'client_certificate', 'client_private_key']);
for (const key of uri.searchParams.keys()) {
  const normalized = key.toLowerCase();
  if (normalized.startsWith('ssl') || explicitTlsKeys.has(normalized)) {
    process.exit(1);
  }
}
let username;
let password;
try {
  username = decodeURIComponent(uri.username);
  password = decodeURIComponent(uri.password);
} catch {
  process.exit(1);
}
if (username === '' || password === '' || /[\0\r\n]/.test(username) || /[\0\r\n]/.test(password)) {
  process.exit(1);
}
process.stdout.write(`${Buffer.from(username).toString('base64')}\n${Buffer.from(password).toString('base64')}`);
NODE
  )"; then
    fail "$uri_name must be a PostgreSQL URI with valid login credentials and without a fragment or TLS query parameters"
  fi

  credential_separator='
'
  username_base64="${encoded_credentials%%"$credential_separator"*}"
  password_base64="${encoded_credentials#*"$credential_separator"}"
  [ -n "$username_base64" ] && [ -n "$password_base64" ] && [ "$password_base64" != "$encoded_credentials" ] || fail "$uri_name credentials could not be normalized"
  username="$(printf '%s' "$username_base64" | base64 -d)"
  password="$(printf '%s' "$password_base64" | base64 -d)"
  export "PS_${prefix}_USERNAME=$username"
  export "PS_${prefix}_PASSWORD=$password"
}

load_ca_bundle() {
  file_name="$1"
  eval "file_value=\${$file_name:-}"
  [ -n "$file_value" ] || fail "$file_name is required for verified TLS"
  case "$file_value" in
    /*) ;;
    *) fail "$file_name must be an absolute path" ;;
  esac
  [ -r "$file_value" ] || fail "$file_name is not readable"

  if ! PS_CA_FILE="$file_value" node - <<'NODE'
const fs = require('node:fs');
const { X509Certificate } = require('node:crypto');
let value;
try {
  value = fs.readFileSync(process.env.PS_CA_FILE, 'utf8');
} catch {
  process.exit(1);
}
const pattern = /-----BEGIN CERTIFICATE-----[\s\S]*?-----END CERTIFICATE-----/g;
const certificates = value.match(pattern) ?? [];
if (certificates.length === 0 || value.replace(pattern, '').trim() !== '') {
  process.exit(1);
}
try {
  for (const certificate of certificates) new X509Certificate(certificate);
} catch {
  process.exit(1);
}
process.stdout.write(value.trimEnd());
NODE
  then
    fail "$file_name must contain only valid X.509 CERTIFICATE PEM blocks"
  fi
}

configure_connection() {
  prefix="$1"
  uri_name="PS_${prefix}_URI"
  mode_name="PS_${prefix}_SSL_MODE"
  file_name="PS_${prefix}_CA_FILE"
  cert_name="PS_${prefix}_CA_CERT"

  normalize_uri_credentials "$prefix"
  eval "mode_value=\${$mode_name:-}"
  case "$mode_value" in
    disable)
      eval "file_value=\${$file_name:-}"
      [ -z "$file_value" ] || fail "$file_name must be unset when $mode_name is disable"
      cert_value=''
      ;;
    verify-ca|verify-full)
      cert_value="$(load_ca_bundle "$file_name")"
      ;;
    *) fail "$mode_name must be disable, verify-ca, or verify-full" ;;
  esac

  export "$cert_name=$cert_value"
}

configure_connection SOURCE
configure_connection STORAGE

exec "$@"
