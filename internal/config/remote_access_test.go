package config

import "testing"

func TestValidate_RemoteProxyRequiresExplicitOptIn(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Global.ListenAddr = "0.0.0.0"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected remote listener to require explicit opt-in")
	}
	cfg.Global.AllowRemoteProxy = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() with explicit remote proxy opt-in: %v", err)
	}
}
