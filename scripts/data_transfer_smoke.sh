#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

require_cmd go
require_cmd curl

if command -v python3 >/dev/null 2>&1; then
  PY=python3
elif command -v python >/dev/null 2>&1; then
  PY=python
else
  echo "missing required command: python3 (or python)" >&2
  exit 1
fi

get_free_port() {
  "$PY" - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
port = s.getsockname()[1]
s.close()
print(port)
PY
}

wait_http_ok() {
  local url="$1"
  local tries="${2:-100}"
  local delay="${3:-0.1}"
  for _ in $(seq 1 "$tries"); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$delay"
  done
  echo "timeout waiting for: $url" >&2
  return 1
}

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/clipal-data-transfer.XXXXXXXX")"
src="$tmpdir/source"
dst="$tmpdir/destination"
mkdir -p "$src" "$dst"

src_port="$(get_free_port)"
dst_port="$(get_free_port)"
clipal_pid=""

cleanup() {
  set +e
  if [[ -n "${clipal_pid:-}" ]]; then
    kill "$clipal_pid" >/dev/null 2>&1 || true
    wait "$clipal_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmpdir" >/dev/null 2>&1 || true
}
trap cleanup EXIT

cat >"$src/config.yaml" <<YAML
listen_addr: 127.0.0.1
port: $src_port
log_level: info
YAML

cat >"$dst/config.yaml" <<YAML
listen_addr: 127.0.0.1
port: $dst_port
log_level: info
YAML
chmod 600 "$src/config.yaml" "$dst/config.yaml"

cat >"$tmpdir/credential.json" <<'JSON'
{
  "type": "codex",
  "email": "transfer-test@example.com",
  "access_token": "fake-access-token",
  "refresh_token": "fake-refresh-token"
}
JSON

cat >"$tmpdir/sub2api.json" <<'JSON'
{
  "exported_at": "2026-07-12T12:00:00Z",
  "accounts": [
    {
      "name": "OpenAI OAuth",
      "platform": "openai",
      "type": "oauth",
      "credentials": {
        "access_token": "sub2api-access-token",
        "refresh_token": "sub2api-refresh-token",
        "chatgpt_account_id": "sub2api-account"
      }
    }
  ]
}
JSON

echo "building clipal data-transfer test binary..."
(cd "$repo_root" && go build -o "$tmpdir/clipal" ./cmd/clipal)

echo "test: external credential dry-run is side-effect free"
"$tmpdir/clipal" import --config-dir "$src" --dry-run "$tmpdir/credential.json" >"$tmpdir/external-preview.json"
"$PY" - "$tmpdir/external-preview.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    plan = json.load(f)
assert plan["format"] == "cliproxyapi", plan
assert plan["mode"] == "merge", plan
assert plan["files"] == 1, plan
assert plan["credentials"] == 1, plan
PY
if [[ -e "$src/oauth" ]]; then
  echo "dry-run unexpectedly created OAuth state" >&2
  exit 1
fi

echo "test: external formats are merge-only and mixed auto-detection works"
if "$tmpdir/clipal" import --config-dir "$src" --dry-run --mode replace "$tmpdir/credential.json" >"$tmpdir/external-replace.log" 2>&1; then
  echo "external replace unexpectedly succeeded" >&2
  exit 1
fi
if ! grep -q "require merge mode" "$tmpdir/external-replace.log"; then
  echo "external replace failed for an unexpected reason" >&2
  cat "$tmpdir/external-replace.log" >&2 || true
  exit 1
fi
"$tmpdir/clipal" import --config-dir "$src" --dry-run "$tmpdir/credential.json" "$tmpdir/sub2api.json" >"$tmpdir/mixed-preview.json"
"$PY" - "$tmpdir/mixed-preview.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    plan = json.load(f)
assert plan["format"] == "mixed", plan
assert plan["mode"] == "merge", plan
assert plan["files"] == 2, plan
assert plan["credentials"] == 2, plan
PY

echo "test: apply external credential and export canonical private backup"
"$tmpdir/clipal" import --config-dir "$src" --yes "$tmpdir/credential.json" >"$tmpdir/external-apply.json"
"$tmpdir/clipal" export --config-dir "$src" --output "$tmpdir/clipal-data.json"
"$PY" - "$tmpdir/clipal-data.json" <<'PY'
import json, os, stat, sys
path = sys.argv[1]
with open(path, encoding="utf-8") as f:
    data = json.load(f)
assert data["schema"] == "clipal.data", data
assert data["schema_version"] == 1, data
assert sorted(data["data"]["clients"]) == ["claude", "gemini", "openai"], data
assert len(data["data"]["credentials"]) == 1, data
credential = data["data"]["credentials"][0]
assert credential["access_token"] == "fake-access-token", credential
assert credential["refresh_token"] == "fake-refresh-token", credential
providers = data["data"]["clients"]["openai"]["providers"]
assert len(providers) == 1 and providers[0]["auth_type"] == "oauth", providers
assert stat.S_IMODE(os.stat(path).st_mode) == 0o600, oct(os.stat(path).st_mode)
PY

echo "test: native dry-run is replace-by-default and side-effect free"
cp "$dst/config.yaml" "$tmpdir/destination-config.before"
"$tmpdir/clipal" import --config-dir "$dst" --dry-run "$tmpdir/clipal-data.json" >"$tmpdir/native-preview.json"
cmp "$tmpdir/destination-config.before" "$dst/config.yaml"
"$PY" - "$tmpdir/native-preview.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    plan = json.load(f)
assert plan["format"] == "clipal-v1", plan
assert plan["mode"] == "replace", plan
assert plan["native"] is True, plan
assert plan["credentials"] == 1, plan
PY

echo "test: native replace round-trip preserves the complete dataset"
"$tmpdir/clipal" import --config-dir "$dst" --yes "$tmpdir/clipal-data.json" >"$tmpdir/native-apply.json"
"$tmpdir/clipal" export --config-dir "$dst" --output "$tmpdir/restored.json"
"$PY" - "$tmpdir/clipal-data.json" "$tmpdir/restored.json" <<'PY'
import json, sys
def load(path):
    with open(path, encoding="utf-8") as f:
        value = json.load(f)
    value.pop("exported_at", None)
    return value
source = load(sys.argv[1])
restored = load(sys.argv[2])
assert restored == source, {"source": source, "restored": restored}
PY

echo "test: native merge does not duplicate providers or credentials"
"$tmpdir/clipal" import --config-dir "$dst" --format clipal --mode merge --yes "$tmpdir/clipal-data.json" >"$tmpdir/native-merge.json"
"$tmpdir/clipal" export --config-dir "$dst" --output "$tmpdir/merged.json"
"$PY" - "$tmpdir/merged.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    data = json.load(f)
assert len(data["data"]["credentials"]) == 1, data["data"]["credentials"]
assert len(data["data"]["clients"]["openai"]["providers"]) == 1, data["data"]["clients"]["openai"]
PY

echo "starting restored Clipal instance on 127.0.0.1:$src_port ..."
"$tmpdir/clipal" --config-dir "$dst" >"$tmpdir/clipal.log" 2>&1 &
clipal_pid="$!"
if ! wait_http_ok "http://127.0.0.1:$src_port/health"; then
  echo "---- clipal.log ----" >&2
  cat "$tmpdir/clipal.log" >&2 || true
  exit 1
fi

echo "test: legacy export route is absent and canonical API exports data"
legacy_status="$(curl -sS -o "$tmpdir/legacy-export.body" -w '%{http_code}' "http://127.0.0.1:$src_port/api/config/export")"
if [[ "$legacy_status" != "404" ]]; then
  echo "legacy export returned HTTP $legacy_status, want 404" >&2
  exit 1
fi
curl -fsS "http://127.0.0.1:$src_port/api/data/export" >"$tmpdir/api-export.json"
"$PY" - "$tmpdir/api-export.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    data = json.load(f)
assert data["schema"] == "clipal.data", data
assert data["schema_version"] == 1, data
PY

echo "test: CLI delegates preview, apply, and export to the running instance"
"$tmpdir/clipal" export --config-dir "$dst" --output "$tmpdir/daemon-export.json"
"$tmpdir/clipal" import --config-dir "$dst" --dry-run "$tmpdir/daemon-export.json" >"$tmpdir/daemon-preview.json"
"$tmpdir/clipal" import --config-dir "$dst" --yes "$tmpdir/daemon-export.json" >"$tmpdir/daemon-apply.json"
curl -fsS "http://127.0.0.1:$src_port/health" >/dev/null

echo "test: stale preview plan is rejected without mutating data"
"$PY" - "$tmpdir/daemon-export.json" "$tmpdir/preview-request.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    raw = f.read()
request = {"files": [{"name": "backup.json", "data": raw}], "format": "auto"}
with open(sys.argv[2], "w", encoding="utf-8") as f:
    json.dump(request, f)
PY
curl -fsS -H 'Content-Type: application/json' -H 'X-Clipal-UI: 1' --data-binary "@$tmpdir/preview-request.json" \
  "http://127.0.0.1:$src_port/api/data/import/preview" >"$tmpdir/preview-response.json"
"$PY" - "$tmpdir/daemon-export.json" "$tmpdir/preview-response.json" "$tmpdir/stale-apply-request.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as f:
    dataset = json.load(f)
with open(sys.argv[2], encoding="utf-8") as f:
    plan = json.load(f)
dataset["data"]["global"]["log_level"] = "debug" if dataset["data"]["global"].get("log_level") != "debug" else "info"
request = {
    "files": [{"name": "backup.json", "data": json.dumps(dataset)}],
    "format": "auto",
    "plan_id": plan["id"],
}
with open(sys.argv[3], "w", encoding="utf-8") as f:
    json.dump(request, f)
PY
stale_status="$(curl -sS -o "$tmpdir/stale-apply.body" -w '%{http_code}' -H 'Content-Type: application/json' -H 'X-Clipal-UI: 1' \
  --data-binary "@$tmpdir/stale-apply-request.json" "http://127.0.0.1:$src_port/api/data/import/apply")"
if [[ "$stale_status" != "409" ]]; then
  echo "stale apply returned HTTP $stale_status, want 409" >&2
  cat "$tmpdir/stale-apply.body" >&2 || true
  exit 1
fi
curl -fsS "http://127.0.0.1:$src_port/health" >/dev/null
curl -fsS "http://127.0.0.1:$src_port/api/data/export" >"$tmpdir/after-stale-export.json"
"$PY" - "$tmpdir/daemon-export.json" "$tmpdir/after-stale-export.json" <<'PY'
import json, sys
def load(path):
    with open(path, encoding="utf-8") as f:
        value = json.load(f)
    value.pop("exported_at", None)
    return value
assert load(sys.argv[2]) == load(sys.argv[1]), "stale apply mutated exported data"
PY

echo ""
echo "data transfer smoke test passed"
