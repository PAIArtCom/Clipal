"use strict";

const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const repoBaseUrl =
  process.env.CLIPAL_NPM_BASE_URL || "https://github.com/PAIArtCom/Clipal/releases/download";

const PLATFORM_PACKAGES = {
  "darwin-x64": "@paiart/clipal-darwin-x64",
  "darwin-arm64": "@paiart/clipal-darwin-arm64",
  "linux-x64": "@paiart/clipal-linux-x64",
  "linux-arm64": "@paiart/clipal-linux-arm64",
  "win32-x64": "@paiart/clipal-win32-x64",
  "win32-arm64": "@paiart/clipal-win32-arm64"
};

function platformKey() {
  return `${process.platform}-${process.arch}`;
}

function platformPackageName() {
  return PLATFORM_PACKAGES[platformKey()] || null;
}

function binaryName() {
  return process.platform === "win32" ? "clipal.exe" : "clipal";
}

function assetName() {
  switch (process.platform) {
    case "darwin":
      if (process.arch === "x64") return "clipal-darwin-amd64";
      if (process.arch === "arm64") return "clipal-darwin-arm64";
      break;
    case "linux":
      if (process.arch === "x64") return "clipal-linux-amd64";
      if (process.arch === "arm64") return "clipal-linux-arm64";
      break;
    case "win32":
      if (process.arch === "x64") return "clipal-windows-amd64.exe";
      if (process.arch === "arm64") return "clipal-windows-arm64.exe";
      break;
    default:
      break;
  }
  throw new Error(`unsupported platform ${process.platform}/${process.arch}`);
}

function resolvePlatformBinary() {
  const packageName = platformPackageName();
  if (!packageName) {
    return null;
  }

  try {
    const packageDir = path.dirname(require.resolve(`${packageName}/package.json`));
    const candidate = path.join(packageDir, binaryName());
    if (fs.existsSync(candidate)) {
      return candidate;
    }
  } catch {
    // The optional platform package was skipped or is not installed.
  }
  return null;
}

function cacheDirForVersion(version) {
  const explicit = process.env.CLIPAL_NPM_CACHE_DIR;
  if (explicit) {
    return path.join(explicit, version);
  }
  return path.join(os.homedir(), ".clipal", "npm", version);
}

async function downloadToFile(url, destination, version) {
  const response = await fetch(url, {
    headers: {
      "user-agent": `clipal-npm/${version}`
    },
    redirect: "follow",
    signal: AbortSignal.timeout(120000)
  });

  if (!response.ok) {
    throw new Error(`download failed for ${url}: HTTP ${response.status}`);
  }

  const data = Buffer.from(await response.arrayBuffer());
  fs.writeFileSync(destination, data, { mode: 0o755 });
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

function sha256File(filePath) {
  const hash = crypto.createHash("sha256");
  hash.update(fs.readFileSync(filePath));
  return hash.digest("hex");
}

async function ensureFallbackBinary(version) {
  const targetPath = path.join(cacheDirForVersion(version), binaryName());
  if (fs.existsSync(targetPath)) {
    if (process.platform !== "win32") {
      fs.chmodSync(targetPath, 0o755);
    }
    return targetPath;
  }

  const asset = assetName();
  fs.mkdirSync(path.dirname(targetPath), { recursive: true });

  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "clipal-npm-"));
  const checksumsPath = path.join(tempDir, "checksums.txt");
  const downloadPath = path.join(tempDir, asset);

  try {
    await downloadToFile(
      `${repoBaseUrl}/v${version}/checksums.txt`,
      checksumsPath,
      version
    );
    const checksums = parseChecksums(fs.readFileSync(checksumsPath, "utf8"));
    const expectedSha = checksums.get(asset);
    if (!expectedSha) {
      throw new Error(`checksums.txt does not contain ${asset}`);
    }

    await downloadToFile(`${repoBaseUrl}/v${version}/${asset}`, downloadPath, version);
    const actualSha = sha256File(downloadPath);
    if (actualSha !== expectedSha) {
      throw new Error(`checksum mismatch for ${asset}`);
    }

    if (process.platform !== "win32") {
      fs.chmodSync(downloadPath, 0o755);
    }

    try {
      fs.renameSync(downloadPath, targetPath);
    } catch (err) {
      if (err.code === "EXDEV") {
        fs.copyFileSync(downloadPath, targetPath);
        fs.unlinkSync(downloadPath);
        if (process.platform !== "win32") {
          fs.chmodSync(targetPath, 0o755);
        }
      } else if (fs.existsSync(targetPath)) {
        return targetPath;
      } else {
        throw err;
      }
    }

    return targetPath;
  } finally {
    fs.rmSync(tempDir, { recursive: true, force: true });
  }
}

async function resolveBinary(version) {
  const platformBinary = resolvePlatformBinary();
  if (platformBinary) {
    return platformBinary;
  }

  if (!platformPackageName()) {
    throw new Error(`unsupported platform ${process.platform}/${process.arch}`);
  }

  return ensureFallbackBinary(version);
}

module.exports = {
  assetName,
  cacheDirForVersion,
  parseChecksums,
  platformKey,
  platformPackageName,
  resolveBinary,
  sha256File
};
