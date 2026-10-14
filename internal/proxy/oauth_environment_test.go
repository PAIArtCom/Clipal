package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/oauth"
)

func TestOAuthAttemptsUseAccountIdentityAndRegionWithoutMutatingInput(t *testing.T) {
	svc := oauth.NewService(t.TempDir())
	var providers []config.Provider
	var credentials []*oauth.Credential
	for i, account := range []string{"account-A", "account-B"} {
		country := "JP"
		if i == 1 {
			country = "DE"
		}
		cred := &oauth.Credential{Provider: config.OAuthProviderClaude, Ref: account, AccountID: account, AccessToken: "secret-" + account, ExpiresAt: time.Now().Add(time.Hour), Environment: &oauth.RequestEnvironment{Country: country, Timezone: map[string]string{"JP": "Asia/Tokyo", "DE": "Europe/Berlin"}[country], Source: "user"}}
		if err := svc.Store().Save(cred); err != nil {
			t.Fatal(err)
		}
		credentials = append(credentials, cred)
		providers = append(providers, config.Provider{Name: account, AuthType: config.ProviderAuthTypeOAuth, OAuthProvider: cred.Provider, OAuthRef: cred.Ref, Priority: i + 1})
	}
	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", providers, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	defer cp.Close()
	raw := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"My actual timezone is Europe/London"}],"metadata":{"country":"GB","locale":"en-GB","timezone":"Europe/London","user_id":"{\"device_id\":\"client-device\",\"account_uuid\":\"wrong-account\",\"session_id\":\"conversation-1\"}"}}`)
	original := httptest.NewRequest(http.MethodPost, "http://local/clipal/v1/messages", bytes.NewReader(raw))
	original.Header.Set("Content-Type", "application/json")
	original.Header.Set("Accept-Language", "en-GB")
	payload := newRequestPayload(raw)
	for i, provider := range providers {
		req, err := cp.createClaudeOAuthRequestWithPayloadForProvider(original, provider, i, "/v1/messages", payload)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(req.Body)
		var root map[string]any
		_ = json.Unmarshal(data, &root)
		meta, ok := root["metadata"].(map[string]any)
		if !ok {
			t.Fatal("missing Claude metadata")
		}
		id := parseClaudeOAuthJSONObject(stringValue(meta["user_id"]))
		if id["device_id"] != credentials[i].Environment.DeviceID || id["account_uuid"] != credentials[i].AccountID || id["session_id"] != "conversation-1" {
			t.Fatalf("attempt %d identity=%v", i, id)
		}
		if meta["country"] != credentials[i].Environment.Country || meta["timezone"] != credentials[i].Environment.Timezone || req.Header.Get("Accept-Language") != credentials[i].Environment.Language {
			t.Fatalf("attempt %d region=%v", i, meta)
		}
		if !bytes.Contains(data, []byte("My actual timezone is Europe/London")) {
			t.Fatal("user message was changed")
		}
		if !bytes.Equal(signClaudeOAuthMessageBody(data), data) {
			t.Fatal("body was not re-signed after metadata normalization")
		}
		again, err := req.GetBody()
		if err != nil {
			t.Fatal(err)
		}
		retry, _ := io.ReadAll(again)
		_ = again.Close()
		if !bytes.Equal(data, retry) {
			t.Fatal("replay body lost account environment")
		}
	}
	if !bytes.Equal(payload.Body(), raw) || original.Header.Get("Accept-Language") != "en-GB" {
		t.Fatal("failover mutated the original request")
	}
}

func TestUnknownAccountRegionDoesNotInheritChangingClientEnvironment(t *testing.T) {
	e := &oauth.RequestEnvironment{DeviceID: "stable", DNS: "auto"}
	req := httptest.NewRequest(http.MethodPost, "http://local/", nil)
	req.Header.Set("Accept-Language", "de-DE")
	req.Header.Set("X-Timezone", "Europe/Berlin")
	oauth.ApplyEnvironmentHeaders(req, e)
	if req.Header.Get("Accept-Language") != "" || req.Header.Get("X-Timezone") != "" {
		t.Fatal("unknown account region inherited host headers")
	}
	meta := map[string]any{"country": "DE", "locale": "de-DE", "timezone": "Europe/Berlin", "session_id": "preserved"}
	normalizeEnvironmentMetadata(meta, e)
	if len(meta) != 1 || meta["session_id"] != "preserved" {
		t.Fatal("unknown region metadata was not omitted")
	}
}
