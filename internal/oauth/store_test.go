package oauth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

func TestStoreSaveUses0600Permissions(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	cred := &Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Date(2026, 4, 18, 15, 0, 0, 0, time.UTC),
		LastRefresh:  time.Date(2026, 4, 18, 14, 30, 0, 0, time.UTC),
		Metadata: map[string]string{
			"id_token": "jwt-token",
		},
	}

	if err := store.Save(cred); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path, err := store.resolvePath(config.OAuthProviderCodex, cred.Ref)
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("perm = %o, want 0600", got)
	}
	if got := filepath.Base(path); got != "sean@example.com--codex-sean-example-com.json" {
		t.Fatalf("credential filename = %q", got)
	}
}

func TestCredentialFileName_PreservesReadableEmailPrefix(t *testing.T) {
	got := credentialFileName("eileenallen247719@hotmail.com", "7636ekkJJ[42")
	if got != "eileenallen247719@hotmail.com--7636ekkJJ[42.json" {
		t.Fatalf("credential filename = %q", got)
	}
}

func TestStoreLoadRoundTripPreservesCredentialMetadata(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	want := &Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Date(2026, 4, 18, 15, 0, 0, 0, time.UTC),
		LastRefresh:  time.Date(2026, 4, 18, 14, 30, 0, 0, time.UTC),
		Metadata: map[string]string{
			"id_token": "jwt-token",
		},
	}

	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load(config.OAuthProviderCodex, want.Ref)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("credential mismatch:\n got %#v\nwant %#v", got, want)
	}
}

func TestStoreSave_ReusesExistingRefForSameOAuthAccount(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	existing := &Credential{
		Ref:          "codex-legacy-ref",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		ExpiresAt:    time.Date(2026, 4, 18, 15, 0, 0, 0, time.UTC),
		LastRefresh:  time.Date(2026, 4, 18, 14, 30, 0, 0, time.UTC),
		Metadata: map[string]string{
			"id_token": "jwt-old",
		},
	}
	if err := store.Save(existing); err != nil {
		t.Fatalf("Save existing: %v", err)
	}

	incoming := &Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-new",
		RefreshToken: "refresh-new",
		ExpiresAt:    time.Date(2026, 4, 18, 16, 0, 0, 0, time.UTC),
		LastRefresh:  time.Date(2026, 4, 18, 15, 30, 0, 0, time.UTC),
		Metadata: map[string]string{
			"id_token": "jwt-new",
		},
	}
	newRef := incoming.Ref
	if err := store.Save(incoming); err != nil {
		t.Fatalf("Save incoming: %v", err)
	}

	got, err := store.Load(config.OAuthProviderCodex, existing.Ref)
	if err != nil {
		t.Fatalf("Load existing ref: %v", err)
	}
	if got.Ref != existing.Ref {
		t.Fatalf("ref = %q, want %q", got.Ref, existing.Ref)
	}
	if got.AccessToken != "access-new" {
		t.Fatalf("access_token = %q, want access-new", got.AccessToken)
	}
	if got.RefreshToken != "refresh-new" {
		t.Fatalf("refresh_token = %q, want refresh-new", got.RefreshToken)
	}
	if got.Metadata["id_token"] != "jwt-new" {
		t.Fatalf("id_token = %q, want jwt-new", got.Metadata["id_token"])
	}

	if _, err := store.Load(config.OAuthProviderCodex, newRef); !os.IsNotExist(err) {
		t.Fatalf("Load new ref err = %v, want not-exist", err)
	}
	accounts, err := store.List(config.OAuthProviderCodex)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts len = %d, want 1", len(accounts))
	}
}

func TestStoreSave_DropsStaleAntigravityTierMetadataOnUpdate(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	existing := &Credential{
		Ref:          "antigravity-sean-example-com-project-123",
		Provider:     config.OAuthProviderAntigravity,
		Email:        "sean@example.com",
		AccountID:    "project-123",
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		Metadata: map[string]string{
			"project_id":                           "project-123",
			"requested_project_id":                 "requested-project",
			"tier_id":                              "g1-pro-tier",
			"current_tier_id":                      "free-tier",
			"current_tier_name":                    "Antigravity",
			"paid_tier_id":                         "g1-pro-tier",
			"paid_tier_name":                       "Google AI Pro",
			"paid_credit_type":                     "GOOGLE_ONE_AI",
			"paid_minimum_credit_amount_for_usage": "50",
			"allowed_default_tier_id":              "free-tier",
			"allowed_default_tier_name":            "Antigravity",
		},
	}
	if err := store.Save(existing); err != nil {
		t.Fatalf("Save existing: %v", err)
	}

	incoming := &Credential{
		Ref:          "antigravity-sean-example-com-project-123",
		Provider:     config.OAuthProviderAntigravity,
		Email:        "sean@example.com",
		AccountID:    "project-123",
		AccessToken:  "access-new",
		RefreshToken: "refresh-new",
		Metadata: map[string]string{
			"project_id":        "project-123",
			"tier_id":           "free-tier",
			"current_tier_id":   "free-tier",
			"current_tier_name": "Antigravity",
		},
	}
	if err := store.Save(incoming); err != nil {
		t.Fatalf("Save incoming: %v", err)
	}

	loaded, err := store.Load(config.OAuthProviderAntigravity, incoming.Ref)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := loaded.Metadata["requested_project_id"]; got != "requested-project" {
		t.Fatalf("requested_project_id = %q, want requested-project", got)
	}
	for _, key := range []string{
		"paid_tier_id",
		"paid_tier_name",
		"paid_credit_type",
		"paid_minimum_credit_amount_for_usage",
		"allowed_default_tier_id",
		"allowed_default_tier_name",
	} {
		if got := loaded.Metadata[key]; got != "" {
			t.Fatalf("metadata[%q] = %q, want empty", key, got)
		}
	}
	if got := loaded.Metadata["tier_id"]; got != "free-tier" {
		t.Fatalf("tier_id = %q, want free-tier", got)
	}
}

func TestStoreSave_DoesNotMergeDifferentAccountsOnRefCollision(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	existing := &Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
	}
	if err := store.Save(existing); err != nil {
		t.Fatalf("Save existing: %v", err)
	}

	incoming := &Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_456",
		AccessToken:  "access-new",
		RefreshToken: "refresh-new",
	}
	if err := store.Save(incoming); err != nil {
		t.Fatalf("Save incoming: %v", err)
	}
	if incoming.Ref == existing.Ref {
		t.Fatalf("incoming ref = %q, want disambiguated ref", incoming.Ref)
	}

	loadedExisting, err := store.Load(config.OAuthProviderCodex, existing.Ref)
	if err != nil {
		t.Fatalf("Load existing: %v", err)
	}
	if loadedExisting.AccountID != "acct_123" {
		t.Fatalf("existing account_id = %q, want acct_123", loadedExisting.AccountID)
	}
	if loadedExisting.AccessToken != "access-old" {
		t.Fatalf("existing access_token = %q, want access-old", loadedExisting.AccessToken)
	}

	loadedIncoming, err := store.Load(config.OAuthProviderCodex, incoming.Ref)
	if err != nil {
		t.Fatalf("Load incoming: %v", err)
	}
	if loadedIncoming.AccountID != "acct_456" {
		t.Fatalf("incoming account_id = %q, want acct_456", loadedIncoming.AccountID)
	}
	if loadedIncoming.AccessToken != "access-new" {
		t.Fatalf("incoming access_token = %q, want access-new", loadedIncoming.AccessToken)
	}

	accounts, err := store.List(config.OAuthProviderCodex)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("accounts len = %d, want 2", len(accounts))
	}
}

func TestStoreSave_ReusesExistingRefWhenHistoricalCredentialMissesAccountIdentity(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	existing := &Credential{
		Ref:          "codex-legacy-ref",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
	}
	if err := store.Save(existing); err != nil {
		t.Fatalf("Save existing: %v", err)
	}

	incoming := &Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-new",
		RefreshToken: "refresh-new",
	}
	if err := store.Save(incoming); err != nil {
		t.Fatalf("Save incoming: %v", err)
	}
	if incoming.Ref != existing.Ref {
		t.Fatalf("incoming ref = %q, want %q", incoming.Ref, existing.Ref)
	}

	loaded, err := store.Load(config.OAuthProviderCodex, existing.Ref)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AccountID != "acct_123" {
		t.Fatalf("account_id = %q, want acct_123", loaded.AccountID)
	}
	if loaded.AccessToken != "access-new" {
		t.Fatalf("access_token = %q, want access-new", loaded.AccessToken)
	}
}

func TestStoreSave_PrefersMostRecentlyRefreshedDuplicateOverFilenameOrder(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	stale := &Credential{
		Ref:          "codex-a-ref",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccessToken:  "access-stale",
		RefreshToken: "refresh-stale",
		LastRefresh:  time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC),
	}
	fresh := &Credential{
		Ref:          "codex-z-ref",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccessToken:  "access-fresh",
		RefreshToken: "refresh-fresh",
		LastRefresh:  time.Date(2026, 4, 18, 13, 0, 0, 0, time.UTC),
	}
	if err := store.Save(stale); err != nil {
		t.Fatalf("Save stale: %v", err)
	}
	if err := writeCredentialFile(
		store.preferredPath(config.OAuthProviderCodex, fresh.Email, fresh.Ref),
		fresh,
	); err != nil {
		t.Fatalf("write fresh duplicate: %v", err)
	}

	incoming := &Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccessToken:  "access-updated",
		RefreshToken: "refresh-updated",
		LastRefresh:  time.Date(2026, 4, 18, 14, 0, 0, 0, time.UTC),
	}
	if err := store.Save(incoming); err != nil {
		t.Fatalf("Save incoming: %v", err)
	}
	if incoming.Ref != fresh.Ref {
		t.Fatalf("incoming ref = %q, want freshest duplicate ref %q", incoming.Ref, fresh.Ref)
	}

	loadedFresh, err := store.Load(config.OAuthProviderCodex, fresh.Ref)
	if err != nil {
		t.Fatalf("Load fresh: %v", err)
	}
	if loadedFresh.AccessToken != "access-updated" {
		t.Fatalf("fresh access_token = %q, want access-updated", loadedFresh.AccessToken)
	}

	loadedStale, err := store.Load(config.OAuthProviderCodex, stale.Ref)
	if err != nil {
		t.Fatalf("Load stale: %v", err)
	}
	if loadedStale.AccessToken != "access-stale" {
		t.Fatalf("stale access_token = %q, want access-stale", loadedStale.AccessToken)
	}
}

func TestStoreListAndSave_SkipUnreadableJSONFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Save(&Credential{
		Ref:          "codex-good-ref",
		Provider:     config.OAuthProviderCodex,
		Email:        "good@example.com",
		AccountID:    "acct_good",
		AccessToken:  "access-good",
		RefreshToken: "refresh-good",
		LastRefresh:  time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Save good: %v", err)
	}

	badPath := store.preferredPath(config.OAuthProviderCodex, "bad@example.com", "codex-bad-ref")
	if err := os.MkdirAll(filepath.Dir(badPath), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(badPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("WriteFile bad json: %v", err)
	}

	accounts, err := store.List(config.OAuthProviderCodex)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts len = %d, want 1", len(accounts))
	}
	if accounts[0].Ref != "codex-good-ref" {
		t.Fatalf("listed ref = %q, want codex-good-ref", accounts[0].Ref)
	}

	if err := store.Save(&Credential{
		Ref:          "codex-new-ref",
		Provider:     config.OAuthProviderCodex,
		Email:        "new@example.com",
		AccountID:    "acct_new",
		AccessToken:  "access-new",
		RefreshToken: "refresh-new",
	}); err != nil {
		t.Fatalf("Save with unreadable neighbor: %v", err)
	}
}

func writeCredentialFile(path string, cred *Credential) error {
	data, err := json.MarshalIndent(cred, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(path, data, 0o600)
}
