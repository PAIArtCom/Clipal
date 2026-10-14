package oauth

import (
	"github.com/PAIArtCom/Clipal/internal/config"
)

func EnvironmentTarget(provider config.OAuthProvider) string {
	switch provider {
	case config.OAuthProviderClaude:
		return "https://api.anthropic.com/"
	case config.OAuthProviderCodex:
		return "https://chatgpt.com/"
	default:
		return "https://daily-cloudcode-pa.googleapis.com/"
	}
}

// PrepareEnvironment never infers account regions. All writes reuse the transfer
// barrier protocol; unconfirmed legacy values await an explicit user action.
func (s *Service) PrepareEnvironment(cred *Credential) *Credential {
	release := lockStoreForRefresh()
	defer release()
	return s.prepareEnvironment(s.store.withTransferLockBypass(), cred)
}
func (s *Service) prepareEnvironment(store *Store, cred *Credential) *Credential {
	if cred == nil || !cred.environmentPendingWrite || cred.Environment.NeedsConfirmation {
		return cred
	}
	latest, err := store.PersistEnvironment(cred)
	if err != nil {
		return cred
	}
	result := cred.Clone()
	result.Environment = latest.Environment.Clone()
	result.environmentPendingWrite = false
	return result
}
