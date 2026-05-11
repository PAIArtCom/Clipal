#!/usr/bin/env node

const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const https = require("node:https");

const packageRoot = path.join(__dirname, "..");
const pkg = JSON.parse(fs.readFileSync(path.join(packageRoot, "package.json"), "utf8"));
const vendorDir = path.join(packageRoot, "vendor");

const repoBaseUrl =
  process.env.CLIPAL_NPM_BASE_URL || "https://github.com/PAIArtCom/Clipal/releases/download";
const versionTag = `v${pkg.version}`;
const checksumsUrl = `${repoBaseUrl}/${versionTag}/checksums.txt`;

function resolveAssetName() {
  switch (process.platform) {
    case "darwin":
      if (process.arch === "arm64") return "clipal-darwin-arm64";
      if (process.arch === "x64") return "clipal-darwin-amd64";
      break;
    case "linux":
      if (process.arch === "arm64") return "clipal-linux-arm64";
      if (process.arch === "x64") return "clipal-linux-amd64";
      break;
    case "win32":
      if (process.arch === "arm64") return "clipal-windows-arm64.exe";
      if (process.arch === "x64") return "clipal-windows-amd64.exe";
      break;
    default:
      break;
  }
  throw new Error(`unsupported platform ${process.platform}/${process.arch}`);
}

function download(url, destination) {
  return new Promise((resolve, reject) => {
    const request = https.get(
      url,
      {
        headers: {
          "user-agent": `clipal-npm/${pkg.version}`
        }
      },
      (response) => {
        if (
          response.statusCode &&
          response.statusCode >= 300 &&
          response.statusCode < 400 &&
          response.headers.location
        ) {
          response.resume();
          download(response.headers.location, destination).then(resolve, reject);
          return;
        }

        if (response.statusCode !== 200) {
          response.resume();
          reject(new Error(`download failed for ${url}: HTTP ${response.statusCode}`));
          return;
        }

        const out = fs.createWriteStream(destination, { mode: 0o755 });
        response.pipe(out);
        out.on("finish", () => out.close(resolve));
        out.on("error", reject);
      }
    );

    request.on("error", reject);
  });
}

function parseChecksums(text) {
  const map = new Map();
  for (const line of text.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const match = trimmed.match(/^([a-f0-9]{64})\s+\*?(.+)$/i);
    if (!match) {
      throw new Error(`invalid checksums line: ${line}`);
    }
    map.set(match[2], match[1].toLowerCase());
  }
  return map;
}

function sha256(filePath) {
  const hash = crypto.createHash("sha256");
  hash.update(fs.readFileSync(filePath));
  return hash.digest("hex");
}

async function main() {
  const assetName = resolveAssetName();
  const binaryName = process.platform === "win32" ? "clipal.exe" : "clipal";
  const targetPath = path.join(vendorDir, binaryName);

  fs.mkdirSync(vendorDir, { recursive: true });

  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "clipal-npm-"));
  const checksumsPath = path.join(tempDir, "checksums.txt");
  const downloadPath = path.join(tempDir, assetName);

  try {
    console.log(`clipal: downloading ${assetName} for ${process.platform}/${process.arch}`);
    await download(checksumsUrl, checksumsPath);
    const checksums = parseChecksums(fs.readFileSync(checksumsPath, "utf8"));
    const expectedSha = checksums.get(assetName);
    if (!expectedSha) {
      throw new Error(`checksums.txt does not contain ${assetName}`);
    }

    await download(`${repoBaseUrl}/${versionTag}/${assetName}`, downloadPath);
    const actualSha = sha256(downloadPath);
    if (actualSha !== expectedSha) {
      throw new Error(`checksum mismatch for ${assetName}`);
    }

    fs.copyFileSync(downloadPath, targetPath);
    if (process.platform !== "win32") {
      fs.chmodSync(targetPath, 0o755);
    }
    console.log(`clipal: installed bundled binary to ${path.relative(packageRoot, targetPath)}`);
  } finally {
    fs.rmSync(tempDir, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error(`clipal: postinstall failed: ${err.message}`);
  process.exit(1);
});
