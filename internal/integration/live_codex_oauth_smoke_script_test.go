package integration

import (
	"os"
	"strings"
	"testing"
)

func TestLiveCodexOAuthSmokeScriptUsesTemporaryCredentialCopyAndRefreshProbe(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile("../../scripts/live_codex_oauth_smoke.sh")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	script := string(body)
	for _, want := range []string{
		`--email EMAIL`,
		`default: gpt-6-sol`,
		`CLIPAL_LIVE_OAUTH_EMAIL`,
		`CLIPAL_LIVE_VERBOSE`,
		`list_credentials "$creds_json" "$OAUTH_EMAIL" "$OAUTH_REF" "$OAUTH_FILE"`,
		`cfgdir="$tmpdir/config"`,
		`oauth email not found`,
		`credential_path="$cfgdir/oauth/codex/$(basename "$oauth_source_path")"`,
		`clipal_log_level="info"`,
		`clipal_log_level="debug"`,
		`log_level: $clipal_log_level`,
		`--log-level "$clipal_log_level"`,
		`artifacts:`,
		`set CLIPAL_LIVE_VERBOSE=1 to print clipal.log tail and request headers/body`,
		`auth_type: "oauth"`,
		`oauth_provider: "codex"`,
		`response.output_text.delta`,
		`"http://127.0.0.1:$clipal_port/clipal/v1/responses"`,
		`clipal-live-invalid-token`,
		`credential access_token was not replaced after refresh retry`,
		`ok refreshed temp credential updated`,
		`clipal-live-codex-oauth`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("live codex oauth smoke script missing %q", want)
		}
	}
	for _, unwanted := range []string{
		`sync_updated_credential_back`,
		`synced updated oauth credential back to source`,
		`log_level: debug`,
		`--log-level debug`,
		`ok refreshed credential persisted`,
	} {
		if strings.Contains(script, unwanted) {
			t.Fatalf("live codex oauth smoke script should not contain %q", unwanted)
		}
	}
}
