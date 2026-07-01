package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	oauthpkg "github.com/lansespirit/Clipal/internal/oauth"
	proxypkg "github.com/lansespirit/Clipal/internal/proxy"
	"github.com/lansespirit/Clipal/internal/telemetry"
)

type oauthProviderAction string

const (
	oauthProviderActionCreated  oauthProviderAction = "created"
	oauthProviderActionReused   oauthProviderAction = "reused"
	oauthProviderActionRelinked oauthProviderAction = "relinked"
)

type oauthProviderLinkResult struct {
	Provider config.Provider
	Action   oauthProviderAction
	Changed  bool
}

type oauthProviderProxySettings struct {
	Configured bool
	Mode       config.ProviderProxyMode
	URL        string
}

func (a *API) HandleListOAuthProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientType, ok := config.CanonicalClientType(strings.TrimSpace(r.URL.Query().Get("client_type")))
	if !ok {
		writeError(w, "invalid client type", http.StatusBadRequest)
		return
	}

	descriptors := oauthpkg.SupportedProvidersForClient(clientType)
	resp := make([]OAuthProviderOptionResponse, 0, len(descriptors))
	for _, descriptor := range descriptors {
		resp = append(resp, OAuthProviderOptionResponse{Provider: descriptor.Provider})
	}
	writeJSON(w, resp)
}

func (a *API) HandleStartOAuthProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req OAuthStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	clientType, ok := config.CanonicalClientType(strings.TrimSpace(req.ClientType))
	if !ok {
		writeError(w, "invalid client type", http.StatusBadRequest)
		return
	}
	provider := config.OAuthProvider(strings.ToLower(strings.TrimSpace(string(req.Provider))))
	if err := validateOAuthProviderForClient(clientType, provider); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !oauthpkg.ProviderAvailableForClient(provider, clientType) {
		writeError(w, fmt.Sprintf("oauth_provider %q is no longer available for new %s authorizations", provider, clientType), http.StatusBadRequest)
		return
	}
	proxySettings, err := oauthProxySettingsFromStartRequest(req)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	target := oauthTargetClient{
		ClientType:      clientType,
		Provider:        provider,
		ProxyConfigured: proxySettings.Configured,
		ProxyMode:       proxySettings.Mode,
		ProxyURL:        proxySettings.URL,
	}
	session, err := a.oauth.StartLoginWithHTTPClient(provider, a.oauthHTTPClientForTarget(target, provider))
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.setOAuthTargetClient(session.ID, clientType, session.ExpiresAt, proxySettings, provider)
	writeJSON(w, OAuthStartResponse{
		SessionID: session.ID,
		Provider:  session.Provider,
		AuthURL:   session.AuthURL,
		ExpiresAt: formatTimeRFC3339(session.ExpiresAt),
	})
}

func (a *API) HandleGetOAuthSession(w http.ResponseWriter, r *http.Request) {
	sessionID, subresource := extractOAuthSessionPath(r.URL.EscapedPath())
	if subresource == "code" {
		a.HandleSubmitOAuthSessionCode(w, r)
		return
	}
	if subresource == "link" {
		a.HandleLinkOAuthSession(w, r)
		return
	}
	if subresource == "cancel" {
		a.HandleCancelOAuthSession(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if sessionID == "" || subresource != "" {
		writeError(w, "invalid oauth session", http.StatusBadRequest)
		return
	}
	target, _ := a.getOAuthTargetClient(sessionID)

	session, err := a.oauth.PollLoginWithHTTPClient(sessionID, a.oauthHTTPClientForTarget(target, sessionProviderFromTarget(target)))
	if err != nil {
		a.deleteOAuthTargetClient(sessionID)
		writeError(w, err.Error(), http.StatusNotFound)
		return
	}
	a.writeOAuthSessionResponse(w, target, session, false)
	if session.Status == oauthpkg.LoginStatusExpired || session.Status == oauthpkg.LoginStatusError {
		a.deleteOAuthTargetClient(sessionID)
	}
}

func (a *API) HandleSubmitOAuthSessionCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, subresource := extractOAuthSessionPath(r.URL.EscapedPath())
	if sessionID == "" || subresource != "code" {
		writeError(w, "invalid oauth session", http.StatusBadRequest)
		return
	}

	var req OAuthSessionCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	target, ok := a.resolveOAuthTargetClient(sessionID, req.ClientType)
	if !ok {
		writeError(w, "oauth session not found", http.StatusNotFound)
		return
	}
	if strings.TrimSpace(req.Code) == "" {
		writeError(w, "authorization code is required", http.StatusBadRequest)
		return
	}

	session, err := a.oauth.CompleteLoginWithCodeWithHTTPClient(r.Context(), sessionID, req.Code, a.oauthHTTPClientForTarget(target, sessionProviderFromTarget(target)))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, oauthpkg.ErrSessionNotFound) {
			a.deleteOAuthTargetClient(sessionID)
			status = http.StatusNotFound
		} else if errors.Is(err, oauthpkg.ErrInvalidAuthorizationResponse) {
			status = http.StatusBadRequest
		}
		writeError(w, err.Error(), status)
		return
	}
	a.writeOAuthSessionResponse(w, target, session, true)
	if oauthSessionTerminal(session) {
		a.deleteOAuthTargetClient(sessionID)
	}
}

func (a *API) HandleLinkOAuthSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, subresource := extractOAuthSessionPath(r.URL.EscapedPath())
	if sessionID == "" || subresource != "link" {
		writeError(w, "invalid oauth session", http.StatusBadRequest)
		return
	}

	var req OAuthSessionLinkRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
			return
		}
	}
	target, ok := a.resolveOAuthTargetClient(sessionID, req.ClientType)
	if !ok {
		writeError(w, "oauth session not found", http.StatusNotFound)
		return
	}

	session, err := a.oauth.PollLoginWithHTTPClient(sessionID, a.oauthHTTPClientForTarget(target, sessionProviderFromTarget(target)))
	if err != nil {
		a.deleteOAuthTargetClient(sessionID)
		writeError(w, err.Error(), http.StatusNotFound)
		return
	}
	a.writeOAuthSessionResponse(w, target, session, true)
	if oauthSessionTerminal(session) {
		a.deleteOAuthTargetClient(sessionID)
	}
}

func (a *API) HandleCancelOAuthSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID, subresource := extractOAuthSessionPath(r.URL.EscapedPath())
	if sessionID == "" || subresource != "cancel" {
		writeError(w, "invalid oauth session", http.StatusBadRequest)
		return
	}

	target, _ := a.getOAuthTargetClient(sessionID)
	session, err := a.oauth.CancelLogin(sessionID)
	if err != nil {
		if errors.Is(err, oauthpkg.ErrSessionNotFound) {
			a.deleteOAuthTargetClient(sessionID)
			writeError(w, "oauth session not found", http.StatusNotFound)
			return
		}
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.deleteOAuthTargetClient(sessionID)
	a.writeOAuthSessionResponse(w, target, session, false)
}

func (a *API) HandleListOAuthAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provider := extractOAuthAccountsProvider(r.URL.EscapedPath())
	if provider == "" {
		writeError(w, "invalid oauth provider", http.StatusBadRequest)
		return
	}

	accounts, err := a.oauth.List(provider)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cfg := a.loadConfigOrWriteError(w)
	if cfg == nil {
		return
	}
	linked := linkedOAuthProviders(cfg, provider)
	resp := make([]OAuthAccountResponse, 0, len(accounts))
	for _, account := range accounts {
		resp = append(resp, OAuthAccountResponse{
			Provider:        account.Provider,
			Ref:             account.Ref,
			Email:           account.Email,
			ExpiresAt:       formatTimeRFC3339(account.ExpiresAt),
			LastRefresh:     formatTimeRFC3339(account.LastRefresh),
			LinkedProviders: linked[account.Ref],
		})
	}
	writeJSON(w, resp)
}

func (a *API) HandleDeleteOAuthAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provider, ref := extractOAuthAccountRef(r.URL.EscapedPath())
	if provider == "" || ref == "" {
		writeError(w, "invalid oauth account path", http.StatusBadRequest)
		return
	}

	a.configMu.Lock()
	defer a.configMu.Unlock()

	cfg := a.loadConfigOrWriteError(w)
	if cfg == nil {
		return
	}
	_, err := a.deleteOAuthAccountLocked(cfg, provider, ref)
	if err != nil {
		writeAPIError(w, newAPIError(http.StatusInternalServerError, fmt.Sprintf("failed to delete oauth account: %v", err), err))
		return
	}
	writeJSON(w, SuccessResponse{Message: "oauth account deleted successfully"})
}

func (a *API) HandleGetProviderOAuthMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientType, providerName, subresource := extractClientProviderSubresource(r.URL.EscapedPath())
	if clientType == "" || providerName == "" || subresource != "oauth-metadata" {
		writeError(w, "invalid client type or provider name", http.StatusBadRequest)
		return
	}

	cfg := a.loadConfigOrWriteError(w)
	if cfg == nil {
		return
	}
	cc, err := getClientConfigRef(cfg, clientType)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	provider := providerByName(cc.Providers, providerName)
	if provider == nil {
		writeError(w, "provider not found", http.StatusNotFound)
		return
	}
	if !provider.UsesOAuth() {
		writeError(w, "provider does not use oauth", http.StatusBadRequest)
		return
	}
	oauthProvider := provider.NormalizedOAuthProvider()
	if oauthProvider != config.OAuthProviderCodex && oauthProvider != config.OAuthProviderClaude {
		writeError(w, "oauth metadata is unavailable for this provider", http.StatusBadRequest)
		return
	}

	usageCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	httpClient := proxypkg.NewOAuthHTTPClientForProvider(*provider, cfg.Global)
	var resp ProviderOAuthMetadataResponse
	var metadataErr error
	switch oauthProvider {
	case config.OAuthProviderCodex:
		var details *oauthpkg.CodexUsageDetails
		details, metadataErr = a.oauth.GetCodexUsageWithHTTPClient(usageCtx, provider.NormalizedOAuthRef(), httpClient)
		if details != nil {
			resp = ProviderOAuthMetadataResponse{
				OAuthPlanType:   details.PlanType,
				OAuthRateLimits: mapProviderCodexOAuthLimitsResponse(details),
			}
		}
	case config.OAuthProviderClaude:
		var details *oauthpkg.ClaudeUsageDetails
		details, metadataErr = a.oauth.GetClaudeUsageWithHTTPClient(usageCtx, provider.NormalizedOAuthRef(), httpClient)
		if details != nil {
			resp = ProviderOAuthMetadataResponse{
				OAuthPlanType:   firstNonEmptyString(details.PlanType, claudeOAuthPlanType(a.oauth, provider.NormalizedOAuthRef())),
				OAuthRateLimits: mapProviderClaudeOAuthLimitsResponse(details),
			}
		}
	}
	cancel()
	if metadataErr != nil {
		writeAPIError(w, newAPIError(http.StatusBadGateway, fmt.Sprintf("failed to load oauth metadata: %v", metadataErr), metadataErr))
		return
	}

	writeJSON(w, resp)
}

func (a *API) toProviderResponses(providers []config.Provider, usageByProvider map[string]telemetry.ProviderUsage) []ProviderResponse {
	out := toProviderResponses(providers, usageByProvider)
	now := time.Now()
	for i := range providers {
		if !providers[i].UsesOAuth() {
			continue
		}
		out[i].OAuthAuthStatus = oauthAuthStatus(nil, now)
		cred, err := a.oauth.Load(providers[i].NormalizedOAuthProvider(), providers[i].NormalizedOAuthRef())
		if err != nil {
			continue
		}
		out[i].DisplayName = oauthDisplayName(cred, providers[i])
		out[i].OAuthAuthStatus = oauthAuthStatus(cred, now)
		out[i].OAuthExpiresAt = formatTimeRFC3339(cred.ExpiresAt)
		out[i].OAuthLastRefresh = formatTimeRFC3339(cred.LastRefresh)
	}
	return out
}

func mapProviderCodexOAuthLimitsResponse(details *oauthpkg.CodexUsageDetails) *ProviderOAuthLimits {
	if details == nil {
		return nil
	}
	resp := &ProviderOAuthLimits{
		Primary:   mapProviderCodexOAuthLimitWindow(details.Primary),
		Secondary: mapProviderCodexOAuthLimitWindow(details.Secondary),
	}
	for _, item := range details.Additional {
		resp.Additional = append(resp.Additional, ProviderOAuthAdditionalLimit{
			LimitID:   strings.TrimSpace(item.LimitID),
			LimitName: strings.TrimSpace(item.LimitName),
			Primary:   mapProviderCodexOAuthLimitWindow(item.Primary),
			Secondary: mapProviderCodexOAuthLimitWindow(item.Secondary),
		})
	}
	if resp.Primary == nil && resp.Secondary == nil && len(resp.Additional) == 0 {
		return nil
	}
	return resp
}

func mapProviderCodexOAuthLimitWindow(window *oauthpkg.CodexUsageWindow) *ProviderOAuthLimitWindow {
	if window == nil {
		return nil
	}
	return &ProviderOAuthLimitWindow{
		UsedPercent:   window.UsedPercent,
		WindowMinutes: window.WindowMinutes,
		ResetsAt:      formatTimeRFC3339(window.ResetsAt),
	}
}

func mapProviderClaudeOAuthLimitsResponse(details *oauthpkg.ClaudeUsageDetails) *ProviderOAuthLimits {
	if details == nil {
		return nil
	}
	resp := &ProviderOAuthLimits{
		Primary:   mapProviderClaudeOAuthLimitWindow(details.FiveHour, 5*time.Hour),
		Secondary: mapProviderClaudeOAuthLimitWindow(details.SevenDay, 7*24*time.Hour),
	}
	appendAdditional := func(id string, name string, window *oauthpkg.ClaudeUsageWindow, duration time.Duration) {
		if mapped := mapProviderClaudeOAuthLimitWindow(window, duration); mapped != nil {
			resp.Additional = append(resp.Additional, ProviderOAuthAdditionalLimit{
				LimitID:   id,
				LimitName: name,
				Primary:   mapped,
			})
		}
	}
	appendAdditional("claude_oauth_apps", "OAuth apps", details.SevenDayOAuthApps, 7*24*time.Hour)
	appendAdditional("claude_opus", "Opus", details.SevenDayOpus, 7*24*time.Hour)
	appendAdditional("claude_sonnet", "Sonnet", details.SevenDaySonnet, 7*24*time.Hour)
	if extra := mapProviderClaudeExtraUsageLimitWindow(details.ExtraUsage); extra != nil {
		resp.Additional = append(resp.Additional, ProviderOAuthAdditionalLimit{
			LimitID:   "claude_extra_usage",
			LimitName: "Extra usage",
			Primary:   extra,
		})
	}
	if resp.Primary == nil && resp.Secondary == nil && len(resp.Additional) == 0 {
		return nil
	}
	return resp
}

func mapProviderClaudeOAuthLimitWindow(window *oauthpkg.ClaudeUsageWindow, duration time.Duration) *ProviderOAuthLimitWindow {
	if window == nil {
		return nil
	}
	return &ProviderOAuthLimitWindow{
		UsedPercent:   window.Utilization,
		WindowMinutes: int(duration / time.Minute),
		ResetsAt:      formatTimeRFC3339(window.ResetsAt),
	}
}

func mapProviderClaudeExtraUsageLimitWindow(extra *oauthpkg.ClaudeExtraUsage) *ProviderOAuthLimitWindow {
	if extra == nil || !extra.IsEnabled {
		return nil
	}
	usedPercent := 0.0
	switch {
	case extra.Utilization != nil:
		usedPercent = *extra.Utilization
	case extra.MonthlyLimit != nil && extra.UsedCredits != nil && *extra.MonthlyLimit > 0:
		usedPercent = (float64(*extra.UsedCredits) / float64(*extra.MonthlyLimit)) * 100
	default:
		return nil
	}
	return &ProviderOAuthLimitWindow{UsedPercent: usedPercent}
}

func claudeOAuthPlanType(svc *oauthpkg.Service, ref string) string {
	if svc == nil {
		return ""
	}
	cred, err := svc.Load(config.OAuthProviderClaude, ref)
	if err != nil || cred == nil || cred.Metadata == nil {
		return ""
	}
	return strings.TrimSpace(cred.Metadata["plan_type"])
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func oauthDisplayName(cred *oauthpkg.Credential, provider config.Provider) string {
	if cred != nil {
		if email := strings.TrimSpace(cred.Email); email != "" {
			return email
		}
	}
	return strings.TrimSpace(provider.Name)
}

func oauthAuthStatus(cred *oauthpkg.Credential, now time.Time) string {
	if cred == nil {
		return "reauth_needed"
	}

	hasAccessToken := strings.TrimSpace(cred.AccessToken) != ""
	hasRefreshToken := strings.TrimSpace(cred.RefreshToken) != ""
	needsRefresh := cred.NeedsRefresh(now, 0)

	switch {
	case !hasAccessToken && hasRefreshToken:
		return "refresh_due"
	case !hasAccessToken:
		return "reauth_needed"
	case needsRefresh && hasRefreshToken:
		return "refresh_due"
	case needsRefresh:
		return "reauth_needed"
	default:
		return "ready"
	}
}

func ensureOAuthProviderLinked(cc *config.ClientConfig, cred *oauthpkg.Credential, lookup func(config.OAuthProvider, string) (*oauthpkg.Credential, error), proxySettings ...oauthProviderProxySettings) oauthProviderLinkResult {
	if cc == nil || cred == nil {
		return oauthProviderLinkResult{}
	}
	settings := oauthProviderProxySettings{}
	if len(proxySettings) > 0 {
		settings = proxySettings[0]
	}
	if provider := findLinkedOAuthProvider(cc.Providers, cred.Provider, cred.Ref); provider != nil {
		changed := backfillOAuthProviderIdentity(provider, cred)
		return oauthProviderLinkResult{
			Provider: *provider,
			Action:   oauthProviderActionReused,
			Changed:  changed,
		}
	}

	if provider := findRelinkableOAuthProvider(cc.Providers, cred, lookup); provider != nil {
		applyOAuthCredentialToProvider(provider, cred)
		_ = applyOAuthProviderProxySettings(provider, settings)
		return oauthProviderLinkResult{
			Provider: *provider,
			Action:   oauthProviderActionRelinked,
			Changed:  true,
		}
	}

	name := nextAvailableOAuthProviderName(cc.Providers, desiredOAuthProviderName(cred))
	provider := config.Provider{
		Name:          name,
		AuthType:      config.ProviderAuthTypeOAuth,
		OAuthProvider: cred.Provider,
		OAuthRef:      cred.Ref,
		OAuthIdentity: oauthpkg.AccountIdentityKey(cred),
		Priority:      nextProviderPriority(cc.Providers),
		Enabled:       ptr(true),
	}
	applyOAuthProviderProxySettings(&provider, settings)
	cc.Providers = append(cc.Providers, provider)
	return oauthProviderLinkResult{
		Provider: provider,
		Action:   oauthProviderActionCreated,
		Changed:  true,
	}
}

func applyOAuthProviderProxySettings(provider *config.Provider, settings oauthProviderProxySettings) bool {
	if provider == nil || !settings.Configured {
		return false
	}
	oldMode := provider.NormalizedProxyMode()
	oldURL := provider.NormalizedProxyURL()
	mode := string(settings.Mode)
	proxyURL := settings.URL
	_ = config.ApplyProviderProxySettings(provider, config.ProviderProxySettingsPatch{
		Mode: &mode,
		URL:  &proxyURL,
	}, true)
	return provider.NormalizedProxyMode() != oldMode || provider.NormalizedProxyURL() != oldURL
}

func findLinkedOAuthProvider(providers []config.Provider, oauthProvider config.OAuthProvider, ref string) *config.Provider {
	for i := range providers {
		if providers[i].UsesOAuth() &&
			providers[i].NormalizedOAuthProvider() == oauthProvider &&
			providers[i].NormalizedOAuthRef() == ref {
			return &providers[i]
		}
	}
	return nil
}

func findRelinkableOAuthProvider(providers []config.Provider, cred *oauthpkg.Credential, lookup func(config.OAuthProvider, string) (*oauthpkg.Credential, error)) *config.Provider {
	if cred == nil {
		return nil
	}
	desiredName := desiredOAuthProviderName(cred)
	var fallback *config.Provider
	ambiguousFallback := false

	for i := range providers {
		provider := &providers[i]
		if !provider.UsesOAuth() || provider.NormalizedOAuthProvider() != cred.Provider {
			continue
		}
		if desiredName != "" && provider.Name == desiredName {
			if canRelinkOAuthProvider(provider, cred, lookup) {
				return provider
			}
			continue
		}
		if !canRelinkOAuthProvider(provider, cred, lookup) {
			continue
		}
		if fallback == nil {
			fallback = provider
			continue
		}
		ambiguousFallback = true
	}
	if ambiguousFallback {
		return nil
	}
	return fallback
}

func applyOAuthCredentialToProvider(provider *config.Provider, cred *oauthpkg.Credential) {
	if provider == nil || cred == nil {
		return
	}
	config.NormalizeProviderAuthSettings(provider)
	provider.AuthType = config.ProviderAuthTypeOAuth
	provider.OAuthProvider = cred.Provider
	provider.OAuthRef = cred.Ref
	provider.OAuthIdentity = oauthpkg.AccountIdentityKey(cred)
	config.NormalizeProviderAuthSettings(provider)
}

func backfillOAuthProviderIdentity(provider *config.Provider, cred *oauthpkg.Credential) bool {
	if provider == nil || cred == nil {
		return false
	}
	next := oauthpkg.AccountIdentityKey(cred)
	if next == "" || provider.NormalizedOAuthIdentity() == next {
		return false
	}
	provider.OAuthIdentity = next
	config.NormalizeProviderAuthSettings(provider)
	return true
}

func canRelinkOAuthProvider(provider *config.Provider, cred *oauthpkg.Credential, lookup func(config.OAuthProvider, string) (*oauthpkg.Credential, error)) bool {
	if provider == nil || cred == nil {
		return false
	}
	if !provider.UsesOAuth() || provider.NormalizedOAuthProvider() != cred.Provider {
		return false
	}

	currentRef := provider.NormalizedOAuthRef()
	switch {
	case currentRef == "":
		return providerMatchesOAuthIdentity(provider, cred)
	case currentRef == cred.Ref:
		return true
	case lookup == nil:
		return providerMatchesOAuthIdentity(provider, cred)
	}

	currentCred, err := lookup(cred.Provider, currentRef)
	if err != nil {
		return errors.Is(err, os.ErrNotExist) && providerMatchesOAuthIdentity(provider, cred)
	}
	return oauthpkg.SameAccountIdentity(currentCred, cred)
}

func providerMatchesOAuthIdentity(provider *config.Provider, cred *oauthpkg.Credential) bool {
	if provider == nil || cred == nil {
		return false
	}
	identity := provider.NormalizedOAuthIdentity()
	return identity != "" && identity == oauthpkg.AccountIdentityKey(cred)
}

func desiredOAuthProviderName(cred *oauthpkg.Credential) string {
	if cred == nil {
		return "oauth-account"
	}

	providerPart := slugOAuthProviderNamePart(string(cred.Provider))
	identityPart := slugOAuthProviderNamePart(cred.Email)
	if cred.Provider == config.OAuthProviderGemini || cred.Provider == config.OAuthProviderAntigravity {
		projectPart := slugOAuthProviderNamePart(cred.AccountID)
		if projectPart == "" {
			projectPart = slugOAuthProviderNamePart(cred.Metadata["project_id"])
		}
		switch {
		case identityPart != "" && projectPart != "":
			identityPart = normalizeOAuthProviderNameBase(identityPart + "-" + projectPart)
		case projectPart != "":
			identityPart = projectPart
		}
	}
	if identityPart == "" {
		identityPart = slugOAuthProviderNamePart(cred.Ref)
	}

	switch {
	case providerPart != "" && identityPart != "":
		return normalizeOAuthProviderNameBase(providerPart + "-" + identityPart)
	case identityPart != "":
		return normalizeOAuthProviderNameBase(identityPart)
	case providerPart != "":
		return normalizeOAuthProviderNameBase(providerPart + "-account")
	default:
		return "oauth-account"
	}
}

func nextAvailableOAuthProviderName(providers []config.Provider, desired string) string {
	name := normalizeOAuthProviderNameBase(desired)
	if !providerNameExists(providers, name) {
		return name
	}

	for suffix := 2; ; suffix++ {
		candidate := providerNameWithNumericSuffix(name, suffix)
		if !providerNameExists(providers, candidate) {
			return candidate
		}
	}
}

func normalizeOAuthProviderNameBase(base string) string {
	base = slugOAuthProviderNamePart(base)
	if base == "" {
		base = "oauth-account"
	}
	if len(base) > providerNameMaxLen {
		base = base[:providerNameMaxLen]
	}
	base = strings.Trim(base, "-._")
	if base == "" {
		return "oauth-account"
	}
	return base
}

func slugOAuthProviderNamePart(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return ""
	}

	var b strings.Builder
	lastDash := false
	for _, r := range v {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			_, _ = b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			_ = b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func providerNameWithNumericSuffix(base string, suffix int) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "oauth-account"
	}

	suffixText := fmt.Sprintf("-%d", suffix)
	maxBaseLen := providerNameMaxLen - len(suffixText)
	if maxBaseLen < 1 {
		maxBaseLen = 1
	}
	if len(base) > maxBaseLen {
		base = base[:maxBaseLen]
	}
	base = strings.TrimRight(base, "-._")
	if base == "" {
		base = "oauth"
		if len(base) > maxBaseLen {
			base = base[:maxBaseLen]
		}
	}
	return base + suffixText
}

func linkedOAuthProviders(cfg *config.Config, provider config.OAuthProvider) map[string][]string {
	out := make(map[string][]string)
	if cfg == nil {
		return out
	}
	appendClient := func(clientType string, providers []config.Provider) {
		for _, p := range providers {
			if !p.UsesOAuth() || p.NormalizedOAuthProvider() != provider {
				continue
			}
			out[p.NormalizedOAuthRef()] = append(out[p.NormalizedOAuthRef()], clientType+"/"+p.Name)
		}
	}
	appendClient("claude", cfg.Claude.Providers)
	appendClient("openai", cfg.OpenAI.Providers)
	appendClient("gemini", cfg.Gemini.Providers)
	for ref := range out {
		sort.Strings(out[ref])
	}
	return out
}

func formatTimeRFC3339(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.Format(time.RFC3339)
}

func oauthProxySettingsFromStartRequest(req OAuthStartRequest) (oauthProviderProxySettings, error) {
	if req.ProxyMode == nil && req.ProxyURL == nil {
		return oauthProviderProxySettings{}, nil
	}
	provider := config.Provider{}
	if err := config.ApplyProviderProxySettings(&provider, config.ProviderProxySettingsPatch{
		Mode: req.ProxyMode,
		URL:  req.ProxyURL,
	}, false); err != nil {
		return oauthProviderProxySettings{}, err
	}
	if provider.NormalizedProxyMode() == config.ProviderProxyModeDefault && provider.NormalizedProxyURL() == "" {
		return oauthProviderProxySettings{}, nil
	}
	return oauthProviderProxySettings{
		Configured: true,
		Mode:       provider.NormalizedProxyMode(),
		URL:        provider.NormalizedProxyURL(),
	}, nil
}

func (target oauthTargetClient) proxySettings() oauthProviderProxySettings {
	return oauthProviderProxySettings{
		Configured: target.ProxyConfigured,
		Mode:       target.ProxyMode,
		URL:        target.ProxyURL,
	}
}

func sessionProviderFromTarget(target oauthTargetClient) config.OAuthProvider {
	return config.OAuthProvider(strings.ToLower(strings.TrimSpace(string(target.Provider))))
}

func (a *API) oauthHTTPClientForTarget(target oauthTargetClient, oauthProvider config.OAuthProvider) *http.Client {
	cfg, err := config.Load(a.configDir)
	if err != nil {
		return nil
	}
	globalMode := cfg.Global.NormalizedUpstreamProxyMode()
	if target.ProxyConfigured && target.ProxyMode == config.ProviderProxyModeDirect && oauthProvider == config.OAuthProviderClaude {
		return nil
	}
	if !target.ProxyConfigured && globalMode == config.GlobalUpstreamProxyModeDirect && oauthProvider == config.OAuthProviderClaude {
		return nil
	}
	if !target.ProxyConfigured {
		switch globalMode {
		case config.GlobalUpstreamProxyModeDirect, config.GlobalUpstreamProxyModeCustom:
		default:
			if !proxypkg.OAuthProviderUsesEnvironmentProxy(oauthProvider) {
				return nil
			}
		}
	}
	provider := config.Provider{
		Name:          "oauth-session",
		OAuthProvider: oauthProvider,
	}
	if target.ProxyConfigured {
		provider.ProxyMode = target.ProxyMode
		provider.ProxyURL = target.ProxyURL
	}
	return proxypkg.NewOAuthHTTPClientForProvider(provider, cfg.Global)
}

func (a *API) writeOAuthSessionResponse(w http.ResponseWriter, target oauthTargetClient, session *oauthpkg.LoginSession, linkCompleted bool) {
	if session == nil {
		writeError(w, "oauth session not found", http.StatusNotFound)
		return
	}
	clientType := strings.TrimSpace(target.ClientType)
	resp := OAuthSessionResponse{
		SessionID:     session.ID,
		Provider:      session.Provider,
		Status:        string(session.Status),
		AuthURL:       session.AuthURL,
		ExpiresAt:     formatTimeRFC3339(session.ExpiresAt),
		CredentialRef: session.CredentialRef,
		Email:         session.Email,
		Error:         session.Error,
	}
	if session.Status != oauthpkg.LoginStatusCompleted {
		writeJSON(w, resp)
		return
	}

	if clientType == "" {
		resp.DisplayName = strings.TrimSpace(session.Email)
		writeJSON(w, resp)
		return
	}

	if !linkCompleted {
		resp.DisplayName = strings.TrimSpace(session.Email)
		cfg := a.loadConfigOrWriteError(w)
		if cfg == nil {
			return
		}
		cc, err := getClientConfigRef(cfg, clientType)
		if err != nil {
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}
		if provider := findLinkedOAuthProvider(cc.Providers, session.Provider, session.CredentialRef); provider != nil {
			resp.ProviderName = provider.Name
			resp.ProviderAction = string(oauthProviderActionReused)
			if resp.DisplayName == "" {
				resp.DisplayName = oauthDisplayName(nil, *provider)
			}
		}
		writeJSON(w, resp)
		return
	}

	cred, err := a.oauth.Load(session.Provider, session.CredentialRef)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		writeError(w, err.Error(), status)
		return
	}
	resp.DisplayName = strings.TrimSpace(cred.Email)

	a.configMu.Lock()
	defer a.configMu.Unlock()

	cfg := a.loadConfigOrWriteError(w)
	if cfg == nil {
		return
	}
	cc, err := getClientConfigRef(cfg, clientType)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	link := ensureOAuthProviderLinked(cc, cred, a.oauth.Load, target.proxySettings())
	if link.Changed {
		if !a.saveClientConfigOrWriteError(w, clientType, cfg) {
			return
		}
	}
	resp.ProviderName = link.Provider.Name
	resp.ProviderAction = string(link.Action)
	resp.DisplayName = oauthDisplayName(cred, link.Provider)
	writeJSON(w, resp)
}

func extractOAuthSessionPath(path string) (string, string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "oauth" && parts[2] == "sessions" {
		sessionID := strings.TrimSpace(parts[3])
		if len(parts) == 4 {
			return sessionID, ""
		}
		if len(parts) == 5 {
			return sessionID, strings.TrimSpace(parts[4])
		}
	}
	return "", ""
}

func extractOAuthAccountsProvider(path string) config.OAuthProvider {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "oauth" && parts[2] == "accounts" {
		return config.OAuthProvider(strings.ToLower(strings.TrimSpace(parts[3])))
	}
	return ""
}

func extractOAuthAccountRef(path string) (config.OAuthProvider, string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "oauth" && parts[2] == "accounts" {
		return config.OAuthProvider(strings.ToLower(strings.TrimSpace(parts[3]))), strings.TrimSpace(parts[4])
	}
	return "", ""
}

func (a *API) setOAuthTargetClient(sessionID string, clientType string, expiresAt time.Time, proxySettings oauthProviderProxySettings, provider ...config.OAuthProvider) {
	a.oauthMu.Lock()
	defer a.oauthMu.Unlock()
	a.sweepOAuthTargetsLocked(time.Now())
	oauthProvider := config.OAuthProvider("")
	if len(provider) > 0 {
		oauthProvider = config.OAuthProvider(strings.ToLower(strings.TrimSpace(string(provider[0]))))
	}
	a.oauthTargets[strings.TrimSpace(sessionID)] = oauthTargetClient{
		ClientType:      strings.TrimSpace(clientType),
		Provider:        oauthProvider,
		ProxyConfigured: proxySettings.Configured,
		ProxyMode:       proxySettings.Mode,
		ProxyURL:        proxySettings.URL,
		ExpiresAt:       expiresAt,
	}
}

func (a *API) getOAuthTargetClient(sessionID string) (oauthTargetClient, bool) {
	a.oauthMu.Lock()
	defer a.oauthMu.Unlock()
	a.sweepOAuthTargetsLocked(time.Now())
	target, ok := a.oauthTargets[strings.TrimSpace(sessionID)]
	if !ok || strings.TrimSpace(target.ClientType) == "" {
		return oauthTargetClient{}, false
	}
	return target, true
}

func (a *API) resolveOAuthTargetClient(sessionID string, fallback string) (oauthTargetClient, bool) {
	if target, ok := a.getOAuthTargetClient(sessionID); ok {
		return target, true
	}
	clientType, ok := config.CanonicalClientType(strings.TrimSpace(fallback))
	if !ok {
		return oauthTargetClient{}, false
	}
	return oauthTargetClient{ClientType: clientType}, true
}

func (a *API) deleteOAuthTargetClient(sessionID string) {
	a.oauthMu.Lock()
	defer a.oauthMu.Unlock()
	delete(a.oauthTargets, strings.TrimSpace(sessionID))
}

func (a *API) sweepOAuthTargetsLocked(now time.Time) {
	for sessionID, target := range a.oauthTargets {
		if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(target.ClientType) == "" {
			delete(a.oauthTargets, sessionID)
			continue
		}
		if !target.ExpiresAt.IsZero() && !target.ExpiresAt.After(now) {
			delete(a.oauthTargets, sessionID)
		}
	}
}

func oauthSessionTerminal(session *oauthpkg.LoginSession) bool {
	if session == nil {
		return false
	}
	switch session.Status {
	case oauthpkg.LoginStatusCompleted, oauthpkg.LoginStatusExpired, oauthpkg.LoginStatusError:
		return true
	default:
		return false
	}
}
