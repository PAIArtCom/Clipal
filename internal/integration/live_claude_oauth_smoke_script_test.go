package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestLiveClaudeOAuthSmokeScriptUsesTemporaryCredentialCopyAndRefreshProbe(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../scripts/live_claude_oauth_smoke.sh")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	script := string(body)
	for _, want := range []string{
		`--email EMAIL`,
		`default: claude-sonnet-4-6`,
		`--skip-count-tokens`,
		`CLIPAL_LIVE_OAUTH_EMAIL`,
		`CLIPAL_LIVE_SKIP_COUNT_TOKENS`,
		`CLIPAL_LIVE_VERBOSE`,
		`list_credentials "$creds_json" "$OAUTH_EMAIL" "$OAUTH_REF" "$OAUTH_FILE"`,
		`cfgdir="$tmpdir/config"`,
		`oauth email not found`,
		`credential_path="$cfgdir/oauth/claude/$(basename "$oauth_source_path")"`,
		`data.pop("refresh_token", None)`,
		`stripped refresh_token from temp credential to avoid token rotation`,
		`selected credential access_token is expired; refusing to run live smoke without refresh_token`,
		`clipal_log_level="info"`,
		`clipal_log_level="debug"`,
		`log_level: $clipal_log_level`,
		`--log-level "$clipal_log_level"`,
		`artifacts:`,
		`temp dir will be removed; rerun with --keep-temp to preserve logs`,
		`set CLIPAL_LIVE_VERBOSE=1 to print clipal.log tail and request headers/body`,
		`seconds = min(seconds, 30)`,
		`auth_type: "oauth"`,
		`oauth_provider: "claude"`,
		`"http://127.0.0.1:$clipal_port/clipal/v1/messages"`,
		`"http://127.0.0.1:$clipal_port/clipal/v1/messages/count_tokens"`,
		`def billing_version(text):`,
		`x-anthropic-billing-header: cc_version={billing_version(prompt)}; cc_entrypoint=sdk-cli; cch=00000;`,
		`clipal-live-invalid-token`,
		`credential access_token was not replaced after refresh retry`,
		`ok refreshed temp credential updated`,
		`clipal-live-claude-oauth`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("live claude oauth smoke script missing %q", want)
		}
	}
	for _, unwanted := range []string{
		`sync_updated_credential_back`,
		`synced updated oauth credential back to source`,
		`request artifacts: $headers_file $body_file`,
		`log_level: debug`,
		`--log-level debug`,
		`ok refreshed credential persisted`,
	} {
		if strings.Contains(script, unwanted) {
			t.Fatalf("live claude oauth smoke script should not contain %q", unwanted)
		}
	}

	py, err := exec.LookPath("python3")
	if err != nil {
		py, err = exec.LookPath("python")
	}
	if err != nil {
		t.Skip("python is not available")
	}

	payload := runClaudeSmokePayloadPython(t, py, script, "claude_payload()", "hello")
	billing := claudeSmokeBillingText(t, payload)
	if !strings.Contains(billing, "cc_version=2.1.196.68b; cc_entrypoint=sdk-cli; cch=00000;") {
		t.Fatalf("billing system block = %q", billing)
	}
	if got := payload["stream"]; got == true {
		t.Fatalf("stream = %v, want absent or false", got)
	}

	countTokensPayload := runClaudeSmokePayloadPython(t, py, script, "claude_count_tokens_payload()", "hello")
	countTokensBilling := claudeSmokeBillingText(t, countTokensPayload)
	if !strings.Contains(countTokensBilling, "cc_version=2.1.196.68b; cc_entrypoint=sdk-cli; cch=00000;") {
		t.Fatalf("count_tokens billing system block = %q", countTokensBilling)
	}

	emojiPayload := runClaudeSmokePayloadPython(t, py, script, "claude_payload()", "hello 🌍")
	emojiBilling := claudeSmokeBillingText(t, emojiPayload)
	if !strings.Contains(emojiBilling, "cc_version=2.1.196.") {
		t.Fatalf("emoji billing system block = %q", emojiBilling)
	}
}

func claudeSmokeBillingText(t *testing.T, payload map[string]any) string {
	t.Helper()

	system, ok := payload["system"].([]any)
	if !ok || len(system) == 0 {
		t.Fatalf("payload system = %#v, want non-empty array", payload["system"])
	}
	block, ok := system[0].(map[string]any)
	if !ok {
		t.Fatalf("payload system[0] = %#v, want object", system[0])
	}
	text, ok := block["text"].(string)
	if !ok {
		t.Fatalf("payload system[0].text = %#v, want string", block["text"])
	}
	return text
}

func runClaudeSmokePayloadPython(t *testing.T, py string, script string, anchor string, prompt string) map[string]any {
	t.Helper()

	pythonScript := extractSmokeHeredoc(t, script, anchor)
	args := []string{"-", "claude-sonnet-4-6", prompt}
	if anchor == "claude_payload()" {
		args = append(args, "0")
	}
	cmd := exec.Command(py, args...)
	cmd.Stdin = strings.NewReader(pythonScript)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("payload python failed: %v stderr=%s", err, string(exitErr.Stderr))
		}
		t.Fatalf("payload python failed: %v", err)
	}
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(out))
	if err := decoder.Decode(&root); err != nil {
		t.Fatalf("payload json decode failed: %v body=%s", err, string(out))
	}
	return root
}

func extractSmokeHeredoc(t *testing.T, script string, anchor string) string {
	t.Helper()

	anchorAt := strings.Index(script, anchor)
	if anchorAt < 0 {
		t.Fatalf("script missing anchor %q", anchor)
	}
	startMarker := "<<'PY'\n"
	start := strings.Index(script[anchorAt:], startMarker)
	if start < 0 {
		t.Fatalf("script missing python heredoc after %q", anchor)
	}
	start += anchorAt + len(startMarker)
	end := strings.Index(script[start:], "\nPY\n")
	if end < 0 {
		t.Fatalf("script missing python heredoc terminator after %q", anchor)
	}
	return script[start : start+end]
}
