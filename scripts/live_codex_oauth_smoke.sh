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

usage() {
  cat <<'EOF'
Usage:
  ./scripts/live_codex_oauth_smoke.sh [options]

Options:
  --config-dir DIR          Source config dir to discover OAuth credentials from (default: ~/.clipal)
  --email EMAIL             Select OAuth credential by email
  --oauth-ref REF           Specific OAuth ref to test
  --oauth-file FILE         Specific credential file to copy into the temporary config
  --model MODEL             Model name for the smoke requests (default: gpt-6-sol)
  --skip-stream             Skip the streaming /v1/responses smoke
  --skip-refresh-retry      Skip the forced 401 -> refresh -> retry smoke
  --keep-temp               Keep the temporary config dir and logs after success
  --list                    List discoverable Codex OAuth credentials and exit
  -h, --help                Show this help

Environment fallbacks:
  CLIPAL_LIVE_CONFIG_DIR
  CLIPAL_LIVE_OAUTH_EMAIL
  CLIPAL_LIVE_OAUTH_REF
  CLIPAL_LIVE_OAUTH_FILE
  CLIPAL_LIVE_MODEL
  CLIPAL_LIVE_SKIP_STREAM
  CLIPAL_LIVE_SKIP_REFRESH_RETRY
  CLIPAL_LIVE_KEEP_TEMP
  CLIPAL_LIVE_VERBOSE
EOF
}

CONFIG_DIR="${CLIPAL_LIVE_CONFIG_DIR:-$HOME/.clipal}"
OAUTH_EMAIL="${CLIPAL_LIVE_OAUTH_EMAIL:-}"
OAUTH_REF="${CLIPAL_LIVE_OAUTH_REF:-}"
OAUTH_FILE="${CLIPAL_LIVE_OAUTH_FILE:-}"
MODEL="${CLIPAL_LIVE_MODEL:-gpt-6-sol}"
SKIP_STREAM="${CLIPAL_LIVE_SKIP_STREAM:-0}"
SKIP_REFRESH_RETRY="${CLIPAL_LIVE_SKIP_REFRESH_RETRY:-0}"
KEEP_TEMP="${CLIPAL_LIVE_KEEP_TEMP:-0}"
VERBOSE="${CLIPAL_LIVE_VERBOSE:-0}"
LIST_ONLY=0

clipal_log_level="info"
if [[ "$VERBOSE" == "1" ]]; then
  clipal_log_level="debug"
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config-dir)
      CONFIG_DIR="${2:-}"
      shift 2
      ;;
    --email)
      OAUTH_EMAIL="${2:-}"
      shift 2
      ;;
    --oauth-ref)
      OAUTH_REF="${2:-}"
      shift 2
      ;;
    --oauth-file)
      OAUTH_FILE="${2:-}"
      shift 2
      ;;
    --model)
      MODEL="${2:-}"
      shift 2
      ;;
    --skip-stream)
      SKIP_STREAM=1
      shift
      ;;
    --skip-refresh-retry)
      SKIP_REFRESH_RETRY=1
      shift
      ;;
    --keep-temp)
      KEEP_TEMP=1
      shift
      ;;
    --list)
      LIST_ONLY=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

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
  local tries="${2:-120}"
  local delay="${3:-0.25}"
  for _ in $(seq 1 "$tries"); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$delay"
  done
  echo "timeout waiting for: $url" >&2
  return 1
}

register_artifacts() {
  local artifact=""
  for artifact in "$@"; do
    [[ -n "$artifact" ]] || continue
    failure_artifacts+=("$artifact")
  done
}

print_failure_artifacts() {
  local artifact=""
  local printed=0
  for artifact in "${failure_artifacts[@]}"; do
    [[ -e "$artifact" ]] || continue
    if [[ "$printed" == "0" ]]; then
      echo "artifacts:" >&2
      printed=1
    fi
    echo "  $artifact" >&2
  done
}

print_failure_details() {
  if [[ "$VERBOSE" != "1" ]]; then
    echo "set CLIPAL_LIVE_VERBOSE=1 to print clipal.log tail and request headers/body" >&2
    return
  fi
  if [[ -f "$tmpdir/clipal.log" ]]; then
    echo "---- last 200 lines of clipal.log ----" >&2
    tail -n 200 "$tmpdir/clipal.log" >&2 || true
  fi
  if [[ -n "$last_request_headers" && -f "$last_request_headers" ]]; then
    echo "---- request headers: $last_request_headers ----" >&2
    cat "$last_request_headers" >&2 || true
  fi
  if [[ -n "$last_request_body" && -f "$last_request_body" ]]; then
    echo "---- request body: $last_request_body ----" >&2
    cat "$last_request_body" >&2 || true
  fi
}

post_json_request() {
  local url="$1"
  local payload="$2"
  local body_file="$3"
  local headers_file="$4"
  last_request_headers="$headers_file"
  last_request_body="$body_file"
  register_artifacts "$headers_file" "$body_file"
  rm -f "$body_file" "$headers_file"
  if ! curl -fsS --max-time 120 \
    -D "$headers_file" \
    -o "$body_file" \
    -X POST \
    -H 'Content-Type: application/json' \
    -H 'Authorization: Bearer clipal-placeholder-token' \
    --data "$payload" \
    "$url"; then
    echo "request failed: POST $url" >&2
    return 1
  fi
}

discover_credentials_json() {
  "$PY" - "$CONFIG_DIR" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1]).expanduser()
cred_dir = root / "oauth" / "codex"
items = []
if cred_dir.exists():
    for path in sorted(cred_dir.glob("*.json")):
        try:
            data = json.loads(path.read_text())
        except Exception:
            continue
        items.append({
            "path": str(path),
            "basename": path.name,
            "ref": str(data.get("ref", "")).strip(),
            "email": str(data.get("email", "")).strip(),
            "provider": str(data.get("provider", "")).strip(),
            "has_refresh_token": bool(str(data.get("refresh_token", "")).strip()),
        })
print(json.dumps(items))
PY
}

list_credentials() {
  local creds_json="$1"
  local want_email="${2:-}"
  local want_ref="${3:-}"
  local want_file="${4:-}"
  "$PY" - "$creds_json" "$want_email" "$want_ref" "$want_file" <<'PY'
import json
import os
import pathlib
import sys

items = json.loads(sys.argv[1])
want_email = sys.argv[2].strip().lower()
want_ref = sys.argv[3].strip()
want_file = sys.argv[4].strip()

if want_file:
    selected = []
    want_path = pathlib.Path(want_file).expanduser().resolve()
    for item in items:
        if pathlib.Path(item["path"]).expanduser().resolve() == want_path:
            selected.append(item)
    items = selected
if want_ref:
    items = [item for item in items if item.get("ref") == want_ref]
if want_email:
    items = [item for item in items if str(item.get("email", "")).strip().lower() == want_email]

if not items:
    print("No Codex OAuth credentials found.")
    raise SystemExit(0)

for item in items:
    path = pathlib.Path(item["path"]).expanduser()
    display = str(path).replace(str(pathlib.Path.home()), "~", 1)
    email = item.get("email") or "<unknown-email>"
    ref = item.get("ref") or "<missing-ref>"
    refresh = "yes" if item.get("has_refresh_token") else "no"
    print(f"{ref}\t{email}\trefresh_token={refresh}\t{display}")
PY
}

creds_json="$(discover_credentials_json)"
if [[ "$LIST_ONLY" == "1" ]]; then
  list_credentials "$creds_json" "$OAUTH_EMAIL" "$OAUTH_REF" "$OAUTH_FILE"
  exit 0
fi

selection_json="$("$PY" - "$creds_json" "$OAUTH_EMAIL" "$OAUTH_REF" "$OAUTH_FILE" <<'PY'
import json
import pathlib
import sys

items = json.loads(sys.argv[1])
want_email = sys.argv[2].strip().lower()
want_ref = sys.argv[3].strip()
want_file = sys.argv[4].strip()

if want_file:
    path = pathlib.Path(want_file).expanduser().resolve()
    for item in items:
        item_path = pathlib.Path(item["path"]).expanduser().resolve()
        if item_path == path:
            print(json.dumps(item))
            raise SystemExit(0)
    print(json.dumps({"error": f"oauth credential file not found in discovery set: {path}"}))
    raise SystemExit(0)

if want_ref:
    matches = [item for item in items if item.get("ref") == want_ref]
    if not matches:
        print(json.dumps({"error": f"oauth ref not found: {want_ref}"}))
        raise SystemExit(0)
    if len(matches) > 1:
        print(json.dumps({"error": f"multiple credentials matched oauth ref {want_ref!r}; use --oauth-file"}))
        raise SystemExit(0)
    print(json.dumps(matches[0]))
    raise SystemExit(0)

if want_email:
    matches = [item for item in items if str(item.get("email", "")).strip().lower() == want_email]
    if not matches:
        print(json.dumps({"error": f"oauth email not found: {want_email}"}))
        raise SystemExit(0)
    if len(matches) > 1:
        print(json.dumps({"error": f"multiple credentials matched oauth email {want_email!r}; use --oauth-file"}))
        raise SystemExit(0)
    print(json.dumps(matches[0]))
    raise SystemExit(0)

if not items:
    print(json.dumps({
        "error": "no Codex OAuth credential found under ~/.clipal/oauth/codex. Authorize one first in Web UI: Add Provider -> OAuth -> Codex"
    }))
    raise SystemExit(0)

if len(items) > 1:
    refs = ", ".join(sorted(item.get("ref") or item.get("basename") or "<unknown>" for item in items))
    print(json.dumps({
        "error": "multiple Codex OAuth credentials found; rerun with --oauth-ref or --oauth-file",
        "candidates": refs,
    }))
    raise SystemExit(0)

print(json.dumps(items[0]))
PY
)"

selection_error="$("$PY" - "$selection_json" <<'PY'
import json
import sys
obj = json.loads(sys.argv[1])
print(obj.get("error", ""))
PY
)"
if [[ -n "$selection_error" ]]; then
  echo "$selection_error" >&2
  candidates="$("$PY" - "$selection_json" <<'PY'
import json
import sys
obj = json.loads(sys.argv[1])
print(obj.get("candidates", ""))
PY
)"
  if [[ -n "$candidates" ]]; then
    echo "candidates: $candidates" >&2
  fi
  echo "" >&2
  echo "discoverable credentials:" >&2
  list_credentials "$creds_json" "" "" "" >&2 || true
  exit 1
fi

oauth_source_path="$("$PY" - "$selection_json" <<'PY'
import json
import sys
print(json.loads(sys.argv[1])["path"])
PY
)"
oauth_ref="$("$PY" - "$selection_json" <<'PY'
import json
import sys
print(json.loads(sys.argv[1])["ref"])
PY
)"
oauth_email="$("$PY" - "$selection_json" <<'PY'
import json
import sys
print(json.loads(sys.argv[1]).get("email", ""))
PY
)"
oauth_has_refresh="$("$PY" - "$selection_json" <<'PY'
import json
import sys
print("1" if json.loads(sys.argv[1]).get("has_refresh_token") else "0")
PY
)"

if [[ ! -f "$oauth_source_path" ]]; then
  echo "selected oauth credential file does not exist: $oauth_source_path" >&2
  exit 1
fi

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/clipal-live-codex-oauth.XXXXXXXX")"
cfgdir="$tmpdir/config"
mkdir -p "$cfgdir/oauth/codex"

clipal_port="$(get_free_port)"
clipal_pid=""
declare -a failure_artifacts=()
last_request_headers=""
last_request_body=""
cleanup() {
  local exit_status=$?
  set +e
  if [[ -n "${clipal_pid:-}" ]]; then
    kill "$clipal_pid" >/dev/null 2>&1 || true
    wait "$clipal_pid" >/dev/null 2>&1 || true
  fi
  if [[ "$exit_status" -ne 0 ]]; then
    KEEP_TEMP=1
    echo "" >&2
    echo "live codex oauth smoke failed" >&2
    echo "temp dir preserved for debugging: $tmpdir" >&2
    echo "logs: $tmpdir/clipal.log" >&2
    print_failure_artifacts
    print_failure_details
  fi
  if [[ "$KEEP_TEMP" != "1" ]]; then
    rm -rf "$tmpdir" >/dev/null 2>&1 || true
  fi
  return "$exit_status"
}
trap cleanup EXIT

credential_path="$cfgdir/oauth/codex/$(basename "$oauth_source_path")"
cp "$oauth_source_path" "$credential_path"
chmod 600 "$credential_path"

cat >"$cfgdir/config.yaml" <<YAML
listen_addr: 127.0.0.1
port: $clipal_port
log_level: $clipal_log_level
reactivate_after: 1h
YAML

cat >"$cfgdir/openai.yaml" <<YAML
providers:
  - name: "codex-live"
    auth_type: "oauth"
    oauth_provider: "codex"
    oauth_ref: "$oauth_ref"
    priority: 1
    enabled: true
YAML

echo "building clipal..."
(cd "$repo_root" && go build -o "$tmpdir/clipal" ./cmd/clipal)

echo "starting clipal on 127.0.0.1:$clipal_port ..."
"$tmpdir/clipal" --config-dir "$cfgdir" --listen-addr 127.0.0.1 --port "$clipal_port" --log-level "$clipal_log_level" >"$tmpdir/clipal.log" 2>&1 &
clipal_pid="$!"
if ! wait_http_ok "http://127.0.0.1:$clipal_port/health"; then
  exit 1
fi

echo "using oauth ref: $oauth_ref"
if [[ -n "$oauth_email" ]]; then
  echo "using oauth email: $oauth_email"
fi

run_nonstream_check() {
  local prompt="$1"
  local expect_token="$2"
  local body_file="$3"
  local headers_file="$4"
  local payload
  payload="$("$PY" - "$MODEL" "$prompt" <<'PY'
import json
import sys
print(json.dumps({
    "model": sys.argv[1],
    "input": sys.argv[2],
}, ensure_ascii=False))
PY
)"
  post_json_request \
    "http://127.0.0.1:$clipal_port/clipal/v1/responses" \
    "$payload" \
    "$body_file" \
    "$headers_file"

  "$PY" - "$body_file" "$expect_token" <<'PY'
import json
import re
import sys

def collect_output_texts(node, out):
    if isinstance(node, dict):
        node_type = str(node.get("type", "")).strip()
        if node_type in ("output_text", "text"):
            text = node.get("text")
            if isinstance(text, str) and text.strip():
                out.append(text.strip())
        output_text = node.get("output_text")
        if isinstance(output_text, str) and output_text.strip():
            out.append(output_text.strip())
        for key in ("output", "content", "response"):
            if key in node:
                collect_output_texts(node[key], out)
    elif isinstance(node, list):
        for item in node:
            collect_output_texts(item, out)

path = sys.argv[1]
expect = re.sub(r"[^A-Z0-9]+", "", sys.argv[2].upper())
with open(path, "r", encoding="utf-8") as f:
    data = json.load(f)
texts = []
collect_output_texts(data, texts)
joined = "\n".join(texts)
normalized = re.sub(r"[^A-Z0-9]+", "", joined.upper())
if expect and expect not in normalized:
    raise SystemExit(f"expected token {expect!r} in response text, got: {joined[:400]!r}")
response_id = data.get("id", "")
preview = joined[:120].replace("\n", " ")
print(f"ok id={response_id or '<none>'} text={preview}")
PY
}

run_stream_check() {
  local prompt="$1"
  local expect_token="$2"
  local body_file="$3"
  local headers_file="$4"
  local payload
  payload="$("$PY" - "$MODEL" "$prompt" <<'PY'
import json
import sys
print(json.dumps({
    "model": sys.argv[1],
    "input": sys.argv[2],
    "stream": True,
}, ensure_ascii=False))
PY
)"
  post_json_request \
    "http://127.0.0.1:$clipal_port/clipal/v1/responses" \
    "$payload" \
    "$body_file" \
    "$headers_file"

  "$PY" - "$body_file" "$headers_file" "$expect_token" <<'PY'
import pathlib
import re
import json
import sys

body = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8", errors="replace")
headers = pathlib.Path(sys.argv[2]).read_text(encoding="utf-8", errors="replace").lower()
expect = re.sub(r"[^A-Z0-9]+", "", sys.argv[3].upper())

if "data:" not in body:
    raise SystemExit("stream response did not contain any SSE data frames")
if not any(marker in body for marker in ("response.completed", "[DONE]", "event: completed")):
    raise SystemExit("stream response did not contain a recognizable completion marker")
if "text/event-stream" not in headers and "text/plain" not in headers:
    raise SystemExit("stream response missing expected SSE-compatible content-type")

texts = []
for raw in body.splitlines():
    if not raw.startswith("data:"):
        continue
    payload = raw[5:].strip()
    if not payload or payload == "[DONE]":
        continue
    try:
        data = json.loads(payload)
    except Exception:
        continue

    event_type = str(data.get("type", "")).strip()
    if event_type == "response.output_text.delta":
        delta = data.get("delta")
        if isinstance(delta, str) and delta.strip():
            texts.append(delta.strip())
    elif event_type == "response.output_text.done":
        text = data.get("text")
        if isinstance(text, str) and text.strip():
            texts.append(text.strip())
    elif event_type == "response.completed":
        response = data.get("response")
        if isinstance(response, dict):
            output = response.get("output")
            if isinstance(output, list):
                for item in output:
                    if not isinstance(item, dict):
                        continue
                    content = item.get("content")
                    if not isinstance(content, list):
                        continue
                    for part in content:
                        if not isinstance(part, dict):
                            continue
                        if str(part.get("type", "")).strip() != "output_text":
                            continue
                        text = part.get("text")
                        if isinstance(text, str) and text.strip():
                            texts.append(text.strip())

normalized = re.sub(r"[^A-Z0-9]+", "", "".join(texts).upper())
if expect and expect not in normalized:
    raise SystemExit(f"expected token {expect!r} in stream response, got: {body[:400]!r}")
print("ok stream completed")
PY
}

echo "test: live non-stream /clipal/v1/responses"
run_nonstream_check \
  "Run a compact smoke test." \
  "" \
  "$tmpdir/nonstream.json" \
  "$tmpdir/nonstream.headers"

if [[ "$SKIP_STREAM" != "1" ]]; then
  echo "test: live streaming /clipal/v1/responses"
  run_stream_check \
    "Reply with exactly STREAMOK and nothing else." \
    "STREAMOK" \
    "$tmpdir/stream.sse" \
    "$tmpdir/stream.headers"
fi

if [[ "$SKIP_REFRESH_RETRY" != "1" ]]; then
  if [[ "$oauth_has_refresh" != "1" ]]; then
    echo "skipping refresh-retry smoke: credential has no refresh_token"
  else
    echo "test: live 401 -> refresh -> retry"
    "$PY" - "$credential_path" <<'PY'
import json
import pathlib
import sys
from datetime import datetime, timedelta, timezone

path = pathlib.Path(sys.argv[1])
data = json.loads(path.read_text())
data["access_token"] = "clipal-live-invalid-token"
data["expires_at"] = (datetime.now(timezone.utc) + timedelta(hours=24)).replace(microsecond=0).isoformat()
path.write_text(json.dumps(data, indent=2) + "\n")
PY

    run_nonstream_check \
      "Run a compact refresh-retry smoke test." \
      "" \
      "$tmpdir/retry.json" \
      "$tmpdir/retry.headers"

    "$PY" - "$credential_path" <<'PY'
import json
import pathlib
import sys

data = json.loads(pathlib.Path(sys.argv[1]).read_text())
access_token = str(data.get("access_token", "")).strip()
last_refresh = str(data.get("last_refresh", "")).strip()
if access_token == "clipal-live-invalid-token":
    raise SystemExit("credential access_token was not replaced after refresh retry")
if not last_refresh:
    raise SystemExit("credential last_refresh was not updated after refresh retry")
print("ok refreshed temp credential updated")
PY
  fi
fi

echo ""
echo "live codex oauth smoke passed"
echo "temp dir: $tmpdir"
echo "logs: $tmpdir/clipal.log"
if [[ "$KEEP_TEMP" != "1" ]]; then
  echo "temp dir will be removed on exit; rerun with --keep-temp to preserve artifacts"
fi
