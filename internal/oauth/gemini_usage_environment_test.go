package oauth

import (
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestGeminiUsageCacheKeyIncludesEnvironmentFingerprint(t *testing.T) {
	base := AccountEnvironmentProfile{Request: &RequestEnvironment{DeviceID: "device", DNS: "auto", Country: "US", Language: "en-US", Locale: "en-US", Timezone: "America/New_York", Source: "user"}}
	changed := base.Clone()
	changed.Request.DNS = "cloudflare"
	if got, want := geminiUsageCacheKey(config.OAuthProviderAntigravity, "account", base.Fingerprint()), geminiUsageCacheKey(config.OAuthProviderAntigravity, "account", changed.Fingerprint()); got == want {
		t.Fatalf("cache key did not change with environment: %q", got)
	}
}
