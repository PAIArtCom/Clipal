package oauth

import (
	"strings"

	"github.com/lansespirit/Clipal/internal/config"
)

type ProviderDescriptor struct {
	Provider    config.OAuthProvider `json:"provider"`
	ClientTypes []string             `json:"client_types,omitempty"`
	Available   bool                 `json:"available"`
}

var providerCatalog = []ProviderDescriptor{
	{
		Provider:    config.OAuthProviderCodex,
		ClientTypes: []string{"openai"},
		Available:   true,
	},
	{
		Provider:    config.OAuthProviderGemini,
		ClientTypes: []string{"gemini"},
		Available:   false,
	},
	{
		Provider:    config.OAuthProviderAntigravity,
		ClientTypes: []string{"gemini"},
		Available:   true,
	},
	{
		Provider:    config.OAuthProviderClaude,
		ClientTypes: []string{"claude"},
		Available:   true,
	},
}

func SupportedProvidersForClient(clientType string) []ProviderDescriptor {
	clientType = canonicalCatalogClientType(clientType)
	out := make([]ProviderDescriptor, 0, len(providerCatalog))
	for _, descriptor := range providerCatalog {
		if !descriptor.Available || !descriptor.SupportsClient(clientType) {
			continue
		}
		out = append(out, descriptor.Clone())
	}
	return out
}

func ProviderSupportedForClient(provider config.OAuthProvider, clientType string) bool {
	provider = normalizeProvider(provider)
	clientType = canonicalCatalogClientType(clientType)
	for _, descriptor := range providerCatalog {
		if normalizeProvider(descriptor.Provider) != provider {
			continue
		}
		return descriptor.SupportsClient(clientType)
	}
	return false
}

func ProviderAvailableForClient(provider config.OAuthProvider, clientType string) bool {
	provider = normalizeProvider(provider)
	clientType = canonicalCatalogClientType(clientType)
	for _, descriptor := range providerCatalog {
		if normalizeProvider(descriptor.Provider) != provider {
			continue
		}
		return descriptor.Available && descriptor.SupportsClient(clientType)
	}
	return false
}

func (d ProviderDescriptor) SupportsClient(clientType string) bool {
	clientType = canonicalCatalogClientType(clientType)
	for _, supported := range d.ClientTypes {
		if canonicalCatalogClientType(supported) == clientType {
			return true
		}
	}
	return false
}

func (d ProviderDescriptor) Clone() ProviderDescriptor {
	clone := d
	if d.ClientTypes != nil {
		clone.ClientTypes = append([]string(nil), d.ClientTypes...)
	}
	return clone
}

func canonicalCatalogClientType(clientType string) string {
	if canonical, ok := config.CanonicalClientType(strings.TrimSpace(clientType)); ok {
		return canonical
	}
	return strings.ToLower(strings.TrimSpace(clientType))
}
