#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
version="${RELEASE_VERSION:-${1:-}}"
output_dir="${RELEASE_SDK_OUTPUT_DIR:-$root/tmp/release/sdk}"
registry="${NPM_REGISTRY_URL:-https://npm.pkg.github.com}"
npm_bin="${NPM_BIN:-npm}"
package='@idy/gizway-browser-sdk'

[[ -n "$version" ]] || { printf 'RELEASE_VERSION or a tag argument is required\n' >&2; exit 2; }
"$root/scripts/release/validate-tag.sh" "$version" --syntax-only
package_version="${version#v}"
artifact="${RELEASE_SDK_ARTIFACT:-$output_dir/browser-sdk-$package_version.tgz}"
[[ -f "$artifact" ]] || { printf 'browser SDK package is missing: %s\n' "$artifact" >&2; exit 2; }

package_json="$(tar -xOf "$artifact" package/package.json)"
jq -e --arg package "$package" --arg version "$package_version" --arg registry "$registry" '
  .name == $package and
  .version == $version and
  .publishConfig.registry == $registry and
  .repository.url == "git+https://github.com/idy/gizway.git" and
  (.private | not)
' <<<"$package_json" >/dev/null

# JavaScript template literal, not shell expansion.
# shellcheck disable=SC2016
local_integrity="$(node -e '
  const { createHash } = require("node:crypto");
  const { readFileSync } = require("node:fs");
  process.stdout.write(`sha512-${createHash("sha512").update(readFileSync(process.argv[1])).digest("base64")}`);
' "$artifact")"
error_file="$(mktemp)"
trap 'rm -f "$error_file"' EXIT

if remote_json="$("$npm_bin" view "$package@$package_version" dist.integrity --registry "$registry" --json 2>"$error_file")"; then
  remote_integrity="$(jq -er 'select(type == "string" and startswith("sha512-"))' <<<"$remote_json")"
  if [[ "$remote_integrity" != "$local_integrity" ]]; then
    printf 'published SDK integrity mismatch for %s@%s\n' "$package" "$package_version" >&2
    printf 'expected: %s\nactual:   %s\n' "$local_integrity" "$remote_integrity" >&2
    exit 1
  fi
  printf '%s@%s is already published with matching integrity\n' "$package" "$package_version"
  exit 0
fi

if ! grep -Eqi 'E404|404 Not Found|is not in this registry' "$error_file"; then
  cat "$error_file" >&2
  printf 'failed to inspect existing SDK package; refusing to publish\n' >&2
  exit 1
fi

dist_tag=latest
[[ "$package_version" == *-* ]] && dist_tag=next
"$npm_bin" publish "$artifact" --registry "$registry" --tag "$dist_tag"
