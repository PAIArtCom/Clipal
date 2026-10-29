package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/PAIArtCom/Clipal/internal/oauth"
)

type accountAttempt struct {
	credential *oauth.Credential
	request    *oauth.AccountRequestContext
}
type accountAttemptKey struct{}

// This operates only on adapter-owned metadata, not prompts, messages, tools or
// arbitrary nested JSON. The original request remains untouched across failover.
func applyOAuthEnvironment(req *http.Request, cred *oauth.Credential) (*http.Request, error) {
	if cred == nil || cred.Environment == nil {
		return req, nil
	}
	profile := cred.EnvironmentProfile()
	return applyOAuthRequestContext(req, &oauth.AccountRequestContext{
		Credential: cred.Clone(),
		Profile:    profile,
		Effective:  profile.EffectiveRequest().Clone(),
	})
}

func applyOAuthRequestContext(req *http.Request, account *oauth.AccountRequestContext) (*http.Request, error) {
	if account == nil || account.Credential == nil {
		return req, nil
	}
	cred := account.Credential
	e := account.Effective
	if e == nil {
		e = cred.EnvironmentProfile().EffectiveRequest()
	}
	if e == nil {
		return req, nil
	}
	oauth.ApplyEnvironmentHeaders(req, e)
	if cred.Provider == "codex" {
		req.Header.Set("X-Codex-Installation-Id", e.DeviceID)
	}
	if raw := req.Header.Get("X-Codex-Turn-Metadata"); raw != "" {
		var meta map[string]any
		if json.Unmarshal([]byte(raw), &meta) == nil {
			normalizeEnvironmentMetadata(meta, e)
			if environment, ok := meta["environment"].(map[string]any); ok {
				normalizeEnvironmentMetadata(environment, e)
			}
			data, err := json.Marshal(meta)
			if err != nil {
				return nil, err
			}
			req.Header.Set("X-Codex-Turn-Metadata", string(data))
		}
	}
	if req.Body != nil {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		var root map[string]any
		if json.Unmarshal(data, &root) == nil && root != nil {
			if meta, ok := root["metadata"].(map[string]any); ok {
				normalizeEnvironmentMetadata(meta, e)
				if cred.Provider == "claude" {
					raw := stringValue(meta["user_id"])
					meta["user_id"] = formatClaudeOAuthMetadataUserID(raw, sessionIDFromClaudeOAuthMetadataUserID(raw), cred)
				}
			}
			if meta, ok := root["client_metadata"].(map[string]any); ok {
				normalizeEnvironmentMetadata(meta, e)
				if cred.Provider == "codex" {
					meta["x-codex-installation-id"] = e.DeviceID
				}
			}
			if payload, ok := root["request"].(map[string]any); ok {
				if meta, ok := payload["metadata"].(map[string]any); ok {
					normalizeEnvironmentMetadata(meta, e)
				}
			}
			data, err = json.Marshal(root)
			if err != nil {
				return nil, err
			}
		}
		if cred.Provider == "claude" {
			data = signClaudeOAuthMessageBody(data)
		}
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
		req.Body = io.NopCloser(bytes.NewReader(data))
		req.ContentLength = int64(len(data))
	}
	return req.WithContext(context.WithValue(req.Context(), accountAttemptKey{}, accountAttempt{
		credential: cred.Clone(),
		request:    account.Clone(),
	})), nil
}

func (cp *ClientProxy) accountHTTPClient(req *http.Request, index int) (*http.Client, error) {
	base := cp.upstreamHTTPClient(index)
	attempt, ok := req.Context().Value(accountAttemptKey{}).(accountAttempt)
	if !ok || attempt.credential == nil || attempt.credential.Environment == nil {
		return base, nil
	}
	account := attempt.request
	if account == nil {
		profile := attempt.credential.EnvironmentProfile()
		account = &oauth.AccountRequestContext{
			Credential: attempt.credential.Clone(),
			Profile:    profile,
			Effective:  profile.EffectiveRequest().Clone(),
		}
	}
	cred := account.Credential
	if cp.oauth != nil && attempt.request == nil {
		initialized := cp.oauth.PrepareEnvironment(cred)
		if *initialized.Environment != *cred.Environment {
			profile := initialized.EnvironmentProfile()
			account = &oauth.AccountRequestContext{
				Credential: initialized.Clone(),
				Profile:    profile,
				Effective:  profile.EffectiveRequest().Clone(),
			}
			updated, err := applyOAuthRequestContext(req, account)
			if err != nil {
				return nil, err
			}
			*req = *updated
		}
		if account.Credential == nil {
			account.Credential = initialized.Clone()
		}
	}
	return cp.accountHTTPClientForContext(account, index)
}

func (cp *ClientProxy) accountHTTPClientForContext(account *oauth.AccountRequestContext, index int) (*http.Client, error) {
	base := cp.upstreamHTTPClient(index)
	if account == nil || account.Credential == nil || account.Effective == nil {
		return base, nil
	}
	cred := account.Credential
	key := fmt.Sprintf("%d\x00%s\x00%s", index, cred.Provider, account.Profile.Fingerprint())
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if cp.accountHTTPClients == nil {
		cp.accountHTTPClients = map[string]*http.Client{}
	}
	if client := cp.accountHTTPClients[key]; client != nil {
		return client, nil
	}
	client, err := oauth.EnvironmentHTTPClient(base, account.Effective, cred.Provider)
	if err != nil {
		return nil, err
	}
	// Bound retained pools across environment edits. Existing in-flight requests
	// keep their own client; only idle connections are closed.
	if len(cp.accountHTTPClients) >= 64 {
		for key, old := range cp.accountHTTPClients {
			old.CloseIdleConnections()
			delete(cp.accountHTTPClients, key)
		}
	}
	cp.accountHTTPClients[key] = client
	return client, nil
}

func normalizeEnvironmentMetadata(meta map[string]any, e *oauth.RequestEnvironment) {
	for key, value := range map[string]string{"country": e.Country, "country_code": e.Country, "language": e.Language, "locale": e.Locale, "timezone": e.Timezone, "time_zone": e.Timezone} {
		if _, exists := meta[key]; exists {
			if value != "" {
				meta[key] = value
			} else {
				delete(meta, key)
			}
		}
	}
}
