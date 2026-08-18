#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_root="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
cd "${repository_root}"

go_command="${GO:-go}"
packages="$(${go_command} list ./... | grep -v '/web/apps/gizway/node_modules/')"
# npm dependencies may contain Go fixtures. They are third-party inputs, not
# packages in this module's handwritten source gate.
"${go_command}" vet ${packages}
GO="${go_command}" "${script_dir}/lint-go-modernize.sh" ${packages}
"${go_command}" tool staticcheck ${packages}
GO="${go_command}" "${script_dir}/govulncheck.sh" ${packages}
