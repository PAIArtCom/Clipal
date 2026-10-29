package oauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

// AccountIdentity is the non-secret identity of one OAuth account. It is a
// view over Credential, not another credential store; access and refresh
// tokens are deliberately absent.
type AccountIdentity struct {
	Provider       config.OAuthProvider `json:"provider"`
	Key            string               `json:"key"`
	Source         string               `json:"source"`
	AccountID      string               `json:"account_id,omitempty"`
	OrganizationID string               `json:"organization_id,omitempty"`
	Email          string               `json:"email,omitempty"`
}

// EnvironmentState describes the account-owned request profile. It says
// nothing about network reachability or upstream account acceptance.
type EnvironmentState string

const (
	EnvironmentStateUnconfigured      EnvironmentState = "unconfigured"
	EnvironmentStateNeedsConfirmation EnvironmentState = "needs_confirmation"
	EnvironmentStatePartial           EnvironmentState = "partial"
	EnvironmentStateConfigured        EnvironmentState = "configured"
)

// EnvironmentEvidenceState classifies fresh runtime evidence. It is separate
// from EnvironmentState because a configured profile does not prove that a
// route or upstream account is usable.
type EnvironmentEvidenceState string

const (
	EnvironmentEvidenceUnknown  EnvironmentEvidenceState = "unknown"
	EnvironmentEvidenceObserved EnvironmentEvidenceState = "observed"
	EnvironmentEvidenceVerified EnvironmentEvidenceState = "verified"
	EnvironmentEvidenceBlocked  EnvironmentEvidenceState = "blocked"
)

// EnvironmentEvidenceScope states which boundary produced the observation.
// Verification is scoped; a successful reference lookup is not OAuth auth.
type EnvironmentEvidenceScope string

const (
	EnvironmentEvidenceScopeRequestProfile   EnvironmentEvidenceScope = "request_profile"
	EnvironmentEvidenceScopeTargetConnection EnvironmentEvidenceScope = "target_connection"
	EnvironmentEvidenceScopeReferenceExit    EnvironmentEvidenceScope = "reference_exit"
	EnvironmentEvidenceScopeOAuthAuth        EnvironmentEvidenceScope = "oauth_auth"
)

// EnvironmentEvidence is an in-memory observation envelope. Observations are
// intentionally separate from the stored request profile because routes and
// exits can change without a credential change.
type EnvironmentEvidence struct {
	State     EnvironmentEvidenceState `json:"state"`
	Scope     EnvironmentEvidenceScope `json:"scope"`
	CheckedAt time.Time                `json:"checked_at,omitempty"`
	Source    string                   `json:"source,omitempty"`
}

// AccountEnvironmentProfile groups the stable account identity with the
// account-owned request settings. Evidence is optional and is never persisted
// by Credential.Save; callers may attach a fresh diagnostic observation.
type AccountEnvironmentProfile struct {
	Identity AccountIdentity       `json:"identity"`
	Request  *RequestEnvironment   `json:"request_environment,omitempty"`
	State    EnvironmentState      `json:"state"`
	Evidence []EnvironmentEvidence `json:"evidence,omitempty"`
}

func (c *Credential) AccountIdentity() AccountIdentity {
	if c == nil {
		return AccountIdentity{}
	}
	key := AccountEnvironmentIdentity(c)
	source := "device"
	switch {
	case strings.HasPrefix(key, "acct:"):
		source = "account_id"
	case strings.HasPrefix(key, "org:"):
		source = "organization_id"
	case strings.HasPrefix(key, "email:"):
		source = "email"
	case strings.HasPrefix(key, "ref:"):
		source = "ref"
	}
	return AccountIdentity{
		Provider:       normalizeProvider(c.Provider),
		Key:            key,
		Source:         source,
		AccountID:      strings.TrimSpace(c.AccountID),
		OrganizationID: credentialMetadataValue(c, "organization_id"),
		Email:          strings.TrimSpace(c.Email),
	}
}

func (c *Credential) EnvironmentProfile() AccountEnvironmentProfile {
	if c == nil {
		return AccountEnvironmentProfile{State: EnvironmentStateUnconfigured}
	}
	request := c.Environment.Clone()
	return AccountEnvironmentProfile{
		Identity: c.AccountIdentity(),
		Request:  request,
		State:    request.State(),
	}
}

func (p AccountEnvironmentProfile) Clone() AccountEnvironmentProfile {
	clone := p
	clone.Request = p.Request.Clone()
	if len(p.Evidence) > 0 {
		clone.Evidence = append([]EnvironmentEvidence(nil), p.Evidence...)
	}
	return clone
}

func (p AccountEnvironmentProfile) EffectiveRequest() *RequestEnvironment {
	return p.Request.ForRequests()
}

// Fingerprint identifies the effective account request environment used by a
// transport client. It excludes unconfirmed region fields and contains no
// credentials, so callers can safely use it as a pool-generation key.
func (p AccountEnvironmentProfile) Fingerprint() string {
	data, _ := json.Marshal(p.EffectiveRequest())
	sum := sha256.Sum256(append([]byte("clipal-environment-v1\x00"), data...))
	return hex.EncodeToString(sum[:])
}

func (e *RequestEnvironment) State() EnvironmentState {
	if e == nil {
		return EnvironmentStateUnconfigured
	}
	if e.NeedsConfirmation {
		return EnvironmentStateNeedsConfirmation
	}
	if e.Complete() {
		return EnvironmentStateConfigured
	}
	return EnvironmentStatePartial
}
