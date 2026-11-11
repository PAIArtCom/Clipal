#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/clipal-installers.XXXXXX")"
server_pid=""

cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" >/dev/null 2>&1 || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

bash -n "$repo_root/scripts/install.sh"

dry_run_output="$(CLIPAL_VERSION=v9.9.9 CLIPAL_INSTALL_DIR="$tmp_dir/dry-run" \
  CLIPAL_OS=linux CLIPAL_ARCH=amd64 sh "$repo_root/scripts/install.sh" --dry-run)"
grep -q 'Dry run: would download' <<< "$dry_run_output"
[[ ! -e "$tmp_dir/dry-run" ]]

if command -v pwsh >/dev/null 2>&1; then
  cat > "$tmp_dir/parse-install-ps1.ps1" <<'PS1'
param([Parameter(Mandatory = $true)][string]$Path)
$errors = $null
$null = [System.Management.Automation.Language.Parser]::ParseFile($Path, [ref]$null, [ref]$errors)
if ($errors.Count -gt 0) {
    $errors | ForEach-Object { Write-Error $_.Message }
    exit 1
}
PS1
  pwsh -NoProfile -NonInteractive -File "$tmp_dir/parse-install-ps1.ps1" "$repo_root/scripts/install.ps1"
  echo "install.ps1 syntax ok"
else
  echo "pwsh not found; skipping install.ps1 syntax check"
fi

version="9.9.9"
release_root="$tmp_dir/releases"
release_dir="$release_root/v${version}"
mkdir -p "$release_dir"

write_fake_binary() {
  local asset="$1"
  cat > "$release_dir/$asset" <<'SH'
#!/bin/sh
echo "clipal v9.9.9 (fake)"
SH
  chmod +x "$release_dir/$asset"
}

for asset in \
  clipal-darwin-amd64 \
  clipal-darwin-arm64 \
  clipal-linux-amd64 \
  clipal-linux-arm64; do
  write_fake_binary "$asset"
done

for asset in clipal-windows-amd64.exe clipal-windows-arm64.exe; do
  printf 'fake windows binary\n' > "$release_dir/$asset"
done

"$repo_root/scripts/gen_checksums.sh" "$release_dir" > "$release_dir/checksums.txt"
cp "$release_dir/checksums.txt" "$tmp_dir/checksums.good"

port="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"

python3 -m http.server "$port" --bind 127.0.0.1 --directory "$release_root" >/dev/null 2>&1 &
server_pid=$!

ready=false
for _ in $(seq 1 50); do
  if curl -fsS "http://127.0.0.1:${port}/v${version}/checksums.txt" >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.1
done
if [[ "$ready" != true ]]; then
  echo "error: local release server did not start" >&2
  exit 1
fi

base_url="http://127.0.0.1:${port}"
install_dir="$tmp_dir/bin"

CLIPAL_DOWNLOAD_BASE_URL="$base_url" \
CLIPAL_VERSION="v${version}" \
CLIPAL_INSTALL_DIR="$install_dir" \
CLIPAL_OS=linux \
CLIPAL_ARCH=amd64 \
  sh "$repo_root/scripts/install.sh" >"$tmp_dir/install.log" 2>&1

if [[ -x "$install_dir/clipal" ]]; then
  :
else
  echo "error: install.sh did not install a binary" >&2
  exit 1
fi
"$install_dir/clipal" --version | grep -q "clipal v9.9.9 (fake)"

awk -v bad="0000000000000000000000000000000000000000000000000000000000000000" \
  '$2 == "clipal-linux-amd64" { $1 = bad } { print }' OFS="  " \
  "$release_dir/checksums.txt" > "$tmp_dir/checksums.bad"
mv "$tmp_dir/checksums.bad" "$release_dir/checksums.txt"

if CLIPAL_DOWNLOAD_BASE_URL="$base_url" \
   CLIPAL_VERSION="v${version}" \
   CLIPAL_INSTALL_DIR="$tmp_dir/bad-bin" \
   CLIPAL_OS=linux \
   CLIPAL_ARCH=amd64 \
   sh "$repo_root/scripts/install.sh" >"$tmp_dir/bad-checksum.log" 2>&1; then
  echo "error: install.sh accepted a mismatched checksum" >&2
  exit 1
fi
grep -q "checksum mismatch" "$tmp_dir/bad-checksum.log"
cp "$tmp_dir/checksums.good" "$release_dir/checksums.txt"

if CLIPAL_DOWNLOAD_BASE_URL="$base_url" \
   CLIPAL_VERSION="v${version}" \
   CLIPAL_INSTALL_DIR="$tmp_dir/unsupported" \
   CLIPAL_OS=linux \
   CLIPAL_ARCH=riscv64 \
   sh "$repo_root/scripts/install.sh" >"$tmp_dir/unsupported.log" 2>&1; then
  echo "error: install.sh accepted an unsupported architecture" >&2
  exit 1
fi
grep -q "unsupported CPU architecture" "$tmp_dir/unsupported.log"

if command -v pwsh >/dev/null 2>&1; then
  ps_install_dir="$tmp_dir/ps-bin"
  ps_dry_run_dir="$tmp_dir/ps-dry-run"
  ps_dry_run_output="$(CLIPAL_VERSION="v${version}" \
    CLIPAL_INSTALL_DIR="$ps_dry_run_dir" \
    CLIPAL_DRY_RUN=1 \
    pwsh -NoProfile -NonInteractive -File "$repo_root/scripts/install.ps1")"
  grep -q 'Dry run: would download' <<< "$ps_dry_run_output"
  [[ ! -e "$ps_dry_run_dir" ]]

  CLIPAL_DOWNLOAD_BASE_URL="$base_url" \
  CLIPAL_VERSION="v${version}" \
  CLIPAL_INSTALL_DIR="$ps_install_dir" \
    pwsh -NoProfile -NonInteractive -File "$repo_root/scripts/install.ps1" >"$tmp_dir/ps-install.log" 2>&1

  [[ -f "$ps_install_dir/clipal.exe" ]] || {
    echo "error: install.ps1 did not install a binary" >&2
    exit 1
  }
  echo "install.ps1 download and checksum verification ok"
fi

echo "installer checks passed"
