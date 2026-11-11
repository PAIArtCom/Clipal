#!/bin/sh
# Install the Clipal CLI from GitHub Releases.
#
# Usage:
#   curl -fsSL https://clipal.paiart.com/install.sh | sh
#   curl -fsSL https://clipal.paiart.com/install.sh | sh -s -- --version v0.31.0
#
# Environment:
#   CLIPAL_VERSION       Release version to install (default: latest)
#   CLIPAL_INSTALL_DIR   Directory for the clipal binary (default: ~/.local/bin)
#   CLIPAL_DOWNLOAD_BASE_URL Override the release download base URL (mirror)
#   CLIPAL_DRY_RUN        Set to 1 to print the install plan without changes

set -eu

REPO="PAIArtCom/Clipal"
DOWNLOAD_BASE="${CLIPAL_DOWNLOAD_BASE_URL:-${CLIPAL_DOWNLOAD_BASE:-https://github.com/${REPO}/releases/download}}"
VERSION="${CLIPAL_VERSION:-}"
INSTALL_DIR="${CLIPAL_INSTALL_DIR:-}"
DRY_RUN="${CLIPAL_DRY_RUN:-0}"

usage() {
  cat <<'EOF'
Install the Clipal CLI from GitHub Releases.

Usage:
  install.sh [options]

Options:
  --version <tag>   Release version to install, for example v0.31.0 (default: latest)
  --dir <path>      Directory for the clipal binary (default: ~/.local/bin)
  --dry-run         Print the install plan without downloading or changing files
  -h, --help        Show this help

Environment variables:
  CLIPAL_VERSION, CLIPAL_INSTALL_DIR, CLIPAL_DOWNLOAD_BASE_URL, CLIPAL_DRY_RUN
EOF
}

info() {
  printf '%s\n' "$*"
}

error() {
  printf 'clipal install: %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || error "missing value for --version"
      VERSION="$2"
      shift 2
      ;;
    --version=*)
      VERSION="${1#*=}"
      shift
      ;;
    --dir)
      [ "$#" -ge 2 ] || error "missing value for --dir"
      INSTALL_DIR="$2"
      shift 2
      ;;
    --dir=*)
      INSTALL_DIR="${1#*=}"
      shift
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      error "unknown argument: $1 (use --help)"
      ;;
  esac
done

if need_cmd curl; then
  fetch() {
    curl -fsSL --retry 3 --retry-delay 1 --connect-timeout 20 -o "$2" "$1"
  }
  resolve_latest() {
    url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")" ||
      error "unable to resolve the latest Clipal release"
    printf '%s\n' "$url"
  }
elif need_cmd wget; then
  fetch() {
    wget -q -O "$2" "$1"
  }
  resolve_latest() {
    url="$(wget -q --server-response --spider "https://github.com/${REPO}/releases/latest" 2>&1 |
      awk '/^[[:space:]]*Location: / { loc = $2 } END { gsub(/\r/, "", loc); print loc }')" ||
      error "unable to resolve the latest Clipal release"
    [ -n "$url" ] || error "unable to resolve the latest Clipal release"
    printf '%s\n' "$url"
  }
else
  error "need curl or wget to download Clipal"
fi

detect_os() {
  raw_os="${CLIPAL_OS:-$(uname -s 2>/dev/null || true)}"
  case "$raw_os" in
    Darwin|darwin) printf 'darwin\n' ;;
    Linux|linux) printf 'linux\n' ;;
    *)
      error "unsupported operating system: ${raw_os:-unknown} (supported: macOS, Linux)"
      ;;
  esac
}

detect_arch() {
  raw_arch="${CLIPAL_ARCH:-$(uname -m 2>/dev/null || true)}"
  case "$raw_arch" in
    x86_64|amd64|x64) printf 'amd64\n' ;;
    arm64|aarch64) printf 'arm64\n' ;;
    *)
      error "unsupported CPU architecture: ${raw_arch:-unknown} (supported: x86_64/amd64, arm64/aarch64)"
      ;;
  esac
}

sha256_file() {
  if need_cmd sha256sum; then
    sha256sum "$1" | awk '{print $1}'
  elif need_cmd shasum; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif need_cmd openssl; then
    openssl dgst -sha256 "$1" | awk '{print $NF}'
  else
    error "need sha256sum, shasum, or openssl to verify the download"
  fi
}

if [ -z "$INSTALL_DIR" ]; then
  [ -n "${HOME:-}" ] || error "HOME is not set; use --dir to choose an install directory"
  INSTALL_DIR="${HOME}/.local/bin"
fi

if [ "$VERSION" = "latest" ]; then
  VERSION=""
fi

if [ -z "$VERSION" ]; then
  info "Resolving the latest Clipal release..."
  latest_url="$(resolve_latest)"
  VERSION="${latest_url##*/}"
fi

VERSION="${VERSION#v}"
[ -n "$VERSION" ] || error "could not determine the Clipal version to install"
TAG="v${VERSION}"

os="$(detect_os)"
arch="$(detect_arch)"
ASSET="clipal-${os}-${arch}"

if [ "$DRY_RUN" = "1" ]; then
  info "Dry run: would download ${DOWNLOAD_BASE}/${TAG}/${ASSET}"
  info "Would verify ${DOWNLOAD_BASE}/${TAG}/checksums.txt and install to ${INSTALL_DIR}/clipal"
  exit 0
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/clipal-install.XXXXXX")" ||
  error "unable to create a temporary directory"
trap 'rm -rf "$tmp_dir"' EXIT
trap 'exit 1' HUP INT TERM

info "Downloading Clipal ${TAG} for ${os}/${arch}..."
fetch "${DOWNLOAD_BASE}/${TAG}/checksums.txt" "${tmp_dir}/checksums.txt" ||
  error "unable to download checksums for ${TAG}"
fetch "${DOWNLOAD_BASE}/${TAG}/${ASSET}" "${tmp_dir}/${ASSET}" ||
  error "unable to download ${ASSET} for ${TAG}"

expected="$(awk -v asset="$ASSET" '$2 == asset || $2 == "*" asset { print $1; exit }' "${tmp_dir}/checksums.txt")"
[ -n "$expected" ] || error "checksums.txt for ${TAG} does not list ${ASSET}"

actual="$(sha256_file "${tmp_dir}/${ASSET}")"
[ "$actual" = "$expected" ] ||
  error "checksum mismatch for ${ASSET} (expected ${expected}, got ${actual})"

mkdir -p "$INSTALL_DIR" 2>/dev/null ||
  error "unable to create install directory: ${INSTALL_DIR}"
[ -w "$INSTALL_DIR" ] ||
  error "install directory is not writable: ${INSTALL_DIR} (set CLIPAL_INSTALL_DIR or use --dir)"

target="${INSTALL_DIR}/clipal"
staged="${INSTALL_DIR}/.clipal.tmp.$$"
cp "${tmp_dir}/${ASSET}" "$staged" ||
  error "unable to write to ${INSTALL_DIR}"
chmod 0755 "$staged"
mv -f "$staged" "$target"

info "Installed ${target}"

case ":${PATH}:" in
  *":${INSTALL_DIR}:"*)
    ;;
  *)
    profile="${HOME}/.profile"
    case "${SHELL:-}" in
      */zsh) profile="${HOME}/.zshrc" ;;
      */bash) profile="${HOME}/.bashrc" ;;
    esac
    printf '\n%s is not on your PATH. Add it with:\n\n  echo '\''export PATH="%s:$PATH"'\'' >> %s\n  . %s\n\n' \
      "$INSTALL_DIR" "$INSTALL_DIR" "$profile" "$profile"
    ;;
esac

if "$target" --version >/dev/null 2>&1; then
  info "$("$target" --version)"
else
  info "Clipal was installed but did not run; check your platform and try again."
  exit 1
fi
