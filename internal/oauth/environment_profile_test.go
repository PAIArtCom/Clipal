package oauth

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestEnvironmentProfileSeparatesStableIdentityFromSecrets(t *testing.T) {
	cred := &Credential{
		Provider:     config.OAuthProviderClaude,
		Ref:          "owner",
		AccountID:    "acct-1",
		Email:        "owner@example.com",
		AccessToken:  "access-secret",
		RefreshToken: "refresh-secret",
		Metadata:     map[string]string{"organization_id": "org-1"},
		Environment:  &RequestEnvironment{Country: "JP", Language: "ja-JP", Locale: "ja-JP", Timezone: "Asia/Tokyo", Source: "user"},
	}

	profile := cred.EnvironmentProfile()
	if profile.Identity.Key != "acct:acct-1" || profile.Identity.Source != "account_id" {
		t.Fatalf("identity=%+v", profile.Identity)
	}
	if profile.State != EnvironmentStateConfigured || profile.Request.Language != "ja-JP" {
		t.Fatalf("profile=%+v", profile)
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(encoded)
	for _, secret := range []string{"access-secret", "refresh-secret", `"ref"`} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("profile leaked %q: %s", secret, serialized)
		}
	}
}

func TestEnvironmentProfileUsesFallbackIdentitySourceWithoutChangingMatching(t *testing.T) {
	cred := &Credential{Provider: config.OAuthProviderCodex, Ref: "legacy", Email: "legacy@example.com"}
	profile := cred.EnvironmentProfile()
	if profile.Identity.Key != "email:legacy@example.com" || profile.Identity.Source != "email" {
		t.Fatalf("identity=%+v", profile.Identity)
	}
	if AccountIdentityKey(cred) != "email:legacy@example.com" {
		t.Fatalf("matching key changed: %q", AccountIdentityKey(cred))
	}

	unknown := &Credential{Provider: config.OAuthProviderCodex, Ref: "opaque"}
	unknownProfile := unknown.EnvironmentProfile()
	if !strings.HasPrefix(unknownProfile.Identity.Key, "device:") || unknownProfile.Identity.Source != "device" {
		t.Fatalf("unknown identity=%+v", unknownProfile.Identity)
	}
}

func TestEnvironmentProfileStateAndEvidenceClone(t *testing.T) {
	cases := []struct {
		name string
		env  *RequestEnvironment
		want EnvironmentState
	}{
		{name: "nil", want: EnvironmentStateUnconfigured},
		{name: "legacy", env: &RequestEnvironment{NeedsConfirmation: true}, want: EnvironmentStateNeedsConfirmation},
		{name: "partial", env: &RequestEnvironment{Country: "JP"}, want: EnvironmentStatePartial},
		{name: "configured", env: &RequestEnvironment{Country: "JP", Language: "ja-JP", Locale: "ja-JP", Timezone: "Asia/Tokyo"}, want: EnvironmentStateConfigured},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.env.State(); got != tc.want {
				t.Fatalf("state=%q want %q", got, tc.want)
			}
		})
	}

	cred := &Credential{Provider: config.OAuthProviderCodex, AccountID: "acct", Environment: &RequestEnvironment{Country: "US", Timezone: "America/New_York", Source: "user"}}
	profile := cred.EnvironmentProfile()
	profile.Evidence = []EnvironmentEvidence{{State: EnvironmentEvidenceObserved, Scope: EnvironmentEvidenceScopeTargetConnection, CheckedAt: time.Now(), Source: "diagnostic"}}
	clone := profile.Clone()
	clone.Request.Country = "JP"
	clone.Evidence[0].Source = "other"
	if profile.Request.Country == clone.Request.Country || profile.Evidence[0].Source == clone.Evidence[0].Source {
		t.Fatalf("profile clone shares mutable state: original=%+v clone=%+v", profile, clone)
	}
	if got := profile.EffectiveRequest().Country; got != "US" {
		t.Fatalf("effective request country=%q", got)
	}
}

func TestEnvironmentProfileFingerprintTracksEffectiveRequestOnly(t *testing.T) {
	base := AccountEnvironmentProfile{Request: &RequestEnvironment{DeviceID: "device", DNS: "auto", Source: "user", Country: "US", Language: "en-US", Locale: "en-US", Timezone: "America/New_York"}}
	same := base.Clone()
	if base.Fingerprint() != same.Fingerprint() {
		t.Fatal("cloned profile changed fingerprint")
	}

	changed := base.Clone()
	changed.Request.Timezone = "Asia/Tokyo"
	if base.Fingerprint() == changed.Fingerprint() {
		t.Fatal("effective timezone change reused fingerprint")
	}

	legacy := base.Clone()
	legacy.Request.NeedsConfirmation = true
	legacy.Request.Country = "JP"
	legacy.Request.Language = "ja-JP"
	legacy.Request.Locale = "ja-JP"
	legacy.Request.Timezone = "Asia/Tokyo"
	unconfirmed := base.Clone()
	unconfirmed.Request.NeedsConfirmation = true
	unconfirmed.Request.Country = "DE"
	unconfirmed.Request.Language = "de-DE"
	unconfirmed.Request.Locale = "de-DE"
	unconfirmed.Request.Timezone = "Europe/Berlin"
	if legacy.Fingerprint() != unconfirmed.Fingerprint() {
		t.Fatal("unconfirmed region fields changed effective fingerprint")
	}
}
