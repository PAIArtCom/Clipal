#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
pkg_dir="$repo_root/packaging/npm"
out_dir="$repo_root/build/artifacts/npm"

mkdir -p "$out_dir"
rm -f "$out_dir"/clipal-*.tgz

(cd "$pkg_dir" && npm pack --pack-destination "$out_dir")

echo "npm package written to:"
find "$out_dir" -maxdepth 1 -type f -name "clipal-*.tgz" | sort
