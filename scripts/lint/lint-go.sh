#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
cd "${repository_root}"

go_command="${GO:-go}"
"${go_command}" vet ./...
GO="${go_command}" "${script_dir}/lint-go-modernize.sh"
"${go_command}" tool staticcheck ./...
"${go_command}" tool govulncheck ./...
