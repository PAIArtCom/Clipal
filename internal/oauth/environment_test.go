package oauth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestAccountEnvironmentPersistsAcrossRefreshRenameAndReopen(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	cred := &Credential{Provider: config.OAuthProviderClaude, Ref: "first", AccountID: "account-A", AccessToken: "one"}
	if err := store.Save(cred); err != nil {
		t.Fatal(err)
	}
	device := cred.Environment.DeviceID
	updated, err := store.UpdateEnvironment(cred.Provider, cred.Ref, &RequestEnvironment{Country: "jp", Timezone: "Asia/Tokyo", Source: "user", DNS: "cloudflare"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Environment.Language != "ja-JP" || updated.Environment.Timezone != "Asia/Tokyo" || updated.Environment.DeviceID != device {
		t.Fatalf("environment=%+v", updated.Environment)
	}
	stale := cred.Clone()
	stale.AccessToken = "rotated"
	stale.Ref = "renamed"
	if err := store.Save(stale); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewStore(dir).Load(cred.Provider, "first")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AccessToken != "rotated" || loaded.Environment.DNS != "cloudflare" || loaded.Environment.DeviceID != device {
		t.Fatalf("refresh overwrote environment: %+v", loaded.Environment)
	}
	other := &Credential{Provider: cred.Provider, AccountID: "account-B"}
	if AccountDeviceID(other) == device {
		t.Fatal("different accounts share device ID")
	}
	other.Provider = config.OAuthProviderCodex
	other.AccountID = cred.AccountID
	if AccountDeviceID(other) == device {
		t.Fatal("different OAuth services share device ID")
	}
}

func TestNewAccountDefaultsUserChoiceAndDaylightSaving(t *testing.T) {
	c := &Credential{Provider: config.OAuthProviderCodex, AccountID: "account", Metadata: map[string]string{"country_code": "JP", "timezone": "Asia/Tokyo", "language": "ja-JP"}}
	e, err := NewAccountEnvironment(c, nil)
	if err != nil || e.Country != "US" || e.Timezone != "America/New_York" || e.Source != "default" || e.Language != "en-US" || e.NeedsConfirmation {
		t.Fatalf("defaults=%+v err=%v", e, err)
	}
	loc, err := time.LoadLocation(e.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	_, winter := time.Date(2026, 1, 15, 12, 0, 0, 0, loc).Zone()
	_, summer := time.Date(2026, 7, 15, 12, 0, 0, 0, loc).Zone()
	if winter != -5*3600 || summer != -4*3600 {
		t.Fatal("New York did not follow IANA daylight saving")
	}
	changed, err := NewAccountEnvironment(c, &RegionOptions{Country: "jp", Timezone: "Asia/Tokyo", Source: "default"})
	if err != nil || changed.Source != "user" || changed.Language != "ja-JP" {
		t.Fatalf("choice=%+v err=%v", changed, err)
	}
	for _, bad := range []RegionOptions{{Country: "", Timezone: "UTC"}, {Country: "USA", Timezone: "UTC"}, {Country: "US", Timezone: "UTC-5"}, {Country: "US", Timezone: "Local"}, {Country: "US", Timezone: "America/Not_A_Zone"}, {Country: "US", Timezone: "America/New_York", Source: "exit"}} {
		if _, err := NewAccountEnvironment(c, &bad); err == nil {
			t.Fatalf("accepted invalid choice %+v", bad)
		}
	}
}

func TestLegacyRegionNeedsConfirmationAndReadsDoNotWrite(t *testing.T) {
	for _, hasRegion := range []bool{false, true} {
		dir := t.TempDir()
		store := NewStore(dir)
		c := &Credential{Provider: config.OAuthProviderCodex, Ref: "legacy", AccountID: "account", AccessToken: "token", Metadata: map[string]string{"country_code": "JP"}}
		if hasRegion {
			c.Environment = &RequestEnvironment{Country: "US", Timezone: "America/Los_Angeles", Language: "en-US", Locale: "en-US", DNS: "auto"}
		}
		raw, _ := json.Marshal(c)
		path := store.preferredPath(c.Provider, "", c.Ref)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		loaded, err := store.Load(c.Provider, c.Ref)
		if err != nil {
			t.Fatal(err)
		}
		if !loaded.Environment.NeedsConfirmation || loaded.Environment.Source != "" || (hasRegion && loaded.Environment.Timezone != "America/Los_Angeles") || (!hasRegion && loaded.Environment.Country != "") {
			t.Fatalf("legacy=%+v", loaded.Environment)
		}
		if effective := loaded.Environment.ForRequests(); effective.Country != "" || effective.Timezone != "" || effective.DeviceID == "" || effective.DNS != "auto" {
			t.Fatalf("unconfirmed request=%+v", effective)
		}
		svc := NewService(dir)
		svc.PrepareEnvironment(loaded)
		if _, err := store.List(c.Provider); err != nil {
			t.Fatal(err)
		}
		after, _ := os.ReadFile(path)
		if string(after) != string(raw) {
			t.Fatal("read or request preparation persisted legacy region")
		}
		configured, err := store.ConfigureRegion(loaded, RegionOptions{Country: "US", Timezone: "America/New_York"})
		if err != nil {
			t.Fatal(err)
		}
		if configured.Environment.Source != "user" || configured.Environment.NeedsConfirmation || configured.Metadata["country_code"] != "JP" || configured.AccessToken != "token" {
			t.Fatal("confirmation changed tokens or upstream attributes")
		}
		refreshed := loaded.Clone()
		refreshed.AccessToken = "refreshed"
		if err := store.Save(refreshed); err != nil {
			t.Fatal(err)
		}
		reopened, err := NewStore(dir).Load(c.Provider, c.Ref)
		if err != nil || !reflect.DeepEqual(reopened.Environment, configured.Environment) {
			t.Fatal("refresh or restart overwrote configured environment")
		}
	}
}

func TestConfiguredRegionAndTokenSaveRemainSerialized(t *testing.T) {
	store := NewStore(t.TempDir())
	c := &Credential{Provider: config.OAuthProviderCodex, Ref: "account", AccountID: "account", AccessToken: "before"}
	c.Environment, _ = NewAccountEnvironment(c, nil)
	if err := store.Save(c); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := c.Clone()
			copy.AccessToken = "after"
			if err := store.Save(copy); err != nil {
				t.Error(err)
			}
		}()
	}
	configured, err := store.ConfigureRegion(c, RegionOptions{Country: "JP", Timezone: "Asia/Tokyo"})
	if err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	saved, err := store.Load(c.Provider, c.Ref)
	if err != nil || saved.AccessToken != "after" || !reflect.DeepEqual(saved.Environment, configured.Environment) {
		t.Fatal("concurrent save lost user region")
	}
}

func TestLoginAddsDefaultsButReauthorizationPreservesAccountRegion(t *testing.T) {
	dir := t.TempDir()
	c := &Credential{Provider: config.OAuthProviderClaude, Ref: "account", AccountID: "id", AccessToken: "new", Metadata: map[string]string{"country_code": "JP"}}
	stub := &stubProviderClient{provider: c.Provider, startSession: &LoginSession{ID: "session", Provider: c.Provider, Status: LoginStatusPending, ExpiresAt: time.Now().Add(time.Hour)}, exchangeCred: c}
	svc := NewService(dir, WithProviderClient(stub))
	session, err := svc.StartLogin(c.Provider)
	if err != nil {
		t.Fatal(err)
	}
	completed := svc.completeSessionWithCode(context.Background(), session, "code", nil, nil)
	if completed.Status != LoginStatusCompleted {
		t.Fatal(completed.Error)
	}
	saved, err := svc.Load(c.Provider, c.Ref)
	if err != nil || saved.Environment.Source != "default" || saved.Environment.Timezone != "America/New_York" {
		t.Fatal("login did not apply defaults")
	}
	configured, err := svc.Store().ConfigureRegion(saved, RegionOptions{Country: "JP", Timezone: "Asia/Tokyo"})
	if err != nil {
		t.Fatal(err)
	}
	session, err = svc.StartLoginWithEnvironment(c.Provider, nil, &RegionOptions{Country: "DE", Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	completed = svc.completeSessionWithCode(context.Background(), session, "code", nil, nil)
	if completed.Status != LoginStatusCompleted {
		t.Fatal(completed.Error)
	}
	saved, err = svc.Load(c.Provider, c.Ref)
	if err != nil || !reflect.DeepEqual(saved.Environment, configured.Environment) {
		t.Fatal("reauthorization changed existing account settings")
	}
}
