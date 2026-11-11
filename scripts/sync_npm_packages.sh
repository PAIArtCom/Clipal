#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
shared_dir="$repo_root/packaging/npm-shared"
package_dirs=(
  "$repo_root/packaging/npm"
  "$repo_root/packaging/npm-paiart"
)

check_only=false
if [[ "${1:-}" == "--check" ]]; then
  check_only=true
fi

for package_dir in "${package_dirs[@]}"; do
  mkdir -p "$package_dir/bin" "$package_dir/lib"

  for file in bin/clipal.js lib/install.js; do
    source="$shared_dir/$file"
    target="$package_dir/$file"

    if [[ "$check_only" == true ]]; then
      if ! cmp -s "$source" "$target"; then
        echo "error: $target is out of sync with $source" >&2
        echo "run ./scripts/sync_npm_packages.sh" >&2
        exit 1
      fi
      continue
    fi

    cp "$source" "$target"
  done

  chmod +x "$package_dir/bin/clipal.js"
done

if [[ "$check_only" == true ]]; then
  echo "npm package wrappers are in sync"
fi
