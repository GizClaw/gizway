#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
. "${script_dir}/common/postgresql.sh"
start_test_postgresql
trap stop_test_postgresql EXIT INT TERM

"${script_dir}/test-unit-go.sh"
"${script_dir}/test-unit-go-race.sh"
"${script_dir}/test-unit-api.sh"
"${script_dir}/test-unit-postgresql.sh"
"${script_dir}/test-unit-web.sh"
(cd "${script_dir}/../../tests/powersync" && npm run typecheck && npm test)
