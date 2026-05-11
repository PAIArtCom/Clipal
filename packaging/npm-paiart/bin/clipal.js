#!/usr/bin/env node

const fs = require("node:fs");
const path = require("node:path");
const { spawn } = require("node:child_process");

const binaryName = process.platform === "win32" ? "clipal.exe" : "clipal";
const binaryPath = path.join(__dirname, "..", "vendor", binaryName);

if (!fs.existsSync(binaryPath)) {
  console.error(
    "clipal: bundled binary is missing. Reinstall the package or run `npm rebuild @paiart/clipal`."
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
