package proxy

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	oauthpkg "github.com/PAIArtCom/Clipal/internal/oauth"
)

type liveCountingGeminiClient struct {
	*oauthpkg.GeminiClient
	fetchUsageCalls *int32
}

func (c *liveCountingGeminiClient) FetchUsage(ctx context.Context, cred *oauthpkg.Credential) (*oauthpkg.GeminiUsageDetails, error) {
	atomic.AddInt32(c.fetchUsageCalls, 1)
	return c.GeminiClient.FetchUsage(ctx, cred)
}

func (c *liveCountingGeminiClient) WithHTTPClient(httpClient *http.Client) oauthpkg.ProviderClient {
	clone, _ := c.GeminiClient.WithHTTPClient(httpClient).(*oauthpkg.GeminiClient)
	return &liveCountingGeminiClient{
		GeminiClient:    clone,
		fetchUsageCalls: c.fetchUsageCalls,
	}
}

func TestLiveGeminiOAuthCooldownUsesCachedQuotaReset(t *testing.T) {
	ref := strings.TrimSpace(os.Getenv("CLIPAL_LIVE_GEMINI_FAILOVER_REF"))
	if ref == "" {
		t.Skip("set CLIPAL_LIVE_GEMINI_FAILOVER_REF to run live Gemini failover validation")
	}

	configDir := strings.TrimSpace(os.Getenv("CLIPAL_LIVE_CONFIG_DIR"))
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("UserHomeDir: %v", err)
		}
		configDir = filepath.Join(home, ".clipal")
	}

	var fetchUsageCalls int32
	svc := oauthpkg.NewService(
		configDir,
		oauthpkg.WithGeminiUsageTTL(30*time.Second),
		oauthpkg.WithProviderClient(&liveCountingGeminiClient{
			GeminiClient:    oauthpkg.NewGeminiClient(),
			fetchUsageCalls: &fetchUsageCalls,
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	usage, err := svc.GetGeminiUsage(ctx, ref)
	if err != nil {
		t.Fatalf("GetGeminiUsage(%q): %v", ref, err)
	}

	target, ok := pickLiveExhaustedGeminiBucket(usage, time.Now())
	if !ok {
		t.Skipf("no exhausted Gemini bucket with future reset for ref=%s", ref)
	}

	cp := newClientProxy(ClientGemini, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "gemini-live-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderGemini,
			OAuthRef:      ref,
			Priority:      1,
		},
		{
			Name:     "p2",
			BaseURL:  "http://p2",
			APIKey:   "k2",
			Priority: 2,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "cloudcode-pa.googleapis.com":
			return newResponse(http.StatusTooManyRequests, http.Header{"Content-Type": []string{"application/json"}}, `{"error":{"code":429,"message":"quota exhausted"}}`), nil
		case "p2":
			return newResponse(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}}, `{"ok":true}`), nil
		default:
			t.Fatalf("unexpected host %q", r.URL.Host)
			return nil, nil
		}
	})

	path := "/v1beta/models/" + target.ModelID + ":generateContent"
	req := httptestNewGeminiGenerateRequest(path)

	rr := httptest.NewRecorder()
	cp.forwardWithFailover(rr, req, path)

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if got := rr.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q, want fallback body", got)
	}
	if !cp.isDeactivated(0) {
		t.Fatalf("expected gemini oauth provider to be in cooldown")
	}

	until := cp.deactivationUntil(0)
	if until.IsZero() {
		t.Fatalf("expected non-zero cooldown until")
	}
	if until.Before(target.ResetTime.Add(-2*time.Minute)) || until.After(target.ResetTime.Add(2*time.Minute)) {
		t.Fatalf("cooldown until = %s, want near %s", until.Format(time.RFC3339), target.ResetTime.Format(time.RFC3339))
	}
	if got := atomic.LoadInt32(&fetchUsageCalls); got != 1 {
		t.Fatalf("fetchUsage calls = %d, want cached quota reused after warmup", got)
	}

	t.Logf("live gemini failover ok: ref=%s model=%s reset=%s", ref, target.ModelID, target.ResetTime.Format(time.RFC3339))
}

func pickLiveExhaustedGeminiBucket(details *oauthpkg.GeminiUsageDetails, now time.Time) (oauthpkg.GeminiUsageBucket, bool) {
	if details == nil {
		return oauthpkg.GeminiUsageBucket{}, false
	}
	for _, bucket := range details.Buckets {
		if strings.TrimSpace(bucket.ModelID) == "" || !bucket.ResetTime.After(now) {
			continue
		}
		if liveGeminiBucketExhausted(bucket) {
			return bucket, true
		}
	}
	return oauthpkg.GeminiUsageBucket{}, false
}

func liveGeminiBucketExhausted(bucket oauthpkg.GeminiUsageBucket) bool {
	if bucket.RemainingFraction != nil && *bucket.RemainingFraction <= 0.05 {
		return true
	}
	return bucket.RemainingAmount != nil && *bucket.RemainingAmount <= 0
}

func httptestNewGeminiGenerateRequest(path string) *http.Request {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/gemini"+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return withRequestContext(req, RequestContext{
		ClientType:     ClientGemini,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiGenerateContent,
		UpstreamPath:   path,
		UnifiedIngress: true,
	})
}
