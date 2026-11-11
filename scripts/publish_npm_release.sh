#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="${1:-}"
artifact_dir="${2:-$repo_root/build/release}"
platform_dir="${3:-$repo_root/build/npm-platform}"

if [[ -z "$version" ]]; then
  echo "usage: $0 <version> [artifact-dir] [platform-package-dir]" >&2
  exit 2
fi

cd "$repo_root"

node scripts/set_npm_version.js "$version"
node scripts/build_npm_platform_packages.js \
  --artifacts "$artifact_dir" \
  --out "$platform_dir" \
  --version "$version"

publish_package() {
  local package_dir="$1"
  local package_name
  package_name="$(node -e 'const path=require("node:path"); process.stdout.write(require(path.resolve(process.argv[1])).name)' "$package_dir/package.json")"

  if npm view "${package_name}@${version}" version >/dev/null 2>&1; then
    echo "skip ${package_name}@${version}: already published"
    return
  fi

  echo "publishing ${package_name}@${version}"
  (cd "$package_dir" && npm publish --provenance --access public)
}

for package_dir in "$platform_dir"/*; do
  publish_package "$package_dir"
done

publish_package "$repo_root/packaging/npm"
publish_package "$repo_root/packaging/npm-paiart"
