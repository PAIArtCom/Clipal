package web

import (
	"embed"
	"mime"
	"net"
	"net/http"
	"path"
	"strings"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/proxy"
	"github.com/lansespirit/Clipal/internal/transfer"
)

//go:embed static/*
var staticFiles embed.FS

const maxAPIRequestBytes = 1 << 20 // 1 MiB (WebUI requests are small)
const maxOAuthImportRequestBytes = 16 << 20
const maxDataImportRequestBytes = transfer.MaxJSONImportRequestBytes

// Handler manages HTTP routes for the web management interface
type Handler struct {
	api *API
}

// NewHandler creates a new web handler
func NewHandler(configDir, version string, runtime *proxy.Router) *Handler {
	return &Handler{
		api: NewAPI(configDir, version, runtime),
	}
}

func isLoopbackRemote(remoteAddr string) bool {
	host := strings.TrimSpace(remoteAddr)
	if host == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	} else {
		// Be defensive: RemoteAddr can be "IP" without port or "[::1]".
		host = strings.Trim(host, "[]")
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLocalhostHost(hostport string) bool {
	host := strings.TrimSpace(hostport)
	if host == "" {
		// Be permissive for HTTP/1.0 style requests or tests with an empty Host.
		return true
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isStateChangingMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func isOAuthCLIProxyAPIImportPath(path string) bool {
	return strings.TrimSuffix(strings.TrimSpace(path), "/") == "/api/oauth/import/cli-proxy-api"
}

func isDataImportPath(path string) bool {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	return path == "/api/data/import/preview" || path == "/api/data/import/apply"
}

func apiRequestBodyLimit(path string) int64 {
	if isOAuthCLIProxyAPIImportPath(path) {
		return maxOAuthImportRequestBytes
	}
	if isDataImportPath(path) {
		return maxDataImportRequestBytes
	}
	return maxAPIRequestBytes
}

func isAllowedAPIRequestMediaType(path string, mediaType string) bool {
	if isOAuthCLIProxyAPIImportPath(path) {
		return mediaType == "multipart/form-data"
	}
	return mediaType == "application/json"
}

func (h *Handler) localOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The management interface is designed for local use only (no auth).
		// Enforce loopback access even if the proxy listens on 0.0.0.0/::.
		//
		// Additionally enforce a localhost Host header to mitigate DNS rebinding
		// (attackers can resolve a public domain to 127.0.0.1 after the page loads).
		if !h.remoteWebUIEnabled() && (!isLoopbackRemote(r.RemoteAddr) || !isLocalhostHost(r.Host)) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeError(w, "forbidden: management interface is localhost-only", http.StatusForbidden)
			} else {
				http.Error(w, "forbidden", http.StatusForbidden)
			}
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")

		// Put a hard cap on WebUI request bodies to avoid unbounded memory usage.
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, apiRequestBodyLimit(r.URL.EscapedPath()))
		}

		// Basic CSRF hardening for state-changing API calls: require an explicit
		// header that the bundled UI will set. Cross-site requests can't set it
		// without a CORS preflight (which we do not allow).
		if strings.HasPrefix(r.URL.Path, "/api/") && isStateChangingMethod(r.Method) {
			if r.Header.Get("X-Clipal-UI") != "1" {
				writeError(w, "forbidden: missing X-Clipal-UI header", http.StatusForbidden)
				return
			}

			// If a state-changing request carries a body, require JSON.
			// This prevents accidental form-encoded submissions and keeps semantics clear.
			if r.Method != http.MethodDelete && (r.ContentLength != 0 || r.Header.Get("Content-Type") != "") {
				mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || !isAllowedAPIRequestMediaType(r.URL.EscapedPath(), mt) {
					expected := "application/json"
					if isOAuthCLIProxyAPIImportPath(r.URL.EscapedPath()) {
						expected = "multipart/form-data"
					}
					writeError(w, "unsupported media type: expected "+expected, http.StatusUnsupportedMediaType)
					return
				}
			}
		}

		next(w, r)
	}
}

func (h *Handler) remoteWebUIEnabled() bool {
	if h.api.runtime != nil {
		if cfg := h.api.runtime.ConfigSnapshot(); cfg != nil {
			return cfg.Global.AllowRemoteWebUI
		}
	}
	cfg, err := config.Load(h.api.configDir)
	return err == nil && cfg.Global.AllowRemoteWebUI
}

// RegisterRoutes registers all web management routes to the provided mux
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Static files (UI)
	mux.HandleFunc("/", h.localOnly(h.serveIndex))
	mux.HandleFunc("/static/", h.localOnly(h.serveStatic))

	// API routes
	mux.HandleFunc("/api/config/global", h.localOnly(h.api.HandleGetGlobalConfig))
	mux.HandleFunc("/api/config/global/update", h.localOnly(h.api.HandleUpdateGlobalConfig))
	mux.HandleFunc("/api/data/export", h.localOnly(h.api.HandleDataExport))
	mux.HandleFunc("/api/data/import/preview", h.localOnly(h.api.HandleDataImportPreview))
	mux.HandleFunc("/api/data/import/apply", h.localOnly(h.api.HandleDataImportApply))
	mux.HandleFunc("/api/integrations", h.localOnly(h.api.HandleListIntegrations))
	mux.HandleFunc("/api/integrations/", h.localOnly(h.routeIntegrations))

	mux.HandleFunc("/api/client-config/", h.localOnly(h.routeClientConfig))
	mux.HandleFunc("/api/providers/", h.localOnly(h.routeProviders))
	mux.HandleFunc("/api/oauth/", h.localOnly(h.routeOAuth))
	mux.HandleFunc("/api/status", h.localOnly(h.api.HandleGetStatus))

	// Service management (OS background service for clipal)
	mux.HandleFunc("/api/service/status", h.localOnly(h.api.HandleServiceStatus))
	mux.HandleFunc("/api/service/", h.localOnly(h.routeService))
}

func (h *Handler) routeClientConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.api.HandleGetClientConfig(w, r)
	case http.MethodPut:
		h.api.HandleUpdateClientConfig(w, r)
	default:
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) routeIntegrations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.api.HandleIntegrationAction(w, r)
	default:
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// serveIndex serves the main management interface HTML
func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}

	data, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// serveStatic serves static assets (CSS, JS)
func (h *Handler) serveStatic(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/static/")
	if p == "" || p == "/" {
		http.NotFound(w, r)
		return
	}

	// Prevent path traversal attempts; embed.FS is already strict, but keep this
	// explicit to avoid surprises if the backing fs ever changes.
	p = path.Clean("/" + p)
	p = strings.TrimPrefix(p, "/")
	if p == "" || strings.HasPrefix(p, "..") {
		http.NotFound(w, r)
		return
	}

	data, err := staticFiles.ReadFile("static/" + p)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "no-store")

	_, _ = w.Write(data)
}

// routeProviders routes provider-related requests to appropriate handlers
func (h *Handler) routeProviders(w http.ResponseWriter, r *http.Request) {
	path := r.URL.EscapedPath()

	// Check if this is a reorder request
	if strings.HasSuffix(path, "/_reorder") {
		h.api.HandleReorderProviders(w, r)
		return
	}

	clientType, providerName, subresource := extractClientProviderSubresource(path)
	if clientType != "" && providerName != "" && subresource == "oauth-metadata" {
		switch r.Method {
		case http.MethodGet:
			h.api.HandleGetProviderOAuthMetadata(w, r)
		default:
			writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// Extract client type and provider name
	clientType = extractClientType(path)
	_, providerName = extractClientAndProvider(path)

	if providerName != "" {
		// Operations on specific provider
		switch r.Method {
		case http.MethodPut:
			h.api.HandleUpdateProvider(w, r)
		case http.MethodDelete:
			h.api.HandleDeleteProvider(w, r)
		default:
			writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	} else if clientType != "" {
		// Operations on provider list
		switch r.Method {
		case http.MethodGet:
			h.api.HandleGetProviders(w, r)
		case http.MethodPost:
			h.api.HandleAddProvider(w, r)
		default:
			writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	} else {
		writeError(w, "invalid request path", http.StatusBadRequest)
	}
}

func (h *Handler) routeOAuth(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.EscapedPath(), "/")
	switch {
	case path == "/api/oauth/providers":
		h.api.HandleListOAuthProviders(w, r)
	case path == "/api/oauth/providers/start":
		h.api.HandleStartOAuthProvider(w, r)
	case path == "/api/oauth/import/cli-proxy-api":
		h.api.HandleImportCLIProxyAPICredentials(w, r)
	case strings.HasPrefix(path, "/api/oauth/sessions/"):
		h.api.HandleGetOAuthSession(w, r)
	case strings.HasPrefix(path, "/api/oauth/accounts/"):
		if r.Method == http.MethodGet {
			h.api.HandleListOAuthAccounts(w, r)
			return
		}
		if r.Method == http.MethodDelete {
			h.api.HandleDeleteOAuthAccount(w, r)
			return
		}
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
	default:
		writeError(w, "invalid request path", http.StatusBadRequest)
	}
}

func (h *Handler) routeService(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.EscapedPath(), "/")

	// Supported:
	//   GET  /api/service/status  (handled separately)
	//   POST /api/service/<install|uninstall|start|stop|restart>
	action := strings.TrimPrefix(path, "/api/service/")
	if action == "" || action == "status" {
		writeError(w, "invalid request path", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPost:
		h.api.HandleServiceAction(w, r, action)
	default:
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
