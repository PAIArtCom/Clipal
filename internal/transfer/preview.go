package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/oauth"
	"github.com/lansespirit/Clipal/internal/telemetry"
)

// importStateFingerprint covers only state whose drift changes what an apply
// would do: the configuration and the credential identity set. Usage counters
// and token material change with routine proxied traffic and refreshes;
// hashing them would invalidate every previewed plan on a busy server.
func importStateFingerprint(cfg *config.Config, credentials []oauth.Credential) string {
	dataset := datasetFromState(cfg, nil, telemetry.DataSnapshot{}, "", timeZero)
	identities := make([]string, 0, len(credentials))
	for _, credential := range credentials {
		identities = append(identities, credentialIdentity(credential)+"\x00"+credential.Ref)
	}
	sort.Strings(identities)
	payload := struct {
		Global      Global            `json:"global"`
		Clients     map[string]Client `json:"clients"`
		Credentials []string          `json:"credentials"`
	}{dataset.Data.Global, dataset.Data.Clients, identities}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

var timeZero = time.Time{}

func importChanges(current *config.Config, currentCredentials []oauth.Credential, currentUsage telemetry.DataSnapshot, incoming *Dataset, native bool, mode Mode) ImportChanges {
	changes := ImportChanges{Configuration: "credentials_only"}
	if incoming == nil {
		return changes
	}
	if native {
		changes.Configuration = string(mode)
		changes.Clients.Updated = 3
		target := configFromDataset(incoming)
		if mode == ModeMerge {
			target = mergeConfig(current, target)
		}
		changes.Providers = providerChanges(current, target)
		changes.Usage = usageChanges(currentUsage, incoming.Data.Usage.toTelemetry(), mode)
	} else {
		changes.Providers = externalProviderChanges(current, currentCredentials, incoming.Data.Credentials)
	}
	changes.Credentials = credentialChanges(currentCredentials, incoming.Data.Credentials, native && mode == ModeReplace)
	return changes
}

func providerChanges(current, target *config.Config) ImportChangeCounts {
	before, after := providerMap(current), providerMap(target)
	changes := ImportChangeCounts{}
	for key, next := range after {
		previous, ok := before[key]
		if !ok {
			changes.Added++
		} else if !reflect.DeepEqual(previous, next) {
			changes.Updated++
		}
	}
	for key := range before {
		if _, ok := after[key]; !ok {
			changes.Removed++
		}
	}
	return changes
}

func providerMap(cfg *config.Config) map[string]config.Provider {
	out := map[string]config.Provider{}
	if cfg == nil {
		return out
	}
	for client, providers := range map[string][]config.Provider{"claude": cfg.Claude.Providers, "openai": cfg.OpenAI.Providers, "gemini": cfg.Gemini.Providers} {
		for _, provider := range providers {
			out[client+"\x00"+provider.Name] = provider
		}
	}
	return out
}

func credentialChanges(current []oauth.Credential, incoming []Credential, replace bool) ImportChangeCounts {
	before := map[string]struct{}{}
	for _, credential := range current {
		before[credentialIdentity(credential)] = struct{}{}
	}
	after := map[string]struct{}{}
	changes := ImportChangeCounts{}
	for _, item := range incoming {
		identity := credentialIdentity(item.toOAuth())
		after[identity] = struct{}{}
		if _, ok := before[identity]; ok {
			changes.Updated++
		} else {
			changes.Added++
		}
	}
	if replace {
		for identity := range before {
			if _, ok := after[identity]; !ok {
				changes.Removed++
			}
		}
	}
	return changes
}

func credentialIdentity(credential oauth.Credential) string {
	if identity := oauth.AccountIdentityKey(&credential); identity != "" {
		return string(credential.Provider) + "\x00" + identity
	}
	return string(credential.Provider) + "\x00" + credential.Ref
}

func usageChanges(current, incoming telemetry.DataSnapshot, mode Mode) ImportChangeCounts {
	changes := ImportChangeCounts{}
	for client, providers := range incoming.Clients {
		for provider := range providers {
			if _, ok := current.Clients[client][provider]; ok {
				changes.Updated++
			} else {
				changes.Added++
			}
		}
	}
	if mode == ModeReplace {
		for client, providers := range current.Clients {
			for provider := range providers {
				if _, ok := incoming.Clients[client][provider]; !ok {
					changes.Removed++
				}
			}
		}
	}
	return changes
}

func externalProviderChanges(current *config.Config, currentCredentials []oauth.Credential, incoming []Credential) ImportChangeCounts {
	changes := ImportChangeCounts{}
	if current == nil {
		changes.Added = len(incoming)
		return changes
	}
	credentialByRef := map[string]string{}
	for _, credential := range currentCredentials {
		credentialByRef[string(credential.Provider)+"\x00"+credential.Ref] = credentialIdentity(credential)
	}
	providers := providerMap(current)
	for _, item := range incoming {
		credential := item.toOAuth()
		identity := credentialIdentity(credential)
		client := externalCredentialClient(credential.Provider)
		matched := false
		for key, provider := range providers {
			if key[:len(client)+1] != client+"\x00" || !provider.UsesOAuth() || provider.NormalizedOAuthProvider() != credential.Provider {
				continue
			}
			providerIdentity := provider.NormalizedOAuthIdentity()
			if providerIdentity != "" {
				providerIdentity = string(credential.Provider) + "\x00" + providerIdentity
			} else {
				providerIdentity = credentialByRef[string(credential.Provider)+"\x00"+provider.NormalizedOAuthRef()]
			}
			if provider.NormalizedOAuthRef() == credential.Ref || providerIdentity == identity {
				matched = true
				break
			}
		}
		if matched {
			changes.Updated++
		} else {
			changes.Added++
		}
	}
	return changes
}

func externalCredentialClient(provider config.OAuthProvider) string {
	switch provider {
	case config.OAuthProviderCodex:
		return "openai"
	case config.OAuthProviderClaude:
		return "claude"
	default:
		return "gemini"
	}
}
