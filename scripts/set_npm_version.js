#!/usr/bin/env node

"use strict";

const fs = require("node:fs");
const path = require("node:path");

const TARGETS = [
  "darwin-x64",
  "darwin-arm64",
  "linux-x64",
  "linux-arm64",
  "win32-x64",
  "win32-arm64"
];

const PACKAGE_DIRS = ["packaging/npm", "packaging/npm-paiart"];
const repoRoot = path.resolve(__dirname, "..");

function main() {
  const version = process.argv[2];
  if (!version) {
    console.error("usage: set_npm_version.js <version>");
    process.exit(2);
  }

  for (const packageDir of PACKAGE_DIRS) {
    const packagePath = path.join(repoRoot, packageDir, "package.json");
    const pkg = JSON.parse(fs.readFileSync(packagePath, "utf8"));
    pkg.version = version;
    pkg.optionalDependencies = pkg.optionalDependencies || {};
    for (const target of TARGETS) {
      pkg.optionalDependencies[`@paiart/clipal-${target}`] = version;
    }
    fs.writeFileSync(packagePath, `${JSON.stringify(pkg, null, 2)}\n`);
  }
}

main();
