#!/usr/bin/env node

"use strict";

const fs = require("node:fs");
const path = require("node:path");

const repoRoot = path.resolve(__dirname, "..");

const TARGETS = [
  { target: "darwin-x64", os: "darwin", cpu: "x64", asset: "clipal-darwin-amd64", binary: "clipal" },
  { target: "darwin-arm64", os: "darwin", cpu: "arm64", asset: "clipal-darwin-arm64", binary: "clipal" },
  { target: "linux-x64", os: "linux", cpu: "x64", asset: "clipal-linux-amd64", binary: "clipal" },
  { target: "linux-arm64", os: "linux", cpu: "arm64", asset: "clipal-linux-arm64", binary: "clipal" },
  { target: "win32-x64", os: "win32", cpu: "x64", asset: "clipal-windows-amd64.exe", binary: "clipal.exe" },
  { target: "win32-arm64", os: "win32", cpu: "arm64", asset: "clipal-windows-arm64.exe", binary: "clipal.exe" }
];

function usage() {
  console.error(
    "usage: build_npm_platform_packages.js [--artifacts DIR] [--out DIR] [--version VERSION]"
  );
}

function parseArgs(argv) {
  const args = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "--artifacts" || arg === "--out" || arg === "--version") {
      const value = argv[i + 1];
      if (!value) {
        throw new Error(`missing value for ${arg}`);
      }
      args[arg.slice(2)] = value;
      i += 1;
      continue;
    }
    if (arg === "--help" || arg === "-h") {
      usage();
      process.exit(0);
    }
    throw new Error(`unknown argument: ${arg}`);
  }
  return args;
}

function main() {
  const args = parseArgs(process.argv.slice(2));
  const basePackage = JSON.parse(
    fs.readFileSync(path.join(repoRoot, "packaging", "npm", "package.json"), "utf8")
  );
  const version = args.version || basePackage.version;
  const artifactsDir = path.resolve(repoRoot, args.artifacts || "build/release");
  const outDir = path.resolve(repoRoot, args.out || "build/npm-platform");

  if (!fs.existsSync(artifactsDir)) {
    throw new Error(`artifacts directory does not exist: ${artifactsDir}`);
  }

  fs.rmSync(outDir, { recursive: true, force: true });
  fs.mkdirSync(outDir, { recursive: true });

  for (const entry of TARGETS) {
    const source = path.join(artifactsDir, entry.asset);
    if (!fs.existsSync(source)) {
      throw new Error(`missing release asset: ${source}`);
    }

    const packageDir = path.join(outDir, `clipal-${entry.target}`);
    const binaryPath = path.join(packageDir, entry.binary);
    fs.mkdirSync(packageDir, { recursive: true });
    fs.copyFileSync(source, binaryPath);
    if (entry.binary !== "clipal.exe") {
      fs.chmodSync(binaryPath, 0o755);
    }

    const packageJSON = {
      name: `@paiart/clipal-${entry.target}`,
      version,
      description: `Clipal binary for ${entry.os} ${entry.cpu}`,
      license: basePackage.license,
      homepage: basePackage.homepage,
      repository: basePackage.repository,
      bugs: basePackage.bugs,
      os: [entry.os],
      cpu: [entry.cpu],
      files: [entry.binary],
      preferUnplugged: true,
      publishConfig: {
        access: "public",
        registry: "https://registry.npmjs.org/"
      }
    };

    fs.writeFileSync(
      path.join(packageDir, "package.json"),
      `${JSON.stringify(packageJSON, null, 2)}\n`
    );
  }

  console.log(`npm platform packages written to ${outDir}`);
}

try {
  main();
} catch (err) {
  console.error(`error: ${err.message}`);
  process.exit(1);
}
