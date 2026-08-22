package proxy

import "testing"

func TestClaudeOAuthMetadataMatchesClaudeCodeRelease(t *testing.T) {
	if claudeOAuthAppVersion != "2.1.240" {
		t.Fatalf("claudeOAuthAppVersion = %q, want 2.1.240", claudeOAuthAppVersion)
	}
	if claudeOAuthUserAgent != "claude-cli/2.1.240 (external, sdk-cli)" {
		t.Fatalf("claudeOAuthUserAgent = %q", claudeOAuthUserAgent)
	}
	if claudeOAuthStainlessTimeout != "300" {
		t.Fatalf("claudeOAuthStainlessTimeout = %q, want 300", claudeOAuthStainlessTimeout)
	}
	if claudeOAuthStainlessRuntimeVersion != "v26.3.0" {
		t.Fatalf("claudeOAuthStainlessRuntimeVersion = %q, want v26.3.0", claudeOAuthStainlessRuntimeVersion)
	}
	if got := claudeOAuthBillingVersionForText("hello"); got != "2.1.240.2d1" {
		t.Fatalf("claudeOAuthBillingVersionForText(hello) = %q, want 2.1.240.2d1", got)
	}
}
