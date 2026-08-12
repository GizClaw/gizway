#!/bin/sh
set -eu

out="${1:-/pki}"
mkdir -p "${out}"
if [ -f "${out}/ca.crt" ]; then
    exit 0
fi

# These dates are part of the E2E fixture, not a relative lifetime. They cover
# the fixed application clock (2026-08-12) and keep TLS valid when the suite is
# executed on a later wall-clock date.
not_before="20200101000000Z"
not_after="21200101000000Z"

openssl genrsa -out "${out}/ca.key" 2048 >/dev/null 2>&1
openssl req -new -key "${out}/ca.key" -subj "/CN=GizWay E2E CA" -out "${out}/ca.csr"
mkdir -p "${out}/newcerts"
: >"${out}/index.txt"
printf '%s\n' 01 >"${out}/serial"
printf '%s\n' "[ca]
default_ca=fixture_ca

[fixture_ca]
database=${out}/index.txt
new_certs_dir=${out}/newcerts
certificate=${out}/ca.crt
private_key=${out}/ca.key
serial=${out}/serial
default_md=sha256
policy=fixture_policy
unique_subject=no

[fixture_policy]
commonName=supplied

[ca_certificate]
basicConstraints=critical,CA:TRUE
keyUsage=critical,keyCertSign,cRLSign" >"${out}/openssl.cnf"
openssl ca -selfsign -batch -notext -config "${out}/openssl.cnf" -in "${out}/ca.csr" \
    -keyfile "${out}/ca.key" -startdate "${not_before}" -enddate "${not_after}" \
    -extensions ca_certificate -out "${out}/ca.crt" >/dev/null 2>&1
rm -f "${out}/ca.csr"

issue() {
    name="$1"
    subject="$2"
    extensions="$3"
    serial="$4"
    openssl genrsa -out "${out}/${name}.key" 2048 >/dev/null 2>&1
    openssl req -new -key "${out}/${name}.key" -subj "${subject}" -out "${out}/${name}.csr"
    printf '%s\n' "[fixture_leaf]
${extensions}" >"${out}/${name}.ext"
    printf '%s\n' "${serial}" >"${out}/serial"
    openssl ca -batch -notext -config "${out}/openssl.cnf" -in "${out}/${name}.csr" \
        -startdate "${not_before}" -enddate "${not_after}" -extensions fixture_leaf \
        -extfile "${out}/${name}.ext" -out "${out}/${name}.crt" >/dev/null 2>&1
    rm -f "${out}/${name}.csr" "${out}/${name}.ext"
}

issue gizpay "/CN=gizpay" "subjectAltName=DNS:gizpay,DNS:toxiproxy,DNS:localhost,IP:127.0.0.1
extendedKeyUsage=serverAuth" 02
issue gizway-cn "/CN=gw-cn-e2e" "subjectAltName=URI:spiffe://gizway/gateway/cn/gw-cn-e2e
extendedKeyUsage=clientAuth" 03
issue gizway-global "/CN=gw-global-e2e" "subjectAltName=URI:spiffe://gizway/gateway/global/gw-global-e2e
extendedKeyUsage=clientAuth" 04

rm -rf "${out}/newcerts"
rm -f "${out}/index.txt" "${out}/index.txt.attr" "${out}/index.txt.old" \
    "${out}/serial" "${out}/serial.old" "${out}/openssl.cnf"
chmod 600 "${out}"/*.key
