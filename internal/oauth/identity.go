package oauth

import (
	"strings"

	"github.com/lansespirit/Clipal/internal/config"
)

type accountIdentityMatchScore int

const (
	accountIdentityNoMatch accountIdentityMatchScore = iota
	accountIdentityWeakMatch
	accountIdentityScopedMatch
	accountIdentityStrongMatch
)

const (
	credentialMatchNone     = 0
	credentialMatchRefBonus = 10
	credentialMatchStep     = 100
)

// SameAccountIdentity reports whether two credentials represent the same
// upstream OAuth account, even if their refs differ.
func SameAccountIdentity(a *Credential, b *Credential) bool {
	return sameAccountIdentityScore(a, b) > accountIdentityNoMatch
}

// AccountIdentityKey returns the normalized identity key used to recognize the
// same upstream OAuth account across credential refreshes and ref changes.
func AccountIdentityKey(cred *Credential) string {
	return canonicalAccountIdentityKey(cred)
}

func sameAccountIdentityScore(a *Credential, b *Credential) accountIdentityMatchScore {
	if a == nil || b == nil {
		return accountIdentityNoMatch
	}
	if normalizeProvider(a.Provider) != normalizeProvider(b.Provider) {
		return accountIdentityNoMatch
	}

	switch normalizeProvider(a.Provider) {
	case config.OAuthProviderClaude:
		return sameClaudeAccountIdentityScore(a, b)
	case config.OAuthProviderGemini:
		return sameGeminiAccountIdentityScore(a, b)
	default:
		return sameDefaultAccountIdentityScore(a, b)
	}
}

func mergeCredentialForUpdate(existing *Credential, incoming *Credential) *Credential {
	if incoming == nil {
		return nil
	}
	merged := incoming.Clone()
	if existing == nil {
		return merged
	}

	if ref := strings.TrimSpace(existing.Ref); ref != "" {
		merged.Ref = ref
	}
	if strings.TrimSpace(merged.Email) == "" {
		merged.Email = existing.Email
	}
	if strings.TrimSpace(merged.AccountID) == "" {
		merged.AccountID = existing.AccountID
	}
	if strings.TrimSpace(merged.AccessToken) == "" {
		merged.AccessToken = existing.AccessToken
	}
	if strings.TrimSpace(merged.RefreshToken) == "" {
		merged.RefreshToken = existing.RefreshToken
	}
	if merged.ExpiresAt.IsZero() {
		merged.ExpiresAt = existing.ExpiresAt
	}
	if merged.LastRefresh.IsZero() {
		merged.LastRefresh = existing.LastRefresh
	}
	if len(existing.Metadata) == 0 {
		return merged
	}
	if merged.Metadata == nil {
		merged.Metadata = make(map[string]string, len(existing.Metadata))
	}
	for k, v := range existing.Metadata {
		if volatileCredentialMetadataKey(merged.Provider, k) {
			continue
		}
		if strings.TrimSpace(merged.Metadata[k]) == "" {
			merged.Metadata[k] = v
		}
	}
	return merged
}

func volatileCredentialMetadataKey(provider config.OAuthProvider, key string) bool {
	if normalizeProvider(provider) != config.OAuthProviderAntigravity {
		return false
	}
	switch strings.TrimSpace(key) {
	case "tier_id",
		"current_tier_id",
		"current_tier_name",
		"paid_tier_id",
		"paid_tier_name",
		"paid_credit_type",
		"paid_minimum_credit_amount_for_usage",
		"allowed_default_tier_id",
		"allowed_default_tier_name":
		return true
	default:
		return false
	}
}

func credentialUpdateMatchScore(existing *Credential, incoming *Credential) int {
	if existing == nil || incoming == nil {
		return credentialMatchNone
	}
	if accountIdentityConflict(existing, incoming) {
		return credentialMatchNone
	}

	score := int(sameAccountIdentityScore(existing, incoming)) * credentialMatchStep
	if strings.TrimSpace(existing.Ref) != "" && strings.TrimSpace(existing.Ref) == strings.TrimSpace(incoming.Ref) {
		score += credentialMatchRefBonus
	}
	return score
}

func sameDefaultAccountIdentityScore(a *Credential, b *Credential) accountIdentityMatchScore {
	accountA := normalizeIdentityString(a.AccountID)
	accountB := normalizeIdentityString(b.AccountID)
	if accountA != "" && accountB != "" {
		if accountA == accountB {
			return accountIdentityStrongMatch
		}
		return accountIdentityNoMatch
	}
	if sameEmailIdentity(a.Email, b.Email) {
		return accountIdentityWeakMatch
	}
	return accountIdentityNoMatch
}

func sameClaudeAccountIdentityScore(a *Credential, b *Credential) accountIdentityMatchScore {
	accountA := normalizeIdentityString(a.AccountID)
	accountB := normalizeIdentityString(b.AccountID)
	if accountA != "" && accountB != "" {
		if accountA == accountB {
			return accountIdentityStrongMatch
		}
		return accountIdentityNoMatch
	}

	orgA := normalizeIdentityString(credentialMetadataValue(a, "organization_id"))
	orgB := normalizeIdentityString(credentialMetadataValue(b, "organization_id"))
	if orgA != "" && orgB != "" {
		if orgA == orgB {
			return accountIdentityScopedMatch
		}
		return accountIdentityNoMatch
	}

	if sameEmailIdentity(a.Email, b.Email) {
		return accountIdentityWeakMatch
	}
	return accountIdentityNoMatch
}

func sameGeminiAccountIdentityScore(a *Credential, b *Credential) accountIdentityMatchScore {
	projectA := normalizeIdentityString(geminiCredentialProjectID(a))
	projectB := normalizeIdentityString(geminiCredentialProjectID(b))
	if projectA != "" && projectB != "" {
		if projectA != projectB {
			return accountIdentityNoMatch
		}
		if geminiEmailsCompatible(a.Email, b.Email) {
			return accountIdentityStrongMatch
		}
		return accountIdentityNoMatch
	}
	if sameEmailIdentity(a.Email, b.Email) {
		return accountIdentityWeakMatch
	}
	return accountIdentityNoMatch
}

func geminiCredentialProjectID(cred *Credential) string {
	if cred == nil {
		return ""
	}
	return firstNonEmpty(
		strings.TrimSpace(cred.AccountID),
		credentialMetadataValue(cred, "project_id"),
		credentialMetadataValue(cred, "requested_project_id"),
	)
}

func credentialMetadataValue(cred *Credential, key string) string {
	if cred == nil || len(cred.Metadata) == 0 {
		return ""
	}
	return strings.TrimSpace(cred.Metadata[strings.TrimSpace(key)])
}

func geminiEmailsCompatible(a string, b string) bool {
	emailA := normalizeEmailIdentity(a)
	emailB := normalizeEmailIdentity(b)
	return emailA == "" || emailB == "" || emailA == emailB
}

func sameEmailIdentity(a string, b string) bool {
	emailA := normalizeEmailIdentity(a)
	emailB := normalizeEmailIdentity(b)
	return emailA != "" && emailA == emailB
}

func accountIdentityConflict(a *Credential, b *Credential) bool {
	if a == nil || b == nil {
		return false
	}
	if normalizeProvider(a.Provider) != normalizeProvider(b.Provider) {
		return true
	}

	switch normalizeProvider(a.Provider) {
	case config.OAuthProviderClaude:
		return claudeAccountIdentityConflict(a, b)
	case config.OAuthProviderGemini:
		return geminiAccountIdentityConflict(a, b)
	default:
		return defaultAccountIdentityConflict(a, b)
	}
}

func defaultAccountIdentityConflict(a *Credential, b *Credential) bool {
	accountA := normalizeIdentityString(a.AccountID)
	accountB := normalizeIdentityString(b.AccountID)
	if accountA != "" && accountB != "" && accountA != accountB {
		return true
	}
	if accountA == "" || accountB == "" {
		return conflictingEmailOnlyIdentity(a.Email, b.Email)
	}
	return false
}

func claudeAccountIdentityConflict(a *Credential, b *Credential) bool {
	accountA := normalizeIdentityString(a.AccountID)
	accountB := normalizeIdentityString(b.AccountID)
	if accountA != "" && accountB != "" {
		return accountA != accountB
	}

	orgA := normalizeIdentityString(credentialMetadataValue(a, "organization_id"))
	orgB := normalizeIdentityString(credentialMetadataValue(b, "organization_id"))
	if orgA != "" && orgB != "" {
		return orgA != orgB
	}

	return conflictingEmailOnlyIdentity(a.Email, b.Email)
}

func geminiAccountIdentityConflict(a *Credential, b *Credential) bool {
	projectA := normalizeIdentityString(geminiCredentialProjectID(a))
	projectB := normalizeIdentityString(geminiCredentialProjectID(b))
	if projectA != "" && projectB != "" {
		if projectA != projectB {
			return true
		}
		return conflictingEmailOnlyIdentity(a.Email, b.Email)
	}
	return conflictingEmailOnlyIdentity(a.Email, b.Email)
}

func conflictingEmailOnlyIdentity(a string, b string) bool {
	emailA := normalizeEmailIdentity(a)
	emailB := normalizeEmailIdentity(b)
	return emailA != "" && emailB != "" && emailA != emailB
}

func canonicalAccountIdentityKey(cred *Credential) string {
	if cred == nil {
		return ""
	}

	switch normalizeProvider(cred.Provider) {
	case config.OAuthProviderClaude:
		if accountID := normalizeIdentityString(cred.AccountID); accountID != "" {
			return "acct:" + accountID
		}
		if orgID := normalizeIdentityString(credentialMetadataValue(cred, "organization_id")); orgID != "" {
			return "org:" + orgID
		}
	case config.OAuthProviderGemini:
		if projectID := normalizeIdentityString(geminiCredentialProjectID(cred)); projectID != "" {
			return "project:" + projectID
		}
	default:
		if accountID := normalizeIdentityString(cred.AccountID); accountID != "" {
			return "acct:" + accountID
		}
	}

	if email := normalizeEmailIdentity(cred.Email); email != "" {
		return "email:" + email
	}
	return ""
}

func normalizeEmailIdentity(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func normalizeIdentityString(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}
