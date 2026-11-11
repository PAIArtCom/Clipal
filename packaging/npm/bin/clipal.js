#!/usr/bin/env node

const fs = require("node:fs");
const path = require("node:path");
const { spawn } = require("node:child_process");

const packageRoot = path.join(__dirname, "..");
const pkg = JSON.parse(fs.readFileSync(path.join(packageRoot, "package.json"), "utf8"));
const packageName = pkg.name;
const binaryName = process.platform === "win32" ? "clipal.exe" : "clipal";
const binaryPath = path.join(packageRoot, "vendor", binaryName);

if (!fs.existsSync(binaryPath)) {
  console.error(
    `clipal: bundled binary is missing.

If you use npm 12+, npm may have blocked the postinstall download.
Fix it with one of:

  npm install -g ${packageName} --allow-scripts=${packageName}
  npm config set allow-scripts=${packageName} --location=user
  npm rebuild -g ${packageName} --allow-scripts=${packageName}

Or install without npm:
  curl -fsSL https://clipal.paiart.com/install.sh | sh
`
  );
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
