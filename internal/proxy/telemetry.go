package proxy

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/logger"
	"github.com/PAIArtCom/Clipal/internal/telemetry"
)

type streamSuccess struct {
	responseBody []byte
	usage        telemetry.UsageSnapshot
}

type providerTelemetryIdentity struct {
	authType      config.ProviderAuthType
	baseURL       string
	keys          string
	oauthProvider config.OAuthProvider
	oauthRef      string
	priority      int
}

func (r *Router) ProviderUsageSnapshots(clientType string) map[string]telemetry.ProviderUsage {
	if r == nil || r.telemetry == nil {
		return nil
	}
	return r.telemetry.ProviderSnapshots(clientType)
}

func (r *Router) RenameProviderUsage(clientType string, from string, to string) error {
	if r == nil || r.telemetry == nil {
		return nil
	}
	return r.telemetry.RenameProvider(clientType, from, to)
}

func (r *Router) DeleteProviderUsage(clientType string, provider string) error {
	if r == nil || r.telemetry == nil {
		return nil
	}
	return r.telemetry.DeleteProvider(clientType, provider)
}

func (cp *ClientProxy) recordCompletedUsage(req *http.Request, provider string, statusCode int, usage telemetry.UsageSnapshot, when time.Time) {
	if cp == nil || cp.telemetry == nil {
		return
	}
	requestCtx, ok := requestContextFromRequest(req)
	clientType := ""
	if ok {
		clientType = strings.TrimSpace(string(requestCtx.ClientType))
	}
	if clientType == "" {
		clientType = strings.TrimSpace(string(cp.clientType))
	}
	countSuccess := ok && recordsGenerationSuccess(requestCtx.Capability, statusCode)
	_ = cp.telemetry.Record(clientType, provider, usage, when, telemetry.RecordOptions{
		CountRequest: true,
		CountSuccess: countSuccess,
	})
}

func recordsGenerationSuccess(capability RequestCapability, statusCode int) bool {
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return false
	}
	switch capability {
	case CapabilityClaudeMessages, CapabilityGeminiGenerateContent, CapabilityGeminiStreamGenerate:
		return true
	default:
		return isOpenAIGenerationCapability(capability)
	}
}

func (r *Router) reconcileTelemetryUsage(oldCfg *config.Config, newCfg *config.Config) {
	if r == nil || r.telemetry == nil || oldCfg == nil || newCfg == nil {
		return
	}
	r.reconcileTelemetryUsageForClient(string(ClientClaude), oldCfg.Claude.Providers, newCfg.Claude.Providers)
	r.reconcileTelemetryUsageForClient(string(ClientOpenAI), oldCfg.OpenAI.Providers, newCfg.OpenAI.Providers)
	r.reconcileTelemetryUsageForClient(string(ClientGemini), oldCfg.Gemini.Providers, newCfg.Gemini.Providers)
}

func (r *Router) reconcileTelemetryUsageForClient(clientType string, oldProviders []config.Provider, newProviders []config.Provider) {
	if r == nil || r.telemetry == nil {
		return
	}

	oldByName := make(map[string]config.Provider, len(oldProviders))
	newByName := make(map[string]config.Provider, len(newProviders))
	for _, provider := range oldProviders {
		oldByName[provider.Name] = provider
	}
	for _, provider := range newProviders {
		newByName[provider.Name] = provider
	}

	removed := make(map[string]config.Provider)
	added := make(map[string]config.Provider)
	for name, provider := range oldByName {
		if _, ok := newByName[name]; !ok {
			removed[name] = provider
		}
	}
	for name, provider := range newByName {
		if _, ok := oldByName[name]; !ok {
			added[name] = provider
		}
	}

	removedByIdentity := make(map[providerTelemetryIdentity][]string)
	addedByIdentity := make(map[providerTelemetryIdentity][]string)
	for name, provider := range removed {
		identity := providerUsageIdentity(provider)
		removedByIdentity[identity] = append(removedByIdentity[identity], name)
	}
	for name, provider := range added {
		identity := providerUsageIdentity(provider)
		addedByIdentity[identity] = append(addedByIdentity[identity], name)
	}

	matchedRemoved := make(map[string]struct{})
	for identity, oldNames := range removedByIdentity {
		newNames := addedByIdentity[identity]
		if len(oldNames) == 1 && len(newNames) == 1 {
			from := oldNames[0]
			to := newNames[0]
			if err := r.telemetry.RenameProvider(clientType, from, to); err != nil {
				logger.Warn("failed to reconcile provider usage rename %s/%s -> %s: %v", clientType, from, to, err)
				continue
			}
			matchedRemoved[from] = struct{}{}
			continue
		}
		if len(oldNames) > 0 && len(newNames) > 0 {
			logger.Warn("skipping ambiguous usage telemetry rename for %s providers with auth_type=%q priority=%d", clientType, identity.authType, identity.priority)
		}
	}

	for name := range removed {
		if _, matched := matchedRemoved[name]; matched {
			continue
		}
		if err := r.telemetry.DeleteProvider(clientType, name); err != nil {
			logger.Warn("failed to reconcile deleted provider usage %s/%s: %v", clientType, name, err)
		}
	}
}

func providerUsageIdentity(provider config.Provider) providerTelemetryIdentity {
	return providerTelemetryIdentity{
		authType:      provider.NormalizedAuthType(),
		baseURL:       strings.TrimSpace(provider.BaseURL),
		keys:          strings.Join(providerUsageKeySet(provider), "\x00"),
		oauthProvider: provider.NormalizedOAuthProvider(),
		oauthRef:      provider.NormalizedOAuthRef(),
		priority:      provider.Priority,
	}
}

func providerUsageKeySet(provider config.Provider) []string {
	seen := make(map[string]struct{})
	keys := make([]string, 0, 1+len(provider.APIKeys))
	if key := strings.TrimSpace(provider.APIKey); key != "" {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for _, key := range provider.APIKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
