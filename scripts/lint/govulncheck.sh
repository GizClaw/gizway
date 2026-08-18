#!/bin/sh
set -eu

go_command="${GO:-go}"
report="$(mktemp "${TMPDIR:-/tmp}/gizway-govulncheck.XXXXXX")"
cleanup() {
    rm -f "${report}"
}
trap cleanup EXIT INT TERM

if "${go_command}" tool govulncheck "$@" >"${report}" 2>&1; then
    cat "${report}"
    exit 0
fi

cat "${report}"
findings="$(sed -n 's/^Vulnerability #[0-9][0-9]*: \(GO-[0-9][0-9]*-[0-9][0-9]*\)$/\1/p' "${report}")"
if [ -z "${findings}" ]; then
    exit 1
fi

for finding in ${findings}; do
    case "${finding}" in
        GO-2026-6173|GO-2026-6172|GO-2026-6171|GO-2026-6170|GO-2026-6169|GO-2026-6168|GO-2026-6166) ;;
        *) exit 1 ;;
    esac
done

# lib/pq has no fixed release for these advisories yet. Keep scanning the full
# module and fail closed for every vulnerability outside this exact allowlist.
finding_count="$(printf '%s\n' "${findings}" | sort -u | wc -l | tr -d ' ')"
no_fix_count="$(grep -c '^    Fixed in: N/A$' "${report}" || true)"
if [ "${finding_count}" -gt 7 ] || [ "${no_fix_count}" -ne "${finding_count}" ]; then
    exit 1
fi
printf '%s\n' 'govulncheck: allowed only the known lib/pq advisories with no fixed release' >&2
