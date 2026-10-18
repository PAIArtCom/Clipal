#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

if ! command -v go >/dev/null 2>&1; then
  echo "lint: go is required" >&2
  exit 1
fi

golangci_lint="${GOLANGCI_LINT:-}"
if [[ -z "$golangci_lint" ]]; then
  if command -v golangci-lint >/dev/null 2>&1; then
    golangci_lint="$(command -v golangci-lint)"
  else
    gopath="$(go env GOPATH)"
    candidates=(
      "$gopath/bin/golangci-lint"
      "$HOME/bin/golangci-lint"
      "/opt/homebrew/bin/golangci-lint"
      "/usr/local/bin/golangci-lint"
    )
    for candidate in "${candidates[@]}"; do
      if [[ -x "$candidate" ]]; then
        golangci_lint="$candidate"
        break
      fi
    done
  fi
fi

if [[ -z "$golangci_lint" || ! -x "$golangci_lint" ]]; then
  echo "lint: golangci-lint is required" >&2
  echo "lint: install it from https://golangci-lint.run/docs/welcome/install/local/" >&2
  echo "lint: or set GOLANGCI_LINT=/path/to/golangci-lint" >&2
  exit 1
fi

exec "$golangci_lint" run "$@"
