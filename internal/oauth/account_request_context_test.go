package oauth

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestResolveAccountRequestContextKeepsCredentialAndEffectiveEnvironmentTogether(t *testing.T) {
	svc := NewService(t.TempDir())
	cred := &Credential{
		Provider:    config.OAuthProviderCodex,
		Ref:         "account-a",
		AccountID:   "acct-a",
		AccessToken: "access-a",
		ExpiresAt:   time.Now().Add(time.Hour),
		Environment: &RequestEnvironment{Country: "JP", Timezone: "Asia/Tokyo", Source: "user"},
	}
	if err := svc.Store().Save(cred); err != nil {
		t.Fatal(err)
	}

	resolved, err := svc.ResolveAccountRequestContext(context.Background(), config.OAuthProviderCodex, cred.Ref, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Credential == nil || resolved.Credential.AccessToken != "access-a" {
		t.Fatalf("credential = %+v", resolved.Credential)
	}
	if resolved.Profile.Identity.Key != "acct:acct-a" || resolved.Profile.State != EnvironmentStateConfigured {
		t.Fatalf("profile = %+v", resolved.Profile)
	}
	if resolved.Effective == nil || resolved.Effective.Country != "JP" || resolved.Effective.Timezone != "Asia/Tokyo" {
		t.Fatalf("effective = %+v", resolved.Effective)
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "access-a") {
		t.Fatalf("request context leaked credential material: %s", encoded)
	}
	resolved.Effective.Country = "DE"
	if resolved.Profile.Request.Country != "JP" {
		t.Fatal("effective environment aliases the stored profile")
	}
}
