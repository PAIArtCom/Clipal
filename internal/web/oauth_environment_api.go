package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/oauth"
	"github.com/PAIArtCom/Clipal/internal/outbound"
	"github.com/PAIArtCom/Clipal/internal/proxy"
)

type consistencyCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type environmentResponse struct {
	Environment     *oauth.RequestEnvironment `json:"environment"`
	AccountID       string                    `json:"account_id,omitempty"`
	IdentitySource  string                    `json:"identity_source"`
	AccountIdentity string                    `json:"account_identity"`
	UpstreamRegion  map[string]string         `json:"upstream_region,omitempty"`
}
type consistencyReport struct {
	Environment       *oauth.RequestEnvironment `json:"environment"`
	CheckedAt         time.Time                 `json:"checked_at"`
	ProxyHint         string                    `json:"proxy_hint,omitempty"`
	BootstrapDNS      string                    `json:"bootstrap_dns"`
	ExitIP            string                    `json:"exit_ip,omitempty"`
	ExitCountry       string                    `json:"exit_country,omitempty"`
	ExitTimezone      string                    `json:"exit_timezone,omitempty"`
	ObservationSource string                    `json:"observation_source,omitempty"`
	TargetStatus      int                       `json:"target_http_status,omitempty"`
	Routing           outbound.RoutingInfo      `json:"routing"`
	DoHResolver       string                    `json:"doh_reference_resolver,omitempty"`
	DoHAddresses      int                       `json:"doh_reference_address_count,omitempty"`
	Checks            []consistencyCheck        `json:"checks"`
}

// Both account management endpoints reject unknown fields and trailing JSON.
// Diagnostics alone allow an empty body, which selects the default checks.
func decodeAccountEnvironmentJSON(r *http.Request, value any, allowEmpty bool) error {
	if r.Body == nil {
		if allowEmpty {
			return nil
		}
		return io.EOF
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		if err == io.EOF && allowEmpty {
			return nil
		}
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON")
		}
		return err
	}
	return nil
}

func (a *API) environmentProvider(w http.ResponseWriter, r *http.Request) (config.Provider, *config.Config, *oauth.Credential, bool) {
	client, name, _ := extractClientProviderSubresource(r.URL.EscapedPath())
	cfg := a.loadConfigOrWriteError(w)
	if cfg == nil {
		return config.Provider{}, nil, nil, false
	}
	cc, err := getClientConfigRef(cfg, client)
	if err != nil {
		writeError(w, "invalid client type", http.StatusBadRequest)
		return config.Provider{}, nil, nil, false
	}
	p := providerByName(cc.Providers, name)
	if p == nil {
		writeError(w, "provider not found", http.StatusNotFound)
		return config.Provider{}, nil, nil, false
	}
	if !p.UsesOAuth() {
		writeError(w, "provider does not use OAuth", http.StatusBadRequest)
		return config.Provider{}, nil, nil, false
	}
	cred, err := a.oauth.Load(p.NormalizedOAuthProvider(), p.NormalizedOAuthRef())
	if err != nil {
		writeError(w, "OAuth credential is unavailable", http.StatusConflict)
		return config.Provider{}, nil, nil, false
	}
	if expected := p.NormalizedOAuthIdentity(); expected != "" && expected != oauth.AccountIdentityKey(cred) {
		writeError(w, "OAuth account identity does not match provider", http.StatusConflict)
		return config.Provider{}, nil, nil, false
	}
	return *p, cfg, cred, true
}

func (a *API) HandleProviderEnvironment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	_, _, cred, ok := a.environmentProvider(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPut {
		var update struct {
			Country  string `json:"country"`
			Timezone string `json:"timezone"`
			Identity string `json:"account_identity"`
		}
		if err := decodeAccountEnvironmentJSON(r, &update, false); err != nil {
			writeError(w, "invalid request environment", http.StatusBadRequest)
			return
		}
		if update.Identity == "" || update.Identity != oauth.AccountEnvironmentIdentity(cred) {
			writeError(w, "OAuth account changed; reload account environment", http.StatusConflict)
			return
		}
		options := oauth.RegionOptions{Country: update.Country, Timezone: update.Timezone, Source: "user"}
		if _, err := oauth.NewAccountEnvironment(cred, &options); err != nil {
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}
		updated, err := a.oauth.Store().ConfigureRegion(cred, options)
		if err != nil {
			writeError(w, "account environment could not be saved", http.StatusConflict)
			return
		}
		cred = updated
	}
	source := "account_id"
	if strings.TrimSpace(cred.AccountID) == "" {
		source = "account_identity_fallback"
	}
	upstream := map[string]string{}
	for _, key := range []string{"country_code", "country", "timezone"} {
		if value := cred.Metadata[key]; value != "" {
			upstream[key] = value
		}
	}
	writeJSON(w, environmentResponse{Environment: cred.Environment, AccountID: cred.AccountID, AccountIdentity: oauth.AccountEnvironmentIdentity(cred), IdentitySource: source, UpstreamRegion: upstream})
}

var diagnosticTarget = oauth.EnvironmentTarget

var diagnosticExitURLs = []string{"https://ipwho.is/", "https://ipapi.co/json/"}

func (a *API) HandleProviderDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var options struct {
		CheckDoH bool `json:"check_doh"`
	}
	if err := decodeAccountEnvironmentJSON(r, &options, true); err != nil {
		writeError(w, "invalid diagnostic options", http.StatusBadRequest)
		return
	}
	a.configMu.Lock()
	p, cfg, cred, ok := a.environmentProvider(w, r)
	a.configMu.Unlock()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	report := consistencyReport{Environment: cred.Environment.Clone(), CheckedAt: time.Now().UTC()}
	check := func(name, status, detail string) {
		report.Checks = append(report.Checks, consistencyCheck{Name: name, Status: status, Detail: detail})
	}
	if cred.Environment.DeviceID == oauth.AccountDeviceID(cred) {
		check("account_identity", "ok", "Persistent device ID matches this OAuth account.")
	}
	if cred.AccountID == "" {
		check("identity_source", "pending", "Account ID is unavailable; device ID uses the stable account identity fallback.")
	}
	if cred.Environment.NeedsConfirmation || !cred.Environment.Complete() {
		check("region", "pending", "Request region requires user confirmation; it is not inferred from network exits.")
	} else {
		check("region", "ok", "Request preferences are stored on the account; they do not prove registration or billing country.")
	}
	base := proxy.NewOAuthHTTPClientForProvider(p, cfg.Global)
	base.Timeout = 10 * time.Second
	base.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	target := diagnosticTarget(cred.Provider)
	resolvedProxy, routeErr := outbound.ProxyFor(base, target)
	if routeErr != nil {
		check("network_exit", "mismatch", "Target proxy policy failed.")
		writeJSON(w, report)
		return
	}

	if resolvedProxy != nil {
		report.ProxyHint = proxyURLHint(resolvedProxy.String())
		report.BootstrapDNS = "DoH is not used for proxy requests"
		check("dns", "ok", "Target hostnames are delegated to the standard proxy; public DoH is not required.")
		check("target_dns", "pending", "The proxy's actual DNS backend and target addresses are unverified.")
	} else {
		report.BootstrapDNS = "fixed DoH bootstrap IP (no explicit proxy)"
	}
	if resolvedProxy == nil && cred.Environment.DNS == "system" {
		report.BootstrapDNS = "DoH is not used"
	}
	if resolvedProxy != nil && net.ParseIP(resolvedProxy.Hostname()) == nil {
		report.BootstrapDNS += "; system DNS resolves the first-hop proxy hostname"
	}
	client, err := outbound.WithDNS(base, cred.Environment.DNS)
	if err != nil {
		check("dns", "mismatch", "Account DNS transport could not be created.")
		writeJSON(w, report)
		return
	}
	defer client.CloseIdleConnections()
	report.Routing = outbound.RouteInfo(client, target)
	if resolvedProxy == nil {
		if cred.Environment.DNS == "system" {
			check("dns", "pending", "DNS follows the system or proxy resolver; Google/Cloudflare is not enforced.")
			check("target_dns", "pending", "Target resolution follows the system resolver; system/TUN routing is not observed.")
		} else if _, err := outbound.Lookup(ctx, client, target); err != nil {
			check("dns", "mismatch", "Account DoH lookup failed; no system DNS fallback was used.")
		} else {
			check("dns", "ok", outbound.ActiveResolver(client, target)+" DoH succeeded on the selected entry.")
			check("target_dns", "ok", "Target connections use DoH returned addresses with original Host and TLS SNI.")
		}
	}
	// Reachability is tested without credentials; any HTTP response verifies the
	// connection, not OAuth authentication, country or routing to other targets.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if resp, err := client.Do(req); err == nil {
		report.TargetStatus = resp.StatusCode
		_ = resp.Body.Close()
		check("connection", "ok", "Target HTTP connection succeeded without account credentials; authentication is unverified.")
	} else {
		check("connection", "pending", "Target connection did not complete; no fallback route was used.")
	}
	if exit, err := outbound.ProbeExit(ctx, client, target, diagnosticExitURLs...); err == nil {
		report.ExitIP, report.ExitCountry, report.ExitTimezone = exit.IP, exit.Country, exit.Timezone
		report.ObservationSource = exit.Source
		check("reference_exit", "ok", "Reference service observation only; it does not prove the target exit or account country.")
	} else {
		check("reference_exit", "pending", "No reference service observation was available on the selected entry.")
	}
	check("network_exit", "pending", "The target exit is unverified; matching proxy entries do not prove matching egress.")
	check("upstream_auth", "pending", "No token refresh or paid model request was sent by this diagnostic.")
	if resolvedProxy != nil && options.CheckDoH {
		dns := cred.Environment.DNS
		if dns == "" || dns == "system" {
			dns = "auto"
		}
		// Reuse the resolver behind Lookup, without account headers or tokens.
		// Run after connection checks so failures cannot consume their budget.
		probeCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		probe, err := outbound.WithDNS(base, dns)
		if err == nil {
			defer probe.CloseIdleConnections()
			var addresses []net.IP
			addresses, err = outbound.Lookup(probeCtx, probe, target)
			if err == nil {
				report.DoHResolver = outbound.ActiveResolver(probe, target)
				report.DoHAddresses = len(addresses)
				check("doh_reference", "ok", report.DoHResolver+" DoH reference lookup succeeded; it does not verify proxy DNS or target egress.")
			}
		}
		if err != nil {
			check("doh_reference", "pending", "Optional DoH reference lookup failed; proxy requests do not depend on it.")
		}
	}
	writeJSON(w, report)
}
