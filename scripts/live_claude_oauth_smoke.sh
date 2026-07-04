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
  ./scripts/live_claude_oauth_smoke.sh [options]

Options:
  --config-dir DIR          Source config dir to discover OAuth credentials from (default: ~/.clipal)
  --email EMAIL             Select OAuth credential by email
  --oauth-ref REF           Specific OAuth ref to test
  --oauth-file FILE         Specific credential file to copy into the temporary config
  --model MODEL             Model name for the smoke requests (default: claude-sonnet-4-6)
  --skip-stream             Skip the streaming /v1/messages smoke
  --skip-count-tokens       Skip the /v1/messages/count_tokens smoke
  --skip-refresh-retry      Skip the forced 401 -> refresh -> retry smoke
  --keep-temp               Keep the temporary config dir and logs after success
  --list                    List discoverable Claude OAuth credentials and exit
  -h, --help                Show this help

Environment fallbacks:
  CLIPAL_LIVE_CONFIG_DIR
  CLIPAL_LIVE_OAUTH_EMAIL
  CLIPAL_LIVE_OAUTH_REF
  CLIPAL_LIVE_OAUTH_FILE
  CLIPAL_LIVE_MODEL
  CLIPAL_LIVE_SKIP_STREAM
  CLIPAL_LIVE_SKIP_COUNT_TOKENS
  CLIPAL_LIVE_SKIP_REFRESH_RETRY
  CLIPAL_LIVE_KEEP_TEMP
  CLIPAL_LIVE_VERBOSE

Notes:
  The script strips refresh_token from the temporary credential by default.
  This prevents refresh token rotation in the temp store from invalidating the
  real credential under the source config dir.
EOF
}

CONFIG_DIR="${CLIPAL_LIVE_CONFIG_DIR:-$HOME/.clipal}"
OAUTH_EMAIL="${CLIPAL_LIVE_OAUTH_EMAIL:-}"
OAUTH_REF="${CLIPAL_LIVE_OAUTH_REF:-}"
OAUTH_FILE="${CLIPAL_LIVE_OAUTH_FILE:-}"
MODEL="${CLIPAL_LIVE_MODEL:-claude-sonnet-4-6}"
SKIP_STREAM="${CLIPAL_LIVE_SKIP_STREAM:-0}"
SKIP_COUNT_TOKENS="${CLIPAL_LIVE_SKIP_COUNT_TOKENS:-0}"
SKIP_REFRESH_RETRY="${CLIPAL_LIVE_SKIP_REFRESH_RETRY:-0}"
KEEP_TEMP="${CLIPAL_LIVE_KEEP_TEMP:-0}"
VERBOSE="${CLIPAL_LIVE_VERBOSE:-0}"
LIST_ONLY=0
claude_code_app_version="2.1.201"
claude_billing_version_salt="59cf53e54c78"

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
    --skip-count-tokens)
      SKIP_COUNT_TOKENS=1
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

read_retry_after() {
  local headers_file="$1"
  "$PY" - "$headers_file" <<'PY'
import pathlib
import sys

headers = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8", errors="replace").splitlines()
for line in headers:
    if ":" not in line:
        continue
    name, value = line.split(":", 1)
    if name.strip().lower() != "retry-after":
        continue
    value = value.strip()
    try:
        seconds = max(int(float(value)), 1)
        seconds = min(seconds, 30)
    except Exception:
        seconds = 2
    print(seconds)
    raise SystemExit(0)
print(2)
PY
}

post_with_rate_limit_retry() {
  local url="$1"
  local payload="$2"
  local body_file="$3"
  local headers_file="$4"
  local stream_mode="${5:-0}"
  local max_attempts="${6:-5}"
  local http_code=""
  local retry_after=""
  local curl_args=(
    -sS
    --compressed
    --max-time 120
    -D "$headers_file"
    -o "$body_file"
    -X POST
    -H 'Content-Type: application/json'
    -H 'Authorization: Bearer clipal-placeholder-token'
    --data "$payload"
  )
  if [[ "$stream_mode" == "1" ]]; then
    curl_args+=(--no-buffer)
  fi
  last_request_headers="$headers_file"
  last_request_body="$body_file"
  register_artifacts "$headers_file" "$body_file"

  for attempt in $(seq 1 "$max_attempts"); do
    rm -f "$body_file" "$headers_file"
    if ! http_code="$(curl "${curl_args[@]}" "$url" -w '%{http_code}')"; then
      echo "curl request failed: $url" >&2
      return 1
    fi
    if [[ "$http_code" == "200" ]]; then
      return 0
    fi
    if [[ "$http_code" != "429" && "$http_code" != "503" ]]; then
      break
    fi
    if [[ "$attempt" == "$max_attempts" ]]; then
      break
    fi
    retry_after="$(read_retry_after "$headers_file")"
    echo "request rate limited (HTTP $http_code), retrying in ${retry_after}s [attempt $attempt/$max_attempts]" >&2
    sleep "$retry_after"
  done

  echo "request failed: POST $url -> HTTP ${http_code:-<unknown>}" >&2
  return 1
}

discover_credentials_json() {
  "$PY" - "$CONFIG_DIR" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1]).expanduser()
cred_dir = root / "oauth" / "claude"
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
    print("No Claude OAuth credentials found.")
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
        "error": "no Claude OAuth credential found under ~/.clipal/oauth/claude. Authorize one first in Web UI: Add Provider -> OAuth -> Claude"
    }))
    raise SystemExit(0)

if len(items) > 1:
    refs = ", ".join(sorted(item.get("ref") or item.get("basename") or "<unknown>" for item in items))
    print(json.dumps({
        "error": "multiple Claude OAuth credentials found; rerun with --oauth-ref or --oauth-file",
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

if [[ ! -f "$oauth_source_path" ]]; then
  echo "selected oauth credential file does not exist: $oauth_source_path" >&2
  exit 1
fi

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/clipal-live-claude-oauth.XXXXXXXX")"
cfgdir="$tmpdir/config"
mkdir -p "$cfgdir/oauth/claude"

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
    echo "" >&2
    echo "live claude oauth smoke failed" >&2
    if [[ "$KEEP_TEMP" == "1" ]]; then
      echo "temp dir preserved for debugging: $tmpdir" >&2
    else
      echo "temp dir will be removed; rerun with --keep-temp to preserve logs" >&2
    fi
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

credential_path="$cfgdir/oauth/claude/$(basename "$oauth_source_path")"
cp "$oauth_source_path" "$credential_path"
chmod 600 "$credential_path"
oauth_has_refresh="$("$PY" - "$credential_path" <<'PY'
import json
import pathlib
import sys
from datetime import datetime, timezone

path = pathlib.Path(sys.argv[1])
data = json.loads(path.read_text())
had_refresh = bool(str(data.get("refresh_token", "")).strip())
data.pop("refresh_token", None)
path.write_text(json.dumps(data, indent=2) + "\n")

expires_raw = str(data.get("expires_at", "")).strip()
if expires_raw:
    try:
        expires = datetime.fromisoformat(expires_raw.replace("Z", "+00:00"))
        if expires.tzinfo is None:
            expires = expires.replace(tzinfo=timezone.utc)
        if expires <= datetime.now(timezone.utc):
            raise SystemExit("selected credential access_token is expired; refusing to run live smoke without refresh_token")
    except ValueError as exc:
        raise SystemExit(f"parse selected credential expires_at: {exc}")

print("0")
if had_refresh:
    print("note: stripped refresh_token from temp credential to avoid token rotation", file=sys.stderr)
PY
)"

cat >"$cfgdir/config.yaml" <<YAML
listen_addr: 127.0.0.1
port: $clipal_port
log_level: $clipal_log_level
reactivate_after: 1h
YAML

cat >"$cfgdir/claude.yaml" <<YAML
mode: auto
pinned_provider: ""
providers:
  - name: "claude-live"
    auth_type: "oauth"
    oauth_provider: "claude"
    oauth_ref: "$oauth_ref"
    priority: 1
    enabled: true
YAML

chmod 600 "$cfgdir/config.yaml" "$cfgdir/claude.yaml"

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

claude_payload() {
  local prompt="$1"
  local stream="${2:-0}"
  "$PY" - "$MODEL" "$prompt" "$stream" "$claude_code_app_version" "$claude_billing_version_salt" <<'PY'
import json
import hashlib
import sys

model = sys.argv[1]
prompt = sys.argv[2]
stream = sys.argv[3] == "1"
claude_code_app_version = sys.argv[4]
claude_billing_version_salt = sys.argv[5]

def billing_version(text):
    encoded = text.encode("utf-16-le", "surrogatepass")
    picks = []
    for idx in (4, 7, 20):
        offset = idx * 2
        if offset + 1 < len(encoded):
            unit = int.from_bytes(encoded[offset:offset + 2], "little")
            picks.append("\ufffd" if 0xD800 <= unit <= 0xDFFF else chr(unit))
        else:
            picks.append("0")
    digest = hashlib.sha256((claude_billing_version_salt + "".join(picks) + claude_code_app_version).encode()).digest()
    suffix = (digest[0] << 4) | (digest[1] >> 4)
    return f"{claude_code_app_version}.{suffix:03x}"

payload = {
    "model": model,
    "max_tokens": 64,
    "system": [
        {
            "type": "text",
            "text": f"x-anthropic-billing-header: cc_version={billing_version(prompt)}; cc_entrypoint=sdk-cli; cch=00000;",
        },
        {
            "type": "text",
            "text": "You are Claude Code. Follow the user's requested exact output for this smoke test.",
        },
    ],
    "messages": [
        {
            "role": "user",
            "content": [{"type": "text", "text": prompt}],
        }
    ],
}
if stream:
    payload["stream"] = True
print(json.dumps(payload, ensure_ascii=False))
PY
}

claude_count_tokens_payload() {
  local prompt="$1"
  "$PY" - "$MODEL" "$prompt" "$claude_code_app_version" "$claude_billing_version_salt" <<'PY'
import json
import hashlib
import sys

model = sys.argv[1]
prompt = sys.argv[2]
claude_code_app_version = sys.argv[3]
claude_billing_version_salt = sys.argv[4]

def billing_version(text):
    encoded = text.encode("utf-16-le", "surrogatepass")
    picks = []
    for idx in (4, 7, 20):
        offset = idx * 2
        if offset + 1 < len(encoded):
            unit = int.from_bytes(encoded[offset:offset + 2], "little")
            picks.append("\ufffd" if 0xD800 <= unit <= 0xDFFF else chr(unit))
        else:
            picks.append("0")
    digest = hashlib.sha256((claude_billing_version_salt + "".join(picks) + claude_code_app_version).encode()).digest()
    suffix = (digest[0] << 4) | (digest[1] >> 4)
    return f"{claude_code_app_version}.{suffix:03x}"

payload = {
    "model": model,
    "system": [
        {
            "type": "text",
            "text": f"x-anthropic-billing-header: cc_version={billing_version(prompt)}; cc_entrypoint=sdk-cli; cch=00000;",
        },
        {
            "type": "text",
            "text": "You are Claude Code. Follow the user's requested exact output for this smoke test.",
        },
    ],
    "messages": [
        {
            "role": "user",
            "content": [{"type": "text", "text": prompt}],
        }
    ],
}
print(json.dumps(payload, ensure_ascii=False))
PY
}

run_message_check() {
  local prompt="$1"
  local expect_token="$2"
  local body_file="$3"
  local headers_file="$4"
  local payload
  payload="$(claude_payload "$prompt" 0)"
  post_with_rate_limit_retry \
    "http://127.0.0.1:$clipal_port/clipal/v1/messages" \
    "$payload" \
    "$body_file" \
    "$headers_file" \
    0

  "$PY" - "$body_file" "$expect_token" <<'PY'
import json
import re
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)

texts = []
for part in data.get("content", []) or []:
    if not isinstance(part, dict):
        continue
    text = part.get("text")
    if isinstance(text, str) and text.strip():
        texts.append(text.strip())

joined = "\n".join(texts)
if not joined:
    raise SystemExit(f"messages response returned no content text: {data}")

expect = re.sub(r"[^A-Z0-9]+", "", sys.argv[2].upper())
normalized = re.sub(r"[^A-Z0-9]+", "", joined.upper())
if expect and expect not in normalized:
    raise SystemExit(f"expected token {expect!r} in response text, got: {joined[:400]!r}")

message_id = str(data.get("id", "")).strip()
preview = joined[:120].replace("\n", " ")
print(f"ok id={message_id or '<none>'} text={preview}")
PY
}

run_stream_check() {
  local prompt="$1"
  local expect_token="$2"
  local body_file="$3"
  local headers_file="$4"
  local payload
  payload="$(claude_payload "$prompt" 1)"
  post_with_rate_limit_retry \
    "http://127.0.0.1:$clipal_port/clipal/v1/messages" \
    "$payload" \
    "$body_file" \
    "$headers_file" \
    1

  "$PY" - "$body_file" "$headers_file" "$expect_token" <<'PY'
import json
import pathlib
import re
import sys

body = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8", errors="replace")
headers = pathlib.Path(sys.argv[2]).read_text(encoding="utf-8", errors="replace").lower()
expect = re.sub(r"[^A-Z0-9]+", "", sys.argv[3].upper())

if "data:" not in body:
    raise SystemExit("stream response did not contain any SSE data frames")
if "text/event-stream" not in headers and "text/plain" not in headers:
    raise SystemExit("stream response missing expected SSE-compatible content-type")
if "message_stop" not in body and "[DONE]" not in body:
    raise SystemExit("stream response did not contain a recognizable completion marker")

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
    if event_type == "content_block_delta":
        delta = data.get("delta")
        if isinstance(delta, dict):
            text = delta.get("text")
            if isinstance(text, str) and text.strip():
                texts.append(text.strip())
    elif event_type == "content_block_start":
        block = data.get("content_block")
        if isinstance(block, dict):
            text = block.get("text")
            if isinstance(text, str) and text.strip():
                texts.append(text.strip())
    elif event_type == "message_start":
        message = data.get("message")
        if isinstance(message, dict):
            for part in message.get("content", []) or []:
                if not isinstance(part, dict):
                    continue
                text = part.get("text")
                if isinstance(text, str) and text.strip():
                    texts.append(text.strip())

joined = "".join(texts)
if not joined:
    raise SystemExit(f"stream response returned no text deltas: {body[:400]!r}")

normalized = re.sub(r"[^A-Z0-9]+", "", joined.upper())
if expect and expect not in normalized:
    raise SystemExit(f"expected token {expect!r} in stream response, got: {body[:400]!r}")

print("ok stream completed")
PY
}

run_count_tokens_check() {
  local prompt="$1"
  local body_file="$2"
  local headers_file="$3"
  local payload
  payload="$(claude_count_tokens_payload "$prompt")"
  post_with_rate_limit_retry \
    "http://127.0.0.1:$clipal_port/clipal/v1/messages/count_tokens" \
    "$payload" \
    "$body_file" \
    "$headers_file" \
    0

  "$PY" - "$body_file" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as f:
    data = json.load(f)

tokens = data.get("input_tokens")
if not isinstance(tokens, int) or tokens <= 0:
    raise SystemExit(f"count_tokens returned invalid input_tokens: {data}")
print(f"ok count_tokens input_tokens={tokens}")
PY
}

echo "test: live /v1/messages"
run_message_check \
  "Reply with exactly LIVEOK and nothing else." \
  "LIVEOK" \
  "$tmpdir/messages.json" \
  "$tmpdir/messages.headers"

if [[ "$SKIP_STREAM" != "1" ]]; then
  echo "test: live streaming /v1/messages"
  run_stream_check \
    "Reply with exactly STREAMOK and nothing else." \
    "STREAMOK" \
    "$tmpdir/stream.sse" \
    "$tmpdir/stream.headers"
fi

if [[ "$SKIP_COUNT_TOKENS" != "1" ]]; then
  echo "test: live /v1/messages/count_tokens"
  run_count_tokens_check \
    "Count tokens for this compact smoke test." \
    "$tmpdir/count_tokens.json" \
    "$tmpdir/count_tokens.headers"
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

    run_message_check \
      "Reply with exactly REFRESHOK and nothing else." \
      "REFRESHOK" \
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
echo "live claude oauth smoke passed"
echo "temp dir: $tmpdir"
echo "logs: $tmpdir/clipal.log"
if [[ "$KEEP_TEMP" != "1" ]]; then
  echo "temp dir will be removed on exit; rerun with --keep-temp to preserve artifacts"
fi
