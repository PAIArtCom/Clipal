#!/usr/bin/env node

"use strict";

const fs = require("node:fs");
const path = require("node:path");
const { spawn } = require("node:child_process");
const { resolveBinary } = require("../lib/install");

const packageRoot = path.join(__dirname, "..");
const pkg = JSON.parse(fs.readFileSync(path.join(packageRoot, "package.json"), "utf8"));

function printRemediation(err) {
  const windowsHint =
    process.platform === "win32"
      ? `\nOn Windows:\n  powershell -ExecutionPolicy ByPass -c "irm https://clipal.paiart.com/install.ps1 | iex"\n`
      : "";

  console.error(
    `clipal: unable to locate the Clipal binary: ${err.message}

This npm package installs a platform-specific optional dependency. If it was
skipped, reinstall with optional dependencies enabled:

  npm install -g ${pkg.name} --include=optional

Or use the standalone installer:
  curl -fsSL https://clipal.paiart.com/install.sh | sh
${windowsHint}`
  );
}

async function main() {
  let binaryPath;
  try {
    binaryPath = await resolveBinary(pkg.version);
  } catch (err) {
    printRemediation(err);
    process.exit(1);
  }

  const child = spawn(binaryPath, process.argv.slice(2), {
    stdio: "inherit"
  });

  child.on("exit", (code, signal) => {
    if (signal) {
      process.kill(process.pid, signal);
      return;
    }
    process.exit(code ?? 1);
  });

  child.on("error", (err) => {
    console.error(`clipal: failed to start bundled binary: ${err.message}`);
    process.exit(1);
  });
}

main();
