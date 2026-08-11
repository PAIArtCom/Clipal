package oauth

import (
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestSameAccountIdentity_AllowsLegacyCodexCredentialWithoutAccountID(t *testing.T) {
	legacy := &Credential{
		Ref:      "codex-legacy-ref",
		Provider: config.OAuthProviderCodex,
		Email:    "sean@example.com",
	}
	incoming := &Credential{
		Ref:       "codex-sean-example-com",
		Provider:  config.OAuthProviderCodex,
		Email:     "sean@example.com",
		AccountID: "acct_123",
	}

	if !SameAccountIdentity(legacy, incoming) {
		t.Fatalf("expected legacy and incoming codex credentials to match")
	}
}

func TestCredentialUpdateMatchScore_RejectsCodexRefCollisionAcrossAccounts(t *testing.T) {
	existing := &Credential{
		Ref:       "codex-sean-example-com",
		Provider:  config.OAuthProviderCodex,
		Email:     "sean@example.com",
		AccountID: "acct_123",
	}
	incoming := &Credential{
		Ref:       "codex-sean-example-com",
		Provider:  config.OAuthProviderCodex,
		Email:     "sean@example.com",
		AccountID: "acct_456",
	}

	if got := credentialUpdateMatchScore(existing, incoming); got != credentialMatchNone {
		t.Fatalf("match score = %d, want %d", got, credentialMatchNone)
	}
}

func TestSameAccountIdentity_AllowsLegacyClaudeCredentialWithoutAccountID(t *testing.T) {
	legacy := &Credential{
		Ref:      "claude-legacy-ref",
		Provider: config.OAuthProviderClaude,
		Email:    "sean@example.com",
		Metadata: map[string]string{
			"organization_id": "org_123",
		},
	}
	incoming := &Credential{
		Ref:       "claude-sean-example-com",
		Provider:  config.OAuthProviderClaude,
		Email:     "sean@example.com",
		AccountID: "acct_123",
		Metadata: map[string]string{
			"organization_id": "org_123",
		},
	}

	if !SameAccountIdentity(legacy, incoming) {
		t.Fatalf("expected legacy and incoming claude credentials to match")
	}
}

func TestSameAccountIdentity_AllowsLegacyGeminiCredentialWithoutProjectID(t *testing.T) {
	legacy := &Credential{
		Ref:      "gemini-legacy-ref",
		Provider: config.OAuthProviderGemini,
		Email:    "sean@example.com",
	}
	incoming := &Credential{
		Ref:       "gemini-sean-example-com-project-123",
		Provider:  config.OAuthProviderGemini,
		Email:     "sean@example.com",
		AccountID: "project-123",
		Metadata: map[string]string{
			"project_id": "project-123",
		},
	}

	if !SameAccountIdentity(legacy, incoming) {
		t.Fatalf("expected legacy and incoming gemini credentials to match")
	}
}
