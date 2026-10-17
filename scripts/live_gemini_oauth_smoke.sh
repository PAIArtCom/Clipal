#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

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
  ./scripts/live_gemini_oauth_smoke.sh [options]

Options:
  --config-dir DIR          Source config dir to discover OAuth credentials from (default: ~/.clipal)
  --email EMAIL             Select OAuth credential by email
  --oauth-ref REF           Specific OAuth ref to test
  --oauth-file FILE         Specific credential file to copy into the temporary config
  --model MODEL             Require a listed text model (default: discover from /models)
  --base-url URL            Test an already running service; do not copy credentials or force refresh
  --provider NAME           Temporarily pin this Gemini provider with --base-url
  --skip-stream             Skip the streaming :streamGenerateContent smoke
  --skip-refresh-retry      Skip the forced 401 -> refresh -> retry smoke
  --keep-temp               Keep the temporary config dir and logs after success
  --list                    List discoverable Gemini OAuth credentials and exit
  -h, --help                Show this help

Models are discovered before generation; explicitly retired candidates are skipped.
Success requires the expected text and a terminal STOP in ordinary and SSE responses.
With --base-url, the running service is used and routing is restored after testing.
Loopback service URLs bypass proxies; remote URLs retain environment/system proxy support.
Without it, a temporary service and credential copy exercise refresh retry as well.

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
MODEL="${CLIPAL_LIVE_MODEL:-}"
BASE_URL=""
PROVIDER=""
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
    --base-url)
      BASE_URL="${2:-}"
      shift 2
      ;;
    --provider)
      PROVIDER="${2:-}"
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
    echo "set CLIPAL_LIVE_VERBOSE=1 to print clipal.log tail" >&2
    return
  fi
  if [[ -f "$tmpdir/clipal.log" ]]; then
    echo "---- last 200 lines of clipal.log ----" >&2
    tail -n 200 "$tmpdir/clipal.log" >&2 || true
  fi

}

discover_credentials_json() {
  "$PY" - "$CONFIG_DIR" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1]).expanduser()
cred_dir = root / "oauth" / "gemini"
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
    print("No Gemini OAuth credentials found.")
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

# Both installed-service and isolated credential tests share discovery and validation.
run_live_checks() {
  local base_url="$1"
  shift
  local args=(--base-url "$base_url")
  if [[ -n "$MODEL" ]]; then args+=(--model "$MODEL"); fi
  if [[ "$SKIP_STREAM" == "1" ]]; then args+=(--skip-stream); fi
  "$PY" "$repo_root/scripts/gemini_live.py" "${args[@]}" "$@"
}

if [[ -n "$BASE_URL" ]]; then
  if [[ -n "$PROVIDER" ]]; then
    run_live_checks "$BASE_URL" --provider "$PROVIDER"
  else
    run_live_checks "$BASE_URL"
  fi
  exit 0
fi
if [[ -n "$PROVIDER" ]]; then
  echo "--provider requires --base-url" >&2
  exit 2
fi
require_cmd go

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
        "error": "no Gemini OAuth credential found under ~/.clipal/oauth/gemini. Authorize one first in Web UI: Add Provider -> OAuth -> Gemini"
    }))
    raise SystemExit(0)

if len(items) > 1:
    refs = ", ".join(sorted(item.get("ref") or item.get("basename") or "<unknown>" for item in items))
    print(json.dumps({
        "error": "multiple Gemini OAuth credentials found; rerun with --oauth-ref or --oauth-file",
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

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/clipal-live-gemini-oauth.XXXXXXXX")"
cfgdir="$tmpdir/config"
mkdir -p "$cfgdir/oauth/gemini"

clipal_port="$(get_free_port)"
clipal_pid=""
declare -a failure_artifacts=()
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
    echo "live gemini oauth smoke failed" >&2
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

credential_path="$cfgdir/oauth/gemini/$(basename "$oauth_source_path")"
cp "$oauth_source_path" "$credential_path"
chmod 600 "$credential_path"

cat >"$cfgdir/config.yaml" <<YAML
listen_addr: 127.0.0.1
port: $clipal_port
log_level: $clipal_log_level
reactivate_after: 1h
YAML

cat >"$cfgdir/gemini.yaml" <<YAML
mode: auto
pinned_provider: ""
providers:
  - name: "gemini-live"
    auth_type: "oauth"
    oauth_provider: "gemini"
    oauth_ref: "$oauth_ref"
    priority: 1
    enabled: true
YAML

chmod 600 "$cfgdir/config.yaml" "$cfgdir/gemini.yaml"

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

echo "test: discover models, live generateContent and streamGenerateContent"
register_artifacts "$tmpdir/live.json"
run_live_checks "http://127.0.0.1:$clipal_port" >"$tmpdir/live.json"
cat "$tmpdir/live.json"
MODEL="$("$PY" - "$tmpdir/live.json" <<'PYMODEL'
import json
import sys
with open(sys.argv[1]) as response:
    print(json.load(response)["selected_model"])
PYMODEL
)"

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

    register_artifacts "$tmpdir/refresh.json"
    "$PY" "$repo_root/scripts/gemini_live.py" \
      --base-url "http://127.0.0.1:$clipal_port" --model "$MODEL" \
      --skip-stream --token REFRESHOK >"$tmpdir/refresh.json"
    cat "$tmpdir/refresh.json"

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
echo "live gemini oauth smoke passed"
echo "temp dir: $tmpdir"
echo "logs: $tmpdir/clipal.log"
if [[ "$KEEP_TEMP" != "1" ]]; then
  echo "temp dir will be removed on exit; rerun with --keep-temp to preserve artifacts"
fi
