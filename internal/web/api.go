package web

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/integration"
	"github.com/lansespirit/Clipal/internal/logger"
	oauthpkg "github.com/lansespirit/Clipal/internal/oauth"
	"github.com/lansespirit/Clipal/internal/proxy"
	"github.com/lansespirit/Clipal/internal/telemetry"
	transferpkg "github.com/lansespirit/Clipal/internal/transfer"
)

var startTime = time.Now()

const (
	providerNameMaxLen = 64
)

// API handles all API requests for the management interface
type API struct {
	configDir    string
	version      string
	runtime      *proxy.Router
	telemetry    *telemetry.Store
	transfer     *transferpkg.Service
	integrations *integration.Manager
	oauth        *oauthpkg.Service
	oauthMu      sync.Mutex
	oauthTargets map[string]oauthTargetClient
	configMu     sync.Mutex
}

type oauthTargetClient struct {
	ClientType      string
	Provider        config.OAuthProvider
	ProxyConfigured bool
	ProxyMode       config.ProviderProxyMode
	ProxyURL        string
	ExpiresAt       time.Time
}

// NewAPI creates a new API handler
func NewAPI(configDir, version string, runtime *proxy.Router) *API {
	var telemetryStore *telemetry.Store
	if runtime != nil {
		telemetryStore = runtime.TelemetryStore()
	} else {
		var err error
		telemetryStore, err = telemetry.NewStore(configDir)
		if err != nil {
			logger.Warn("failed to load usage telemetry from %s: %v", configDir, err)
		}
	}
	api := &API{
		configDir:    configDir,
		version:      version,
		runtime:      runtime,
		telemetry:    telemetryStore,
		integrations: integration.NewManager(configDir),
		oauth:        oauthpkg.NewService(configDir),
		oauthTargets: make(map[string]oauthTargetClient),
	}
	var transferOptions []transferpkg.ServiceOption
	if runtime != nil {
		transferOptions = append(transferOptions, transferpkg.WithApplyCoordinator(runtime.CoordinateDataTransfer))
	}
	transferService, err := transferpkg.NewService(configDir, version, telemetryStore, nil, transferOptions...)
	if err != nil {
		logger.Warn("failed to initialize data transfer service: %v", err)
	} else {
		api.transfer = transferService
	}
	return api
}

func (a *API) reloadRuntimeProviderConfigs() error {
	if a.runtime == nil {
		return nil
	}
	return a.runtime.ReloadProviderConfigs()
}

// HandleGetGlobalConfig returns the current global configuration
func (a *API) HandleGetGlobalConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := a.loadConfigOrWriteError(w)
	if cfg == nil {
		return
	}

	writeJSON(w, toGlobalConfigResponse(cfg.Global))
}

// HandleUpdateGlobalConfig updates the global configuration
func (a *API) HandleUpdateGlobalConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req GlobalConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Load current config
	a.configMu.Lock()
	defer a.configMu.Unlock()

	cfg := a.loadConfigOrWriteError(w)
	if cfg == nil {
		return
	}

	// Update global config
	cfg.Global.ListenAddr = req.ListenAddr
	cfg.Global.AllowRemoteProxy = req.AllowRemoteProxy
	cfg.Global.AllowRemoteWebUI = req.AllowRemoteWebUI
	cfg.Global.Port = req.Port
	cfg.Global.LogLevel = config.LogLevel(req.LogLevel)
	cfg.Global.ReactivateAfter = req.ReactivateAfter
	cfg.Global.UpstreamIdleTimeout = req.UpstreamIdleTimeout
	// Backwards compatible: if the UI/client doesn't send this (older versions),
	// keep the current/default value instead of overwriting with empty.
	if strings.TrimSpace(req.ResponseHeaderTimeout) != "" {
		cfg.Global.ResponseHeaderTimeout = req.ResponseHeaderTimeout
	}
	if err := config.ApplyUpstreamProxySettings(&cfg.Global, config.UpstreamProxySettingsPatch{
		Mode: req.UpstreamProxyMode,
		URL:  req.UpstreamProxyURL,
	}); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg.Global.MaxRequestBody = req.MaxRequestBodyBytes
	cfg.Global.LogDir = req.LogDir
	cfg.Global.LogRetentionDays = req.LogRetentionDays
	cfg.Global.LogStdout = req.LogStdout
	cfg.Global.Notifications.Enabled = req.Notifications.Enabled
	cfg.Global.Notifications.MinLevel = config.LogLevel(req.Notifications.MinLevel)
	cfg.Global.Notifications.ProviderSwitch = req.Notifications.ProviderSwitch
	cfg.Global.CircuitBreaker.FailureThreshold = req.CircuitBreaker.FailureThreshold
	cfg.Global.CircuitBreaker.SuccessThreshold = req.CircuitBreaker.SuccessThreshold
	cfg.Global.CircuitBreaker.OpenTimeout = req.CircuitBreaker.OpenTimeout
	cfg.Global.CircuitBreaker.HalfOpenMaxInFlight = req.CircuitBreaker.HalfOpenMaxInFlight
	if req.Routing.StickySessions.Enabled != nil {
		cfg.Global.Routing.StickySessions.Enabled = *req.Routing.StickySessions.Enabled
	}
	if req.Routing.StickySessions.ExplicitTTL != nil {
		cfg.Global.Routing.StickySessions.ExplicitTTL = *req.Routing.StickySessions.ExplicitTTL
	}
	if req.Routing.BusyBackpressure.Enabled != nil {
		cfg.Global.Routing.BusyBackpressure.Enabled = *req.Routing.BusyBackpressure.Enabled
	}
	if req.Routing.BusyBackpressure.ShortRetryAfterMax != nil {
		cfg.Global.Routing.BusyBackpressure.ShortRetryAfterMax = *req.Routing.BusyBackpressure.ShortRetryAfterMax
	}
	if req.Routing.BusyBackpressure.MaxInlineWait != nil {
		cfg.Global.Routing.BusyBackpressure.MaxInlineWait = *req.Routing.BusyBackpressure.MaxInlineWait
	}

	if !a.saveGlobalConfigOrWriteError(w, cfg) {
		return
	}

	logger.Info("global configuration updated via web interface")
	writeJSON(w, SuccessResponse{Message: "configuration updated successfully"})
}

// HandleGetProviders returns providers for a specific client
func (a *API) HandleGetProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientType := extractClientType(r.URL.EscapedPath())
	if clientType == "" {
		writeError(w, "invalid client type", http.StatusBadRequest)
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

	writeJSON(w, a.toProviderResponses(cc.Providers, a.providerUsageSnapshots(clientType)))
}

func (a *API) HandleGetClientConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientType := extractClientTypeFromClientConfigPath(r.URL.EscapedPath())
	if clientType == "" {
		writeError(w, "invalid client type", http.StatusBadRequest)
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

	writeJSON(w, ClientConfigResponse{
		Mode:            string(cc.Mode),
		PinnedProvider:  cc.PinnedProvider,
		OverrideSupport: toProviderOverrideSupport(providerOverrideSupportForClient(clientType)),
	})
}

func (a *API) HandleUpdateClientConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientType := extractClientTypeFromClientConfigPath(r.URL.EscapedPath())
	if clientType == "" {
		writeError(w, "invalid client type", http.StatusBadRequest)
		return
	}

	var req ClientConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	a.configMu.Lock()
	defer a.configMu.Unlock()

	cfg := a.loadConfigOrWriteError(w)
	if cfg == nil {
		return
	}

	mode := config.ClientMode(strings.ToLower(strings.TrimSpace(req.Mode)))
	pin := strings.TrimSpace(req.PinnedProvider)

	cc, err := getClientConfigRef(cfg, clientType)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	cc.Mode = mode
	cc.PinnedProvider = pin
	if !a.saveClientConfigOrWriteError(w, clientType, cfg) {
		return
	}

	logger.Info("client configuration updated for %s via web interface", clientType)
	writeJSON(w, SuccessResponse{Message: "configuration updated successfully"})
}

// HandleAddProvider adds a new provider for a specific client
func (a *API) HandleAddProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientType := extractClientType(r.URL.EscapedPath())
	if clientType == "" {
		writeError(w, "invalid client type", http.StatusBadRequest)
		return
	}

	var req ProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	normalizeProviderRequest(&req)
	req.Overrides = normalizeProviderOverrideRequest(req.Overrides)
	if err := validateProviderOverrideRequest(clientType, req.Overrides); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if providerRequestUsesOAuth(req) {
		writeError(w, "oauth providers must be created via oauth authorization flow", http.StatusBadRequest)
		return
	}

	name := req.Name
	keys, err := normalizeProviderKeys(req)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Validate provider fields (name is used as a URL identifier).
	if err := validateProviderName(name); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

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

	// Enforce unique provider names within a client config (the name is used as the identifier in URLs).
	if providerNameExists(cc.Providers, name) {
		writeError(w, "provider name already exists", http.StatusConflict)
		return
	}

	priority := 0
	if req.Priority != nil {
		priority = *req.Priority
		if priority < 1 {
			writeError(w, "priority must be >= 1", http.StatusBadRequest)
			return
		}
	}
	if priority == 0 {
		priority = nextProviderPriority(cc.Providers)
	}

	provider, err := providerFromCreateRequest(clientType, req, priority, keys)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cc.Providers = append(cc.Providers, provider)
	if !a.saveClientConfigOrWriteError(w, clientType, cfg) {
		return
	}

	logger.Info("provider %s added to %s via web interface", name, clientType)
	writeJSON(w, SuccessResponse{Message: "provider added successfully"})
}

// HandleUpdateProvider updates an existing provider
func (a *API) HandleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientType, providerName := extractClientAndProvider(r.URL.EscapedPath())
	if clientType == "" || providerName == "" {
		writeError(w, "invalid client type or provider name", http.StatusBadRequest)
		return
	}

	var req ProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	normalizeProviderRequest(&req)
	req.Overrides = normalizeProviderOverrideRequest(req.Overrides)
	if err := validateProviderOverrideRequest(clientType, req.Overrides); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Normalize/validate optional fields.
	if strings.TrimSpace(req.Name) != "" {
		if err := validateProviderName(req.Name); err != nil {
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	keys, err := normalizeProviderKeys(req)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Priority != nil {
		if *req.Priority < 1 {
			writeError(w, "priority must be >= 1", http.StatusBadRequest)
			return
		}
	}

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

	current := providerByName(cc.Providers, providerName)
	if current == nil {
		writeError(w, "provider not found", http.StatusNotFound)
		return
	}
	if current.UsesOAuth() {
		if !isOAuthEnabledOnlyUpdate(req) {
			writeError(w, "oauth providers cannot be edited; reauthorize or delete the account", http.StatusBadRequest)
			return
		}
	} else if providerRequestUsesOAuth(req) {
		writeError(w, "existing providers cannot be converted to oauth; reauthorize instead", http.StatusBadRequest)
		return
	}

	// Reject renames that collide with another provider in the same client config.
	if req.Name != "" && req.Name != providerName {
		if providerNameExists(cc.Providers, req.Name) {
			writeError(w, "provider name already exists", http.StatusConflict)
			return
		}
	}

	updated, err := updateProviderInList(clientType, cc.Providers, providerName, req, keys)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if updated {
		if !a.saveClientConfigOrWriteError(w, clientType, cfg) {
			return
		}
		if newName := strings.TrimSpace(req.Name); newName != "" && newName != providerName {
			if err := a.renameProviderUsage(clientType, providerName, newName); err != nil {
				logger.Warn("failed to rename provider usage %s/%s -> %s: %v", clientType, providerName, newName, err)
			}
		}
	}

	if !updated {
		writeError(w, "provider not found", http.StatusNotFound)
		return
	}

	logger.Info("provider %s updated in %s via web interface", providerName, clientType)
	writeJSON(w, SuccessResponse{Message: "provider updated successfully"})
}

// HandleDeleteProvider deletes a provider
func (a *API) HandleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientType, providerName := extractClientAndProvider(r.URL.EscapedPath())
	if clientType == "" || providerName == "" {
		writeError(w, "invalid client type or provider name", http.StatusBadRequest)
		return
	}

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

	current := providerByName(cc.Providers, providerName)
	if current == nil {
		writeError(w, "provider not found", http.StatusNotFound)
		return
	}
	oauthProvider := current.NormalizedOAuthProvider()
	oauthRef := current.NormalizedOAuthRef()
	deletesOAuthCredential := current.UsesOAuth() && oauthProvider != "" && oauthRef != ""

	var deleted bool
	cc.Providers, deleted = deleteProviderFromList(cc.Providers, providerName)
	if deleted {
		normalizeClientConfigAfterProviderDeletion(cc, map[string]struct{}{
			providerName: {},
		})
		restoreOAuth := func() error { return nil }
		if deletesOAuthCredential && len(collectLinkedOAuthProviders(cfg, oauthProvider, oauthRef)) == 0 {
			var err error
			restoreOAuth, err = a.oauth.Store().DeleteWithRollback(oauthProvider, oauthRef)
			if err != nil {
				writeAPIError(w, newAPIError(http.StatusInternalServerError, fmt.Sprintf("failed to remove oauth credential: %v", err), err))
				return
			}
		}
		if !a.saveClientConfigOrWriteError(w, clientType, cfg) {
			if err := restoreOAuth(); err != nil {
				logger.Warn("failed to restore oauth credential %s/%s after provider delete rollback: %v", oauthProvider, oauthRef, err)
			}
			return
		}
		if err := a.deleteProviderUsage(clientType, providerName); err != nil {
			logger.Warn("failed to delete provider usage %s/%s: %v", clientType, providerName, err)
		}
	}

	if !deleted {
		writeError(w, "provider not found", http.StatusNotFound)
		return
	}

	logger.Info("provider %s deleted from %s via web interface", providerName, clientType)
	writeJSON(w, SuccessResponse{Message: "provider deleted successfully"})
}

// HandleReorderProviders reorders providers based on the provided list
func (a *API) HandleReorderProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientType := extractClientType(r.URL.EscapedPath())
	if clientType == "" {
		writeError(w, "invalid client type", http.StatusBadRequest)
		return
	}

	var req ReorderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

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

	reordered, err := reorderProviders(cc.Providers, req.Providers)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	cc.Providers = reordered
	if !a.saveClientConfigOrWriteError(w, clientType, cfg) {
		return
	}

	logger.Info("providers reordered for %s via web interface", clientType)
	writeJSON(w, SuccessResponse{Message: "providers reordered successfully"})
}

// HandleGetStatus returns the current system status
func (a *API) HandleGetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var cfg *config.Config
	if a.runtime != nil {
		cfg = a.runtime.ConfigSnapshot()
	}
	if cfg == nil {
		cfg = a.loadConfigOrWriteError(w)
		if cfg == nil {
			return
		}
	}

	var snap proxy.RouterRuntimeSnapshot
	if a.runtime != nil {
		snap = a.runtime.RuntimeSnapshot()
	}

	status := StatusResponse{
		Version:   a.version,
		Uptime:    time.Since(startTime).String(),
		ConfigDir: a.configDir,
		Clients:   make(map[string]ClientStatus),
	}

	status.Clients["claude"] = buildClientStatus(cfg.Claude, cfg.Claude.Providers, snap.Clients[proxy.ClientClaude])
	status.Clients["openai"] = buildClientStatus(cfg.OpenAI, cfg.OpenAI.Providers, snap.Clients[proxy.ClientOpenAI])
	status.Clients["gemini"] = buildClientStatus(cfg.Gemini, cfg.Gemini.Providers, snap.Clients[proxy.ClientGemini])

	writeJSON(w, status)
}

func (a *API) HandleListIntegrations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := a.runtimeConfigOrLoad(w)
	if cfg == nil {
		return
	}

	resp := make([]IntegrationResponse, 0, len(integration.SupportedProducts()))
	for _, product := range integration.SupportedProducts() {
		status, err := a.integrations.Status(product, cfg)
		if err != nil {
			writeError(w, fmt.Sprintf("failed to inspect %s: %v", product, err), http.StatusInternalServerError)
			return
		}
		preview, err := a.integrations.Preview(product, cfg)
		if err != nil {
			writeError(w, fmt.Sprintf("failed to preview %s: %v", product, err), http.StatusInternalServerError)
			return
		}
		resp = append(resp, toIntegrationResponse(product, status, preview))
	}
	writeJSON(w, resp)
}

func (a *API) HandleIntegrationAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	product, action := extractIntegrationAction(r.URL.EscapedPath())
	if product == "" || action == "" {
		writeError(w, "invalid integration path", http.StatusBadRequest)
		return
	}

	cfg := a.runtimeConfigOrLoad(w)
	if cfg == nil {
		return
	}

	var (
		result integration.Result
		err    error
	)
	switch action {
	case "apply":
		result, err = a.integrations.Apply(product, cfg)
	case "rollback":
		result, err = a.integrations.Rollback(product, cfg)
	default:
		writeError(w, "invalid integration action", http.StatusBadRequest)
		return
	}
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	preview, err := a.integrations.Preview(result.Product, cfg)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, IntegrationActionResponse{
		Message: result.Message,
		Product: string(result.Product),
		Status:  toIntegrationResponse(result.Product, result.Status, preview),
	})
}

// Helper functions

func (a *API) runtimeConfigOrLoad(w http.ResponseWriter) *config.Config {
	if a.runtime != nil {
		if cfg := a.runtime.ConfigSnapshot(); cfg != nil {
			return cfg
		}
	}
	return a.loadConfigOrWriteError(w)
}

func (a *API) providerUsageSnapshots(clientType string) map[string]telemetry.ProviderUsage {
	if a == nil {
		return nil
	}
	if a.runtime != nil {
		return a.runtime.ProviderUsageSnapshots(clientType)
	}
	if a.telemetry != nil {
		return a.telemetry.ProviderSnapshots(clientType)
	}
	return nil
}

func (a *API) renameProviderUsage(clientType string, from string, to string) error {
	if a == nil {
		return nil
	}
	if a.runtime != nil {
		return a.runtime.RenameProviderUsage(clientType, from, to)
	}
	if a.telemetry != nil {
		return a.telemetry.RenameProvider(clientType, from, to)
	}
	return nil
}

func (a *API) deleteProviderUsage(clientType string, provider string) error {
	if a == nil {
		return nil
	}
	if a.runtime != nil {
		return a.runtime.DeleteProviderUsage(clientType, provider)
	}
	if a.telemetry != nil {
		return a.telemetry.DeleteProvider(clientType, provider)
	}
	return nil
}

func buildClientStatus(cc config.ClientConfig, providers []config.Provider, rt proxy.ClientRuntimeSnapshot) ClientStatus {
	enabled := config.GetEnabledProviders(cc)
	enabledNames := make([]string, 0, len(enabled))
	for _, p := range enabled {
		enabledNames = append(enabledNames, p.Name)
	}

	mode := strings.TrimSpace(string(cc.Mode))
	if mode == "" {
		mode = string(config.ClientModeAuto)
	}
	pin := strings.TrimSpace(cc.PinnedProvider)

	current := strings.TrimSpace(rt.CurrentProvider)
	if mode == string(config.ClientModeManual) && pin != "" {
		// Manual mode is intended to be "sticky" to the pinned provider. Use the
		// config value so UI updates immediately after pinning (before the runtime
		// reload watcher swaps proxies).
		current = pin
	} else if current == "" {
		current = getFirstEnabledProvider(enabled)
	}

	currentProviders := make(map[string]string, len(rt.CurrentProviders)+1)
	for scope, provider := range rt.CurrentProviders {
		scope = strings.TrimSpace(scope)
		provider = strings.TrimSpace(provider)
		if scope == "" || provider == "" {
			continue
		}
		currentProviders[scope] = provider
	}
	if current != "" {
		currentProviders["default"] = current
	}

	// Map runtime per-provider info by name (runtime only contains enabled providers).
	rtByName := make(map[string]proxy.ProviderRuntimeSnapshot, len(rt.Providers))
	for _, p := range rt.Providers {
		rtByName[p.Name] = p
	}

	outProviders := make([]ProviderStatus, 0, len(providers))
	now := time.Now()
	for _, p := range providers {
		enabled := p.IsEnabled()
		ps := ProviderStatus{
			Name:              p.Name,
			Priority:          p.Priority,
			Enabled:           enabled,
			KeyCount:          p.KeyCount(),
			AvailableKeyCount: p.KeyCount(),
		}
		if !enabled {
			ps.SkipReason = "disabled"
			view := proxy.DescribeProviderAvailability(p.Name, enabled, proxy.ProviderRuntimeSnapshot{Name: p.Name})
			ps.State = view.State
			ps.Label = view.Label
			ps.Detail = view.Detail
			outProviders = append(outProviders, ps)
			continue
		}
		if rtSnap, ok := rtByName[p.Name]; ok {
			if rtSnap.KeyCount > 0 {
				ps.KeyCount = rtSnap.KeyCount
			}
			ps.AvailableKeyCount = rtSnap.AvailableKeyCount
			if ps.SkipReason == "" && rtSnap.KeyCount > 0 && rtSnap.AvailableKeyCount == 0 {
				ps.SkipReason = "keys_exhausted"
			}
			if !rtSnap.DeactivatedUntil.IsZero() && now.Before(rtSnap.DeactivatedUntil) {
				ps.SkipReason = "deactivated"
				ps.DeactivatedReason = rtSnap.DeactivatedReason
				ps.DeactivatedMessage = rtSnap.DeactivatedMessage
				ps.DeactivatedIn = time.Until(rtSnap.DeactivatedUntil).Truncate(time.Second).String()
			}
			ps.CircuitState = rtSnap.CircuitState
			if ps.SkipReason == "" && rtSnap.CircuitState == "open" && rtSnap.CircuitOpenIn > 0 {
				ps.SkipReason = "circuit_open"
				ps.CircuitOpenIn = rtSnap.CircuitOpenIn.Truncate(time.Second).String()
			}
			view := proxy.DescribeProviderAvailability(p.Name, enabled, rtSnap)
			ps.State = view.State
			ps.Label = view.Label
			ps.Detail = view.Detail
		} else {
			view := proxy.DescribeProviderAvailability(p.Name, enabled, proxy.ProviderRuntimeSnapshot{Name: p.Name})
			ps.State = view.State
			ps.Label = view.Label
			ps.Detail = view.Detail
		}
		outProviders = append(outProviders, ps)
	}

	var lastSwitch *ProviderSwitchStatus
	if rt.LastSwitch != nil && !rt.LastSwitch.At.IsZero() {
		view := proxy.DescribeProviderSwitch(rt.LastSwitch.From, rt.LastSwitch.To, rt.LastSwitch.Reason, rt.LastSwitch.Status)
		lastSwitch = &ProviderSwitchStatus{
			At:     rt.LastSwitch.At.Format(time.RFC3339),
			From:   rt.LastSwitch.From,
			To:     rt.LastSwitch.To,
			Reason: rt.LastSwitch.Reason,
			Status: rt.LastSwitch.Status,
			Label:  view.Label,
			Detail: view.Detail,
		}
	}
	var lastRequest *RequestOutcomeStatus
	if rt.LastRequest != nil && !rt.LastRequest.At.IsZero() {
		view := proxy.DescribeRequestOutcome(*rt.LastRequest)
		lastRequest = &RequestOutcomeStatus{
			At:         rt.LastRequest.At.Format(time.RFC3339),
			Provider:   rt.LastRequest.Provider,
			Status:     rt.LastRequest.Status,
			Delivery:   rt.LastRequest.Delivery,
			Protocol:   rt.LastRequest.Protocol,
			Capability: userVisibleCapability(rt.LastRequest.Capability),
			Cause:      rt.LastRequest.Cause,
			Bytes:      rt.LastRequest.Bytes,
			Result:     view.Result,
			Label:      view.Label,
			Detail:     view.Detail,
		}
	}

	return ClientStatus{
		Mode:           mode,
		PinnedProvider: pin,

		ProviderCount:    len(providers),
		EnabledProviders: enabledNames,
		CurrentProvider:  current,
		CurrentProviders: currentProviders,

		LastSwitch:  lastSwitch,
		LastRequest: lastRequest,
		Providers:   outProviders,
	}
}

func extractIntegrationAction(path string) (integration.ProductID, string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "integrations" {
		return "", ""
	}
	product := integration.ProductID(strings.TrimSpace(parts[2]))
	switch product {
	case integration.ProductClaudeCode, integration.ProductCodexCLI, integration.ProductOpenCode, integration.ProductGeminiCLI, integration.ProductContinue, integration.ProductAider, integration.ProductGoose:
	default:
		return "", strings.TrimSpace(parts[3])
	}
	return product, strings.TrimSpace(parts[3])
}

func userVisibleCapability(capability string) string {
	switch capability {
	case string(proxy.CapabilityClaudeCountTokens), string(proxy.CapabilityGeminiCountTokens):
		return ""
	default:
		return capability
	}
}

func extractClientType(path string) string {
	// Extract canonical client type from path like /api/providers/claude
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "providers" {
		if clientType, ok := config.CanonicalClientType(parts[2]); ok {
			return clientType
		}
	}
	return ""
}

func extractClientTypeFromClientConfigPath(path string) string {
	// Extract canonical client type from path like /api/client-config/claude
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "client-config" {
		if clientType, ok := config.CanonicalClientType(parts[2]); ok {
			return clientType
		}
	}
	return ""
}

func getClientConfigRef(cfg *config.Config, clientType string) (*config.ClientConfig, error) {
	clientType, _ = config.CanonicalClientType(clientType)
	switch clientType {
	case "claude":
		return &cfg.Claude, nil
	case "openai":
		return &cfg.OpenAI, nil
	case "gemini":
		return &cfg.Gemini, nil
	default:
		return nil, fmt.Errorf("unknown client type: %q", clientType)
	}
}

func providerNameExists(providers []config.Provider, name string) bool {
	for _, p := range providers {
		if p.Name == name {
			return true
		}
	}
	return false
}

func providerByName(providers []config.Provider, name string) *config.Provider {
	for i := range providers {
		if providers[i].Name == name {
			return &providers[i]
		}
	}
	return nil
}

type linkedOAuthProviderRef struct {
	ClientType   string
	ProviderName string
}

func collectLinkedOAuthProviders(cfg *config.Config, provider config.OAuthProvider, ref string) []linkedOAuthProviderRef {
	out := make([]linkedOAuthProviderRef, 0)
	if cfg == nil {
		return out
	}
	appendClient := func(clientType string, providers []config.Provider) {
		for _, p := range providers {
			if p.UsesOAuth() &&
				p.NormalizedOAuthProvider() == provider &&
				p.NormalizedOAuthRef() == ref {
				out = append(out, linkedOAuthProviderRef{
					ClientType:   clientType,
					ProviderName: p.Name,
				})
			}
		}
	}
	appendClient("claude", cfg.Claude.Providers)
	appendClient("openai", cfg.OpenAI.Providers)
	appendClient("gemini", cfg.Gemini.Providers)
	return out
}

func normalizeClientConfigAfterProviderDeletion(cc *config.ClientConfig, removedNames map[string]struct{}) {
	if cc == nil {
		return
	}
	pin := strings.TrimSpace(cc.PinnedProvider)
	if pin == "" {
		return
	}
	if _, removed := removedNames[pin]; !removed {
		return
	}
	cc.PinnedProvider = ""
	if cc.Mode == config.ClientModeManual {
		cc.Mode = config.ClientModeAuto
	}
}

func (a *API) deleteOAuthAccountLocked(cfg *config.Config, provider config.OAuthProvider, ref string) ([]linkedOAuthProviderRef, error) {
	if a == nil || a.oauth == nil {
		return nil, fmt.Errorf("oauth service is unavailable")
	}
	provider = config.OAuthProvider(strings.ToLower(strings.TrimSpace(string(provider))))
	ref = strings.TrimSpace(ref)
	if provider == "" || ref == "" {
		return nil, fmt.Errorf("invalid oauth account")
	}

	linked := collectLinkedOAuthProviders(cfg, provider, ref)
	touchedClients := make(map[string]*config.ClientConfig)
	removedByClient := make(map[string]map[string]struct{})
	for _, item := range linked {
		cc, err := getClientConfigRef(cfg, item.ClientType)
		if err != nil {
			return nil, err
		}
		cc.Providers, _ = deleteProviderFromList(cc.Providers, item.ProviderName)
		touchedClients[item.ClientType] = cc
		if removedByClient[item.ClientType] == nil {
			removedByClient[item.ClientType] = make(map[string]struct{})
		}
		removedByClient[item.ClientType][item.ProviderName] = struct{}{}
	}
	for clientType, cc := range touchedClients {
		normalizeClientConfigAfterProviderDeletion(cc, removedByClient[clientType])
	}
	if len(touchedClients) > 0 {
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
	}

	restores := make([]func() error, 0, len(touchedClients)+1)
	rollback := func() {
		for i := len(restores) - 1; i >= 0; i-- {
			if restores[i] != nil {
				_ = restores[i]()
			}
		}
	}

	restoreOAuth, err := a.oauth.Store().DeleteWithRollback(provider, ref)
	if err != nil {
		return nil, err
	}
	restores = append(restores, restoreOAuth)

	if len(linked) > 0 && a.telemetry != nil {
		refs := make([]telemetry.ProviderRef, 0, len(linked))
		for _, item := range linked {
			refs = append(refs, telemetry.ProviderRef{
				ClientType: item.ClientType,
				Provider:   item.ProviderName,
			})
		}
		restoreTelemetry, err := a.telemetry.DeleteProvidersWithRollback(refs)
		if err != nil {
			rollback()
			return nil, err
		}
		restores = append(restores, restoreTelemetry)
	}

	for _, clientType := range []string{"claude", "openai", "gemini"} {
		cc := touchedClients[clientType]
		if cc == nil {
			continue
		}
		restore, err := a.saveClientConfigWithRollback(clientType, *cc)
		if err != nil {
			rollback()
			return nil, err
		}
		restores = append(restores, restore)
	}

	if len(touchedClients) > 0 {
		if err := a.reloadRuntimeProviderConfigs(); err != nil {
			rollback()
			return nil, err
		}
	}
	return linked, nil
}

func nextProviderPriority(providers []config.Provider) int {
	maxPriority := 0
	for _, p := range providers {
		if p.Priority > maxPriority {
			maxPriority = p.Priority
		}
	}
	return maxPriority + 1
}

func normalizeProviderKeys(req ProviderRequest) ([]string, error) {
	apiKey := strings.TrimSpace(req.APIKey)
	keys := make([]string, 0, len(req.APIKeys)+1)
	seen := make(map[string]struct{}, len(req.APIKeys)+1)
	appendKey := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		keys = append(keys, v)
	}
	if apiKey != "" && len(req.APIKeys) > 0 {
		return nil, fmt.Errorf("api_key and api_keys cannot both be set")
	}
	if apiKey != "" {
		appendKey(apiKey)
	}
	for _, key := range req.APIKeys {
		appendKey(key)
	}
	return keys, nil
}

func normalizeProviderRequest(req *ProviderRequest) {
	if req == nil {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.AuthType = config.ProviderAuthType(strings.ToLower(strings.TrimSpace(string(req.AuthType))))
	req.OAuthProvider = config.OAuthProvider(strings.ToLower(strings.TrimSpace(string(req.OAuthProvider))))
	req.OAuthRef = strings.TrimSpace(req.OAuthRef)
}

func providerRequestUsesOAuth(req ProviderRequest) bool {
	return req.AuthType == config.ProviderAuthTypeOAuth ||
		strings.TrimSpace(string(req.OAuthProvider)) != "" ||
		strings.TrimSpace(req.OAuthRef) != ""
}

func isOAuthEnabledOnlyUpdate(req ProviderRequest) bool {
	return req.Enabled != nil &&
		req.Name == "" &&
		req.BaseURL == "" &&
		req.APIKey == "" &&
		len(req.APIKeys) == 0 &&
		req.AuthType == "" &&
		req.OAuthProvider == "" &&
		req.OAuthRef == "" &&
		req.ProxyMode == nil &&
		req.ProxyURL == nil &&
		req.Overrides == nil &&
		req.Priority == nil
}

func trimStringPtr(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	return &trimmed
}

func ptr[T any](v T) *T {
	return &v
}

type providerOverrideSupport struct {
	Model  bool
	OpenAI struct {
		ReasoningEffort bool
	}
	Claude struct {
		ThinkingBudgetTokens bool
		Effort               bool
	}
}

func providerOverrideSupportForClient(clientType string) providerOverrideSupport {
	var support providerOverrideSupport
	switch canonical, ok := config.CanonicalClientType(clientType); {
	case ok && canonical == "openai":
		support.Model = true
		support.OpenAI.ReasoningEffort = true
	case ok && canonical == "claude":
		support.Model = true
		support.Claude.ThinkingBudgetTokens = true
		support.Claude.Effort = true
	}
	return support
}

func normalizeProviderOverrideRequest(overrides *ProviderOverridesRequest) *ProviderOverridesRequest {
	if overrides == nil {
		return nil
	}

	normalized := &ProviderOverridesRequest{
		Model: trimStringPtr(overrides.Model),
	}
	if overrides.OpenAI != nil && overrides.OpenAI.ReasoningEffort != nil {
		normalized.OpenAI = &OpenAIProviderOverridesRequest{
			ReasoningEffort: trimStringPtr(overrides.OpenAI.ReasoningEffort),
		}
	}
	if overrides.Claude != nil {
		var claude ClaudeProviderOverridesRequest
		if overrides.Claude.ThinkingBudgetTokens != nil {
			claude.ThinkingBudgetTokens = ptr(*overrides.Claude.ThinkingBudgetTokens)
		}
		if overrides.Claude.Effort != nil {
			claude.Effort = trimStringPtr(overrides.Claude.Effort)
		}
		if claude.ThinkingBudgetTokens != nil || claude.Effort != nil {
			normalized.Claude = &claude
		}
	}
	if normalized.Model == nil && normalized.OpenAI == nil && normalized.Claude == nil {
		return nil
	}
	return normalized
}

func validateProviderOverrideRequest(clientType string, overrides *ProviderOverridesRequest) error {
	if overrides == nil {
		return nil
	}
	if canonical, ok := config.CanonicalClientType(clientType); ok {
		clientType = canonical
	}
	support := providerOverrideSupportForClient(clientType)
	if overrides.Model != nil && !support.Model {
		return fmt.Errorf("overrides.model is not supported for %s providers", clientType)
	}
	if overrides.OpenAI != nil && overrides.OpenAI.ReasoningEffort != nil && !support.OpenAI.ReasoningEffort {
		return fmt.Errorf("overrides.openai.reasoning_effort is not supported for %s providers", clientType)
	}
	if overrides.Claude != nil && overrides.Claude.ThinkingBudgetTokens != nil {
		if !support.Claude.ThinkingBudgetTokens {
			return fmt.Errorf("overrides.claude.thinking_budget_tokens is not supported for %s providers", clientType)
		}
		if *overrides.Claude.ThinkingBudgetTokens < 0 {
			return fmt.Errorf("thinking_budget_tokens must be >= 0")
		}
	}
	if overrides.Claude != nil && overrides.Claude.Effort != nil {
		if !support.Claude.Effort {
			return fmt.Errorf("overrides.claude.effort is not supported for %s providers", clientType)
		}
		if !isValidClaudeEffort(*overrides.Claude.Effort) {
			return fmt.Errorf("claude effort must be one of low, medium, high, max, xhigh")
		}
	}
	return nil
}

func applyProviderOverrides(provider *config.Provider, req ProviderRequest) {
	if provider == nil || req.Overrides == nil {
		return
	}
	if provider.Overrides == nil {
		provider.Overrides = &config.ProviderOverrides{}
	}
	if req.Overrides.Model != nil {
		provider.Overrides.Model = ptr(strings.TrimSpace(*req.Overrides.Model))
	}
	if req.Overrides.OpenAI != nil && req.Overrides.OpenAI.ReasoningEffort != nil {
		if provider.Overrides.OpenAI == nil {
			provider.Overrides.OpenAI = &config.OpenAIOverrides{}
		}
		provider.Overrides.OpenAI.ReasoningEffort = ptr(strings.TrimSpace(*req.Overrides.OpenAI.ReasoningEffort))
	}
	if req.Overrides.Claude != nil && req.Overrides.Claude.ThinkingBudgetTokens != nil {
		if provider.Overrides.Claude == nil {
			provider.Overrides.Claude = &config.ClaudeOverrides{}
		}
		provider.Overrides.Claude.ThinkingBudgetTokens = ptr(*req.Overrides.Claude.ThinkingBudgetTokens)
	}
	if req.Overrides.Claude != nil && req.Overrides.Claude.Effort != nil {
		if provider.Overrides.Claude == nil {
			provider.Overrides.Claude = &config.ClaudeOverrides{}
		}
		provider.Overrides.Claude.Effort = ptr(strings.TrimSpace(*req.Overrides.Claude.Effort))
	}
	provider.Overrides = config.NormalizeProviderOverrides(provider.Overrides)
}

func isValidClaudeEffort(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low", "medium", "high", "max", "xhigh":
		return true
	default:
		return false
	}
}

func providerFromCreateRequest(clientType string, req ProviderRequest, priority int, keys []string) (config.Provider, error) {
	provider := config.Provider{
		Name:          req.Name,
		BaseURL:       req.BaseURL,
		AuthType:      req.AuthType,
		OAuthProvider: req.OAuthProvider,
		OAuthRef:      req.OAuthRef,
		Priority:      priority,
		Enabled:       req.Enabled,
	}
	applyProviderOverrides(&provider, req)
	if err := config.ApplyProviderProxySettings(&provider, config.ProviderProxySettingsPatch{
		Mode: req.ProxyMode,
		URL:  req.ProxyURL,
	}, false); err != nil {
		return config.Provider{}, err
	}
	assignProviderKeys(&provider, keys)
	config.NormalizeProviderAuthSettings(&provider)
	if err := validateProviderCredentialSource(clientType, provider); err != nil {
		return config.Provider{}, err
	}
	return provider, nil
}

func mergeProviderUpdate(current config.Provider, req ProviderRequest, keys []string) (config.Provider, error) {
	provider := current
	if req.Name != "" {
		provider.Name = req.Name
	}
	if req.BaseURL != "" {
		provider.BaseURL = req.BaseURL
	}
	if req.AuthType != "" {
		provider.AuthType = req.AuthType
		switch req.AuthType {
		case config.ProviderAuthTypeAPIKey:
			provider.OAuthProvider = ""
			provider.OAuthRef = ""
		case config.ProviderAuthTypeOAuth:
			assignProviderKeys(&provider, nil)
		}
	}
	if req.OAuthProvider != "" {
		provider.OAuthProvider = req.OAuthProvider
	}
	if req.OAuthRef != "" {
		provider.OAuthRef = req.OAuthRef
	}
	if req.APIKey != "" || len(req.APIKeys) > 0 {
		assignProviderKeys(&provider, keys)
	}
	if req.Priority != nil {
		provider.Priority = *req.Priority
	}
	if req.Enabled != nil {
		provider.Enabled = req.Enabled
	}
	applyProviderOverrides(&provider, req)
	if err := config.ApplyProviderProxySettings(&provider, config.ProviderProxySettingsPatch{
		Mode: req.ProxyMode,
		URL:  req.ProxyURL,
	}, true); err != nil {
		return config.Provider{}, err
	}
	config.NormalizeProviderAuthSettings(&provider)
	return provider, nil
}

func validateProviderCredentialSource(clientType string, provider config.Provider) error {
	switch provider.NormalizedAuthType() {
	case config.ProviderAuthTypeAPIKey:
		if _, err := validateBaseURL(provider.BaseURL); err != nil {
			return err
		}
		if len(provider.NormalizedAPIKeys()) == 0 {
			return fmt.Errorf("api_key or api_keys is required")
		}
		if provider.NormalizedOAuthProvider() != "" || provider.NormalizedOAuthRef() != "" {
			return fmt.Errorf("oauth_provider and oauth_ref require auth_type=oauth")
		}
	case config.ProviderAuthTypeOAuth:
		if len(provider.NormalizedAPIKeys()) > 0 {
			return fmt.Errorf("api_key and api_keys cannot be set when auth_type=oauth")
		}
		if provider.NormalizedOAuthProvider() == "" {
			return fmt.Errorf("oauth_provider is required when auth_type=oauth")
		}
		if provider.NormalizedOAuthRef() == "" {
			return fmt.Errorf("oauth_ref is required when auth_type=oauth")
		}
		if err := validateOAuthProviderForClient(clientType, provider.NormalizedOAuthProvider()); err != nil {
			return err
		}
		if strings.TrimSpace(provider.BaseURL) != "" {
			return fmt.Errorf("base_url is not allowed when auth_type=oauth")
		}
	default:
		return fmt.Errorf("auth_type must be one of %q or %q", config.ProviderAuthTypeAPIKey, config.ProviderAuthTypeOAuth)
	}
	return nil
}

func validateOAuthProviderForClient(clientType string, oauthProvider config.OAuthProvider) error {
	if oauthpkg.ProviderSupportedForClient(oauthProvider, clientType) {
		return nil
	}
	if canonical, ok := config.CanonicalClientType(clientType); ok {
		clientType = canonical
	}
	return fmt.Errorf("oauth_provider %q is not supported for %s client", oauthProvider, clientType)
}

func assignProviderKeys(provider *config.Provider, keys []string) {
	if provider == nil {
		return
	}
	provider.APIKey = ""
	provider.APIKeys = nil
	switch len(keys) {
	case 0:
		return
	case 1:
		provider.APIKey = keys[0]
	default:
		provider.APIKeys = append([]string(nil), keys...)
	}
}

func extractClientAndProvider(path string) (string, string) {
	// Extract from path like /api/providers/claude/provider-name
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "providers" {
		clientType := parts[2]
		if canonical, ok := config.CanonicalClientType(clientType); ok {
			clientType = canonical
		}
		// net/http populates r.URL.Path in decoded form, but be defensive if callers
		// use EscapedPath/RawPath or if we receive encoded segments.
		name, err := url.PathUnescape(parts[3])
		if err != nil {
			name = parts[3]
		}
		return clientType, name
	}
	return "", ""
}

func extractClientProviderSubresource(path string) (string, string, string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 5 && parts[0] == "api" && parts[1] == "providers" {
		clientType := parts[2]
		if canonical, ok := config.CanonicalClientType(clientType); ok {
			clientType = canonical
		}
		name, err := url.PathUnescape(parts[3])
		if err != nil {
			name = parts[3]
		}
		return clientType, name, strings.TrimSpace(parts[4])
	}
	return "", "", ""
}

func updateProviderInList(clientType string, providers []config.Provider, name string, req ProviderRequest, keys []string) (bool, error) {
	for i := range providers {
		if providers[i].Name == name {
			updated, err := mergeProviderUpdate(providers[i], req, keys)
			if err != nil {
				return false, err
			}
			if err := validateProviderCredentialSource(clientType, updated); err != nil {
				return false, err
			}
			providers[i] = updated
			return true, nil
		}
	}
	return false, nil
}

func deleteProviderFromList(providers []config.Provider, name string) ([]config.Provider, bool) {
	for i, p := range providers {
		if p.Name == name {
			return append(providers[:i], providers[i+1:]...), true
		}
	}
	return providers, false
}

func reorderProviders(providers []config.Provider, order []string) ([]config.Provider, error) {
	// Create a map for quick lookup (and to detect duplicates).
	providerMap := make(map[string]config.Provider, len(providers))
	for _, p := range providers {
		if _, exists := providerMap[p.Name]; exists {
			return nil, fmt.Errorf("duplicate provider name: %s", p.Name)
		}
		providerMap[p.Name] = p
	}

	seen := make(map[string]struct{}, len(order))
	reordered := make([]config.Provider, 0, len(providers))

	// Add providers in the requested order first.
	for _, name := range order {
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("duplicate provider in reorder list: %s", name)
		}
		seen[name] = struct{}{}
		p, exists := providerMap[name]
		if !exists {
			return nil, fmt.Errorf("unknown provider in reorder list: %s", name)
		}
		reordered = append(reordered, p)
	}

	// Append any providers that were not mentioned, preserving their current order.
	for _, p := range providers {
		if _, ok := seen[p.Name]; ok {
			continue
		}
		reordered = append(reordered, p)
	}

	// Normalize priorities to a continuous range so ordering is deterministic.
	for i := range reordered {
		reordered[i].Priority = i + 1
	}

	return reordered, nil
}

func getFirstEnabledProvider(providers []config.Provider) string {
	if len(providers) > 0 {
		return providers[0].Name
	}
	return ""
}

func validateProviderName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if strings.Contains(name, "\\") {
		return fmt.Errorf("invalid provider name %q: backslash (\\\\) is not allowed", name)
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("invalid provider name %q: slash (/) is not allowed", name)
	}
	if len(name) > providerNameMaxLen {
		return fmt.Errorf("invalid provider name %q: too long (max %d)", name, providerNameMaxLen)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if i == 0 && !isAlphaNum {
			return fmt.Errorf("invalid provider name %q: must start with a letter or number (allowed: letters, numbers, '.', '_', '-')", name)
		}
		if isAlphaNum || c == '.' || c == '_' || c == '-' {
			continue
		}
		return fmt.Errorf("invalid provider name %q: allowed characters are letters, numbers, '.', '_', '-' (no spaces)", name)
	}
	return nil
}

func validateBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("base_url is required")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid base_url %q: %v", raw, err)
	}
	// If the scheme is missing, treat the value as a host and default to https.
	if parsed.Scheme == "" {
		parsed, err = url.Parse("https://" + raw)
		if err != nil {
			return "", fmt.Errorf("invalid base_url %q: %v", raw, err)
		}
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("invalid base_url %q: host is empty", raw)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		// ok
	default:
		return "", fmt.Errorf("invalid base_url %q: scheme must be http or https", raw)
	}
	if parsed.RawQuery != "" {
		return "", fmt.Errorf("invalid base_url %q: query is not allowed", raw)
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("invalid base_url %q: fragment is not allowed", raw)
	}
	return raw, nil
}

//nolint:gosec // path comes from Clipal-managed config store locations, not arbitrary user input.
func atomicWriteFile(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, ".clipal-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	success := false
	defer func() {
		_ = f.Close()
		if !success {
			_ = os.Remove(tmp)
		}
	}()

	if err := f.Chmod(perm); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	success = true
	return nil
}
