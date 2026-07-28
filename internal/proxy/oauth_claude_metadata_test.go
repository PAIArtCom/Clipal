package proxy

import "testing"

func TestClaudeOAuthMetadataMatchesClaudeCodeRelease(t *testing.T) {
	if claudeOAuthAppVersion != "2.1.220" {
		t.Fatalf("claudeOAuthAppVersion = %q, want 2.1.220", claudeOAuthAppVersion)
	}
	if claudeOAuthUserAgent != "claude-cli/2.1.220 (external, sdk-cli)" {
		t.Fatalf("claudeOAuthUserAgent = %q", claudeOAuthUserAgent)
	}
	if claudeOAuthStainlessTimeout != "300" {
		t.Fatalf("claudeOAuthStainlessTimeout = %q, want 300", claudeOAuthStainlessTimeout)
	}
	if claudeOAuthStainlessRuntimeVersion != "v26.3.0" {
		t.Fatalf("claudeOAuthStainlessRuntimeVersion = %q, want v26.3.0", claudeOAuthStainlessRuntimeVersion)
	}
	if got := claudeOAuthBillingVersionForText("hello"); got != "2.1.220.5bd" {
		t.Fatalf("claudeOAuthBillingVersionForText(hello) = %q, want 2.1.220.5bd", got)
	}
}
