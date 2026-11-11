#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/clipal-npm-packaging.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

cd "$repo_root"

./scripts/sync_npm_packages.sh --check

node --check packaging/npm-shared/bin/clipal.js
node --check packaging/npm-shared/lib/install.js
node --check packaging/npm/bin/clipal.js
node --check packaging/npm/lib/install.js
node --check packaging/npm-paiart/bin/clipal.js
node --check packaging/npm-paiart/lib/install.js
node --check scripts/build_npm_platform_packages.js
node --check scripts/set_npm_version.js
bash -n scripts/publish_npm_release.sh
bash -n scripts/sync_npm_packages.sh

for package_dir in packaging/npm packaging/npm-paiart; do
  pack_json="$tmp_dir/$(basename "$package_dir").json"
  (cd "$package_dir" && npm pack --dry-run --json > "$pack_json" 2>/dev/null)

  node - "$pack_json" "$package_dir" <<'NODE'
const fs = require("node:fs");
const path = require("node:path");

const packPath = process.argv[2];
const packageDir = process.argv[3];
const pack = JSON.parse(fs.readFileSync(packPath, "utf8"));
const entry = Object.values(pack)[0];
if (!entry) {
  throw new Error(`${packageDir}: npm pack returned no package`);
}

const pkg = JSON.parse(fs.readFileSync(path.join(packageDir, "package.json"), "utf8"));
if (pkg.scripts && pkg.scripts.postinstall) {
  throw new Error(`${pkg.name}: postinstall must not be present`);
}

const optional = Object.keys(pkg.optionalDependencies || {});
if (optional.length !== 6) {
  throw new Error(`${pkg.name}: expected 6 optional platform dependencies, got ${optional.length}`);
}
for (const target of ["darwin-x64", "darwin-arm64", "linux-x64", "linux-arm64", "win32-x64", "win32-arm64"]) {
  if (!pkg.optionalDependencies[`@paiart/clipal-${target}`]) {
    throw new Error(`${pkg.name}: missing optional dependency @paiart/clipal-${target}`);
  }
}

const packedFiles = new Set(entry.files.map((file) => file.path));
for (const required of ["README.md", "bin/clipal.js", "lib/install.js"]) {
  if (!packedFiles.has(required)) {
    throw new Error(`${pkg.name}: missing packed file ${required}`);
  }
}
if (packedFiles.has("scripts/postinstall.js")) {
  throw new Error(`${pkg.name}: postinstall script must not be packed`);
}

console.log(`${pkg.name}: npm package contents ok`);
NODE
done

artifacts_dir="$tmp_dir/artifacts"
platform_dir="$tmp_dir/platform"
mkdir -p "$artifacts_dir"

for asset in \
  clipal-darwin-amd64 \
  clipal-darwin-arm64 \
  clipal-linux-amd64 \
  clipal-linux-arm64 \
  clipal-windows-amd64.exe \
  clipal-windows-arm64.exe; do
  printf '#!/bin/sh\nexit 0\n' > "$artifacts_dir/$asset"
  chmod +x "$artifacts_dir/$asset"
done

node scripts/build_npm_platform_packages.js \
  --artifacts "$artifacts_dir" \
  --out "$platform_dir" \
  --version 9.9.9

node - "$platform_dir" <<'NODE'
const fs = require("node:fs");
const path = require("node:path");

const platformDir = process.argv[2];
const expected = {
  "clipal-darwin-x64": { os: "darwin", cpu: "x64", binary: "clipal" },
  "clipal-darwin-arm64": { os: "darwin", cpu: "arm64", binary: "clipal" },
  "clipal-linux-x64": { os: "linux", cpu: "x64", binary: "clipal" },
  "clipal-linux-arm64": { os: "linux", cpu: "arm64", binary: "clipal" },
  "clipal-win32-x64": { os: "win32", cpu: "x64", binary: "clipal.exe" },
  "clipal-win32-arm64": { os: "win32", cpu: "arm64", binary: "clipal.exe" }
};

for (const [dirName, want] of Object.entries(expected)) {
  const packageDir = path.join(platformDir, dirName);
  const pkg = JSON.parse(fs.readFileSync(path.join(packageDir, "package.json"), "utf8"));
  if (pkg.name !== `@paiart/${dirName}`) {
    throw new Error(`${dirName}: unexpected package name ${pkg.name}`);
  }
  if (pkg.version !== "9.9.9") {
    throw new Error(`${dirName}: unexpected version ${pkg.version}`);
  }
  if (pkg.os.length !== 1 || pkg.os[0] !== want.os) {
    throw new Error(`${dirName}: unexpected os ${pkg.os}`);
  }
  if (pkg.cpu.length !== 1 || pkg.cpu[0] !== want.cpu) {
    throw new Error(`${dirName}: unexpected cpu ${pkg.cpu}`);
  }
  if (pkg.scripts && pkg.scripts.postinstall) {
    throw new Error(`${dirName}: postinstall must not be present`);
  }
  if (!fs.existsSync(path.join(packageDir, want.binary))) {
    throw new Error(`${dirName}: missing ${want.binary}`);
  }
}

console.log("npm platform packages ok");
NODE

echo "npm packaging checks passed"
