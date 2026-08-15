#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
cd "${repository_root}"
. "${script_dir}/common/postgresql.sh"
start_test_postgresql
trap stop_test_postgresql EXIT INT TERM

"${GO:-go}" test -count=1 ./internal/storage -run '^TestPostgreSQLMilestone03'
