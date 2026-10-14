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
  ./scripts/oauth_authorize_smoke.sh [options]

Modes:
  Default is --live
  --mock uses the built-in mock OAuth server for fully automatic verification

Options:
  --mock                     Use the built-in mock OAuth server
  --live                     Use the real Codex OAuth server
  --keep-temp                Keep the temporary config dir and logs after success
  --open-browser             In --live mode, try to open the authorization URL automatically
  --config-dir DIR           Source config dir for live mode artifacts if needed (default: ~/.clipal, informational only)
  --timeout SECONDS          Poll timeout for login completion (default: 120 in mock mode, 600 in live mode)
  -h, --help                 Show this help

Examples:
  ./scripts/oauth_authorize_smoke.sh
  ./scripts/oauth_authorize_smoke.sh --open-browser
  ./scripts/oauth_authorize_smoke.sh --mock
EOF
}

MODE="live"
KEEP_TEMP=0
OPEN_BROWSER=0
CONFIG_DIR="${CLIPAL_LIVE_CONFIG_DIR:-$HOME/.clipal}"
TIMEOUT_SECONDS=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mock)
      MODE="mock"
      shift
      ;;
    --live)
      MODE="live"
      shift
      ;;
    --keep-temp)
      KEEP_TEMP=1
      shift
      ;;
    --open-browser)
      OPEN_BROWSER=1
      shift
      ;;
    --config-dir)
      CONFIG_DIR="${2:-}"
      shift 2
      ;;
    --timeout)
      TIMEOUT_SECONDS="${2:-}"
      shift 2
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

if [[ -z "$TIMEOUT_SECONDS" ]]; then
  if [[ "$MODE" == "live" ]]; then
    TIMEOUT_SECONDS=600
  else
    TIMEOUT_SECONDS=120
  fi
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

wait_tcp_open() {
  local host="$1"
  local port="$2"
  local tries="${3:-120}"
  local delay="${4:-0.25}"
  for _ in $(seq 1 "$tries"); do
    if "$PY" - "$host" "$port" <<'PY' >/dev/null 2>&1
import socket
import sys

host = sys.argv[1]
port = int(sys.argv[2])
sock = socket.socket()
sock.settimeout(0.5)
try:
    sock.connect((host, port))
except OSError:
    raise SystemExit(1)
finally:
    sock.close()
PY
    then
      return 0
    fi
    sleep "$delay"
  done
  echo "timeout waiting for tcp listener: $host:$port" >&2
  return 1
}

ensure_live_callback_port_available() {
  local port=1455
  if command -v lsof >/dev/null 2>&1; then
    local output
    output="$(lsof -nP -iTCP:${port} -sTCP:LISTEN 2>/dev/null || true)"
    if [[ -n "$output" ]]; then
      echo "live Codex OAuth uses the fixed callback http://localhost:${port}/auth/callback" >&2
      echo "port ${port} is already in use; stop the conflicting process and retry." >&2
      echo "$output" >&2
      exit 1
    fi
  fi
}

api_call() {
  local method="$1"
  local url="$2"
  local body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -fsS -X "$method" \
      -H 'X-Clipal-UI: 1' \
      -H 'Content-Type: application/json' \
      --data "$body" \
      "$url"
  else
    curl -fsS -X "$method" \
      -H 'X-Clipal-UI: 1' \
      "$url"
  fi
}

maybe_open_browser() {
  local url="$1"
  if [[ "$OPEN_BROWSER" != "1" ]]; then
    return 0
  fi
  if command -v open >/dev/null 2>&1; then
    open "$url" >/dev/null 2>&1 || true
    return 0
  fi
  if command -v xdg-open >/dev/null 2>&1; then
    xdg-open "$url" >/dev/null 2>&1 || true
    return 0
  fi
  if command -v start >/dev/null 2>&1; then
    start "$url" >/dev/null 2>&1 || true
  fi
}

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/clipal-oauth-authorize.XXXXXXXX")"
cfgdir="$tmpdir/config"
mkdir -p "$cfgdir"
chmod 700 "$cfgdir"

clipal_port="$(get_free_port)"
mock_port=""
clipal_pid=""
mock_pid=""

cleanup() {
  set +e
  if [[ -n "${clipal_pid:-}" ]]; then
    kill "$clipal_pid" >/dev/null 2>&1 || true
    wait "$clipal_pid" >/dev/null 2>&1 || true
  fi
  if [[ -n "${mock_pid:-}" ]]; then
    kill "$mock_pid" >/dev/null 2>&1 || true
    wait "$mock_pid" >/dev/null 2>&1 || true
  fi
  if [[ "$KEEP_TEMP" != "1" ]]; then
    rm -rf "$tmpdir" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

cat >"$cfgdir/config.yaml" <<YAML
listen_addr: 127.0.0.1
port: $clipal_port
log_level: debug
reactivate_after: 1h
YAML
chmod 600 "$cfgdir/config.yaml"

if [[ "$MODE" == "mock" ]]; then
  mock_port="$(get_free_port)"
  cat >"$tmpdir/mock_oauth.py" <<'PY'
import base64
import json
import os
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlencode, urlparse

EMAIL = "sean@example.com"
ACCOUNT_ID = "acct_123"
AUTH_CODE = "mock-auth-code"
REFRESH_TOKEN = "mock-refresh-token"
ACCESS_TOKEN = "mock-access-token"
CLIENT_ID = os.environ.get("MOCK_OAUTH_CLIENT_ID", "clipal-dev-client")

def b64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).decode("ascii").rstrip("=")

def id_token(email: str, account_id: str) -> str:
    header = {"alg": "none", "typ": "JWT"}
    payload = {
        "email": email,
        "sub": account_id,
        "https://api.openai.com/auth": {
            "chatgpt_account_id": account_id,
        },
    }
    return b64url(json.dumps(header, separators=(",", ":")).encode("utf-8")) + "." + b64url(json.dumps(payload, separators=(",", ":")).encode("utf-8")) + "."

class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        sys.stderr.write(fmt % args)
        sys.stderr.write("\n")

    def do_GET(self):
        parsed = urlparse(self.path)
        if parsed.path == "/oauth/authorize":
            query = parse_qs(parsed.query)
            redirect_uri = query.get("redirect_uri", [""])[0]
            state = query.get("state", [""])[0]
            response_type = query.get("response_type", [""])[0]
            code_challenge = query.get("code_challenge", [""])[0]
            client_id = query.get("client_id", [""])[0]
            if not redirect_uri or not state or response_type != "code" or not code_challenge or client_id != CLIENT_ID:
                self.send_response(400)
                self.end_headers()
                self.wfile.write(b"invalid authorize request")
                return
            target = redirect_uri + "?" + urlencode({"code": AUTH_CODE, "state": state})
            self.send_response(302)
            self.send_header("Location", target)
            self.end_headers()
            return

        self.send_response(404)
        self.end_headers()

    def do_POST(self):
        if self.path != "/oauth/token":
            self.send_response(404)
            self.end_headers()
            return
        length = int(self.headers.get("Content-Length", "0") or "0")
        raw = self.rfile.read(length).decode("utf-8")
        form = parse_qs(raw)
        grant_type = form.get("grant_type", [""])[0]
        client_id = form.get("client_id", [""])[0]
        if client_id != CLIENT_ID:
            self.send_response(400)
            self.end_headers()
            self.wfile.write(b"invalid client_id")
            return
        if grant_type == "authorization_code":
            code = form.get("code", [""])[0]
            code_verifier = form.get("code_verifier", [""])[0]
            redirect_uri = form.get("redirect_uri", [""])[0]
            if code != AUTH_CODE or not code_verifier or not redirect_uri:
                self.send_response(400)
                self.end_headers()
                self.wfile.write(b"invalid authorization_code exchange")
                return
        elif grant_type == "refresh_token":
            refresh_token = form.get("refresh_token", [""])[0]
            if refresh_token != REFRESH_TOKEN:
                self.send_response(400)
                self.end_headers()
                self.wfile.write(b"invalid refresh_token")
                return
        else:
            self.send_response(400)
            self.end_headers()
            self.wfile.write(b"unsupported grant_type")
            return

        body = {
            "access_token": ACCESS_TOKEN,
            "refresh_token": REFRESH_TOKEN,
            "id_token": id_token(EMAIL, ACCOUNT_ID),
            "expires_in": 3600,
        }
        encoded = json.dumps(body).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

port = int(os.environ["MOCK_OAUTH_PORT"])
server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
server.serve_forever()
PY

  export MOCK_OAUTH_PORT="$mock_port"
  export MOCK_OAUTH_CLIENT_ID="clipal-dev-client"
  "$PY" "$tmpdir/mock_oauth.py" >"$tmpdir/mock-oauth.log" 2>&1 &
  mock_pid="$!"
  wait_tcp_open "127.0.0.1" "$mock_port"
fi

if [[ "$MODE" == "live" ]]; then
  ensure_live_callback_port_available
fi

echo "building clipal..."
(cd "$repo_root" && go build -o "$tmpdir/clipal" ./cmd/clipal)

echo "starting clipal on 127.0.0.1:$clipal_port ..."
if [[ "$MODE" == "mock" ]]; then
  env \
    "CLIPAL_OAUTH_CODEX_AUTH_URL=http://127.0.0.1:$mock_port/oauth/authorize" \
    "CLIPAL_OAUTH_CODEX_TOKEN_URL=http://127.0.0.1:$mock_port/oauth/token" \
    "CLIPAL_OAUTH_CODEX_CLIENT_ID=clipal-dev-client" \
    "CLIPAL_OAUTH_CODEX_CALLBACK_HOST=127.0.0.1" \
    "CLIPAL_OAUTH_CODEX_CALLBACK_PORT=0" \
    "CLIPAL_OAUTH_CODEX_CALLBACK_PATH=/auth/callback" \
    "$tmpdir/clipal" --config-dir "$cfgdir" --listen-addr 127.0.0.1 --port "$clipal_port" --log-level debug >"$tmpdir/clipal.log" 2>&1 &
else
  "$tmpdir/clipal" --config-dir "$cfgdir" --listen-addr 127.0.0.1 --port "$clipal_port" --log-level debug >"$tmpdir/clipal.log" 2>&1 &
fi
clipal_pid="$!"
if ! wait_http_ok "http://127.0.0.1:$clipal_port/health"; then
  echo "---- clipal.log ----" >&2
  if [[ -f "$tmpdir/clipal.log" ]]; then
    cat "$tmpdir/clipal.log" >&2 || true
  else
    echo "clipal log file not created: $tmpdir/clipal.log" >&2
  fi
  exit 1
fi

echo "test: start oauth authorization"
start_response_file="$tmpdir/oauth-start.json"
start_http_code="$(curl -sS -o "$start_response_file" -w '%{http_code}' \
  -X POST \
  -H 'X-Clipal-UI: 1' \
  -H 'Content-Type: application/json' \
  --data '{"client_type":"openai","provider":"codex"}' \
  "http://127.0.0.1:$clipal_port/api/oauth/providers/start")"
start_json="$(cat "$start_response_file")"
if [[ "$start_http_code" != "200" ]]; then
  echo "oauth start failed with status $start_http_code: $start_json" >&2
  echo "---- clipal.log ----" >&2
  cat "$tmpdir/clipal.log" >&2 || true
  exit 1
fi
session_id="$("$PY" - "$start_json" <<'PY'
import json, sys
obj = json.loads(sys.argv[1])
print(obj.get("session_id", ""))
PY
)"
auth_url="$("$PY" - "$start_json" <<'PY'
import json, sys
obj = json.loads(sys.argv[1])
print(obj.get("auth_url", ""))
PY
)"
if [[ -z "$session_id" || -z "$auth_url" ]]; then
  echo "failed to parse start response: $start_json" >&2
  exit 1
fi
echo "session: $session_id"

if [[ "$MODE" == "mock" ]]; then
  echo "test: follow mock authorize redirect into clipal callback"
  curl -fsS -L "$auth_url" >/dev/null
else
  echo "complete the live OAuth login in your browser:"
  echo "authorization url: $auth_url"
  maybe_open_browser "$auth_url"
fi

echo "test: poll oauth session until authorization completes"
deadline=$(( $(date +%s) + TIMEOUT_SECONDS ))
session_json=""
while true; do
  session_json="$(curl -fsS "http://127.0.0.1:$clipal_port/api/oauth/sessions/$session_id")"
  status="$("$PY" - "$session_json" <<'PY'
import json, sys
obj = json.loads(sys.argv[1])
print(obj.get("status", ""))
PY
)"
  if [[ "$status" == "completed" ]]; then
    break
  fi
  if [[ "$status" == "error" || "$status" == "expired" ]]; then
    echo "oauth session failed: $session_json" >&2
    exit 1
  fi
  if [[ "$(date +%s)" -ge "$deadline" ]]; then
    echo "oauth session timed out: $session_json" >&2
    exit 1
  fi
  sleep 1
done

echo "test: explicitly link the authorized account to its provider"
session_json="$(curl -fsS \
  -X POST \
  -H 'X-Clipal-UI: 1' \
  -H 'Content-Type: application/json' \
  --data '{"client_type":"openai"}' \
  "http://127.0.0.1:$clipal_port/api/oauth/sessions/$session_id/link")"

provider_name="$("$PY" - "$session_json" <<'PY'
import json, sys
obj = json.loads(sys.argv[1])
print(obj.get("provider_name", ""))
PY
)"
display_name="$("$PY" - "$session_json" <<'PY'
import json, sys
obj = json.loads(sys.argv[1])
print(obj.get("display_name", ""))
PY
)"
credential_ref="$("$PY" - "$session_json" <<'PY'
import json, sys
obj = json.loads(sys.argv[1])
print(obj.get("credential_ref", ""))
PY
)"

if [[ -z "$provider_name" || -z "$credential_ref" ]]; then
  echo "oauth completion response missing provider data: $session_json" >&2
  exit 1
fi

echo "authorized provider: $provider_name"
if [[ -n "$display_name" ]]; then
  echo "display name: $display_name"
fi

echo "test: verify provider list"
providers_json="$(curl -fsS "http://127.0.0.1:$clipal_port/api/providers/openai")"
"$PY" - "$providers_json" "$provider_name" "$credential_ref" <<'PY'
import json
import sys

providers = json.loads(sys.argv[1])
want_name = sys.argv[2]
want_ref = sys.argv[3]
if not isinstance(providers, list) or len(providers) != 1:
    raise SystemExit(f"expected exactly one provider, got: {providers!r}")
provider = providers[0]
if provider.get("name") != want_name:
    raise SystemExit(f"provider name mismatch: {provider.get('name')!r} != {want_name!r}")
if str(provider.get("auth_type", "")).lower() != "oauth":
    raise SystemExit(f"provider auth_type mismatch: {provider.get('auth_type')!r}")
if str(provider.get("oauth_provider", "")).lower() != "codex":
    raise SystemExit(f"provider oauth_provider mismatch: {provider.get('oauth_provider')!r}")
if provider.get("oauth_ref") != want_ref:
    raise SystemExit(f"provider oauth_ref mismatch: {provider.get('oauth_ref')!r} != {want_ref!r}")
print("ok provider api response")
PY

echo "test: verify config and credential were written"
"$PY" - "$cfgdir/openai.yaml" "$cfgdir/oauth/codex" "$provider_name" "$credential_ref" "$display_name" <<'PY'
import json
import pathlib
import re
import sys

openai_yaml = pathlib.Path(sys.argv[1])
oauth_dir = pathlib.Path(sys.argv[2])
want_provider = sys.argv[3]
want_ref = sys.argv[4]
want_display = sys.argv[5]

body = openai_yaml.read_text(encoding="utf-8")
patterns = [
    rf'(?m)^\s*-\s*name:\s*"?{re.escape(want_provider)}"?\s*$',
    r'(?m)^\s*auth_type:\s*"?oauth"?\s*$',
    r'(?m)^\s*oauth_provider:\s*"?codex"?\s*$',
    rf'(?m)^\s*oauth_ref:\s*"?{re.escape(want_ref)}"?\s*$',
]
for pattern in patterns:
    if not re.search(pattern, body):
        raise SystemExit(f"missing pattern {pattern!r} in openai.yaml:\n{body}")

files = sorted(oauth_dir.glob("*.json"))
if len(files) != 1:
    raise SystemExit(f"expected exactly one oauth credential file, got {[f.name for f in files]!r}")
data = json.loads(files[0].read_text(encoding="utf-8"))
if data.get("provider") != "codex":
    raise SystemExit(f"credential provider mismatch: {data.get('provider')!r}")
if data.get("ref") != want_ref:
    raise SystemExit(f"credential ref mismatch: {data.get('ref')!r} != {want_ref!r}")
if want_display and data.get("email") != want_display:
    raise SystemExit(f"credential email mismatch: {data.get('email')!r} != {want_display!r}")
if not str(data.get("access_token", "")).strip():
    raise SystemExit("credential access_token missing")
print("ok config and credential persisted")
PY

echo ""
echo "oauth authorization smoke passed"
echo "mode: $MODE"
echo "temp dir: $tmpdir"
echo "logs: $tmpdir/clipal.log"
if [[ "$MODE" == "mock" ]]; then
  echo "mock oauth log: $tmpdir/mock-oauth.log"
fi
if [[ "$KEEP_TEMP" != "1" ]]; then
  echo "temp dir will be removed on exit; rerun with --keep-temp to preserve artifacts"
fi
