package integration

import (
	"os"
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
		`clipal_log_level="info"`,
		`clipal_log_level="debug"`,
		`log_level: $clipal_log_level`,
		`--log-level "$clipal_log_level"`,
		`artifacts:`,
		`set CLIPAL_LIVE_VERBOSE=1 to print clipal.log tail and request headers/body`,
		`auth_type: "oauth"`,
		`oauth_provider: "claude"`,
		`"http://127.0.0.1:$clipal_port/clipal/v1/messages"`,
		`"http://127.0.0.1:$clipal_port/clipal/v1/messages/count_tokens"`,
		`x-anthropic-billing-header: cc_version=2.1.159.3e1; cc_entrypoint=cli; cch=00000;`,
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
}
