#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
cd "${repository_root}"
. "${script_dir}/common/postgresql.sh"

# Refactor 01 intentionally has no cross-restart Usage recovery contract. This
# regression keeps the former recovery runner's important lifecycle guarantee:
# two service-owned test schemas can be cleaned more than once, and a passing
# run must not turn into exit 127 because the EXIT trap contacts a stopped DB.
start_test_postgresql
pay_schema="gizpay_recovery_$$"
way_schema="gizway_recovery_$$"
run_root="$(mktemp -d)"
cleaned=false

create_test_postgresql_schema "${pay_schema}"
create_test_postgresql_schema "${way_schema}"

cleanup() {
    if [ "${cleaned}" = true ]; then
        return
    fi
    cleaned=true
    if [ -n "${pay_schema}" ]; then
        drop_test_postgresql_schema "${pay_schema}"
        pay_schema=""
    fi
    if [ -n "${way_schema}" ]; then
        drop_test_postgresql_schema "${way_schema}"
        way_schema=""
    fi
    stop_test_postgresql
    if [ -d "${run_root}" ]; then
        rm -rf "${run_root}"
    fi
}
trap cleanup EXIT INT TERM

cleanup
cleanup
exit 0
