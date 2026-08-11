package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/service"
)

type providerStatus struct {
	Client  string `json:"client"`
	Enabled int    `json:"enabled"`
	Active  string `json:"active,omitempty"`
}

type healthStatus struct {
	OK         bool   `json:"ok"`
	URL        string `json:"url,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Error      string `json:"error,omitempty"`
}

type statusReport struct {
	OK      bool   `json:"ok"`
	Summary string `json:"summary"`

	Version string `json:"version"`
	Commit  string `json:"commit"`
	Built   string `json:"built"`

	ConfigDir string `json:"config_dir"`
	Listen    string `json:"listen"`
	Port      int    `json:"port"`
	WebUI     string `json:"web_ui"`

	Health    healthStatus     `json:"health"`
	Providers []providerStatus `json:"providers,omitempty"`
	Service   *service.Status  `json:"service,omitempty"`
}

func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	configDir := fs.String("config-dir", "", "Configuration directory (default: ~/.clipal)")
	timeout := fs.Duration("timeout", 2*time.Second, "Timeout for health/service checks")
	jsonOut := fs.Bool("json", false, "Output status as JSON")
	noService := fs.Bool("no-service", false, "Skip OS service status check")

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	cfgDir := *configDir
	if cfgDir == "" {
		cfgDir = config.GetConfigDir()
	}

	cfg, err := config.Load(cfgDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clipal status failed: %v\n", err)
		os.Exit(1)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "clipal status failed: invalid configuration: %v\n", err)
		os.Exit(1)
	}

	listenAddr := strings.TrimSpace(cfg.Global.ListenAddr)
	port := cfg.Global.Port

	report := statusReport{
		Version:   version,
		Commit:    commit,
		Built:     date,
		ConfigDir: cfgDir,
		Listen:    listenAddr,
		Port:      port,
		WebUI:     fmt.Sprintf("http://localhost:%d/", port),
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	urls := healthCandidateURLs(listenAddr, port)
	h := checkHealth(ctx, urls)
	report.Health = h
	report.OK = h.OK
	if report.OK {
		report.Summary = "OK  Running"
	} else {
		if portLooksInUse(ctx, port) {
			report.Summary = "ERROR  Port in use (not clipal)"
		} else if h.HTTPStatus != 0 {
			report.Summary = "ERROR  Not healthy"
		} else {
			report.Summary = "ERROR  Not running"
		}
	}

	report.Providers = []providerStatus{
		summarizeProviders("claude", cfg.Claude),
		summarizeProviders("openai", cfg.OpenAI),
		summarizeProviders("gemini", cfg.Gemini),
	}

	if !*noService {
		opts := service.Options{
			ConfigDir:  cfgDir,
			BinaryPath: currentExecutablePath(),
		}
		st, _, stErr := service.GetStatus(ctx, opts)
		if stErr == nil {
			report.Service = &st
		}
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		if !report.OK {
			os.Exit(1)
		}
		return
	}

	printStatusReport(report)
	if !report.OK {
		os.Exit(1)
	}
}

func summarizeProviders(client string, cc config.ClientConfig) providerStatus {
	enabled := config.GetEnabledProviders(cc)
	ps := providerStatus{Client: client, Enabled: len(enabled)}
	if cc.Mode == config.ClientModeManual {
		pin := strings.TrimSpace(cc.PinnedProvider)
		for _, p := range enabled {
			if p.Name == pin {
				ps.Active = p.Name
				return ps
			}
		}
	}
	if len(enabled) > 0 {
		ps.Active = enabled[0].Name
	}
	return ps
}

func currentExecutablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		return resolved
	}
	return exe
}

func healthCandidateURLs(listenAddr string, port int) []string {
	var hosts []string

	la := strings.TrimSpace(listenAddr)
	if la == "" {
		hosts = append(hosts, "127.0.0.1")
	} else if la == "0.0.0.0" {
		hosts = append(hosts, "127.0.0.1")
	} else if la == "::" || la == "[::]" {
		hosts = append(hosts, "[::1]")
	} else if strings.EqualFold(la, "localhost") {
		hosts = append(hosts, "localhost", "127.0.0.1")
	} else if ip := net.ParseIP(la); ip != nil {
		if ip.To4() == nil {
			hosts = append(hosts, "["+la+"]")
		} else {
			hosts = append(hosts, la)
		}
	} else {
		hosts = append(hosts, la, "127.0.0.1", "localhost")
	}

	seen := map[string]bool{}
	var urls []string
	for _, h := range hosts {
		u := fmt.Sprintf("http://%s:%d/health", h, port)
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	return urls
}

func checkHealth(ctx context.Context, urls []string) healthStatus {
	client := &http.Client{
		Timeout: 0, // ctx controls timeout
	}

	var lastErr error
	for _, u := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return healthStatus{OK: true, URL: u, HTTPStatus: resp.StatusCode}
		}
		return healthStatus{OK: false, URL: u, HTTPStatus: resp.StatusCode, Error: "unexpected HTTP status"}
	}
	hs := healthStatus{OK: false}
	if len(urls) > 0 {
		hs.URL = urls[0]
	}
	if lastErr != nil {
		hs.Error = lastErr.Error()
	}
	return hs
}

func portLooksInUse(ctx context.Context, port int) bool {
	_ = ctx
	// Best-effort: if we can connect to the port on loopback, something is
	// listening (not necessarily clipal).
	d := net.Dialer{Timeout: 200 * time.Millisecond}
	c, err := d.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		_ = c.Close()
		return true
	}
	// Try IPv6 loopback too.
	c, err = d.Dial("tcp", fmt.Sprintf("[::1]:%d", port))
	if err == nil {
		_ = c.Close()
		return true
	}
	return false
}

func printStatusReport(r statusReport) {
	fmt.Fprintln(os.Stdout, r.Summary)
	fmt.Fprintln(os.Stdout, "")

	fmt.Fprintf(os.Stdout, "Version:    %s (commit %s, built %s)\n", orUnknown(r.Version), orUnknown(r.Commit), orUnknown(r.Built))
	fmt.Fprintf(os.Stdout, "Listen:     %s:%d\n", r.Listen, r.Port)
	if r.Health.OK {
		fmt.Fprintf(os.Stdout, "Health:     %s  (%d OK)\n", r.Health.URL, r.Health.HTTPStatus)
	} else if r.Health.HTTPStatus != 0 {
		fmt.Fprintf(os.Stdout, "Health:     %s  (%d)\n", r.Health.URL, r.Health.HTTPStatus)
	} else if strings.TrimSpace(r.Health.Error) != "" {
		fmt.Fprintf(os.Stdout, "Health:     %s  (unreachable: %s)\n", r.Health.URL, r.Health.Error)
	} else {
		fmt.Fprintf(os.Stdout, "Health:     %s  (unreachable)\n", r.Health.URL)
	}
	fmt.Fprintf(os.Stdout, "Web UI:     %s (localhost-only)\n", r.WebUI)
	fmt.Fprintf(os.Stdout, "Config dir: %s\n", r.ConfigDir)
	fmt.Fprintln(os.Stdout, "")

	fmt.Fprintln(os.Stdout, "Providers:")
	for _, p := range r.Providers {
		active := p.Active
		if active == "" {
			active = "(none)"
		}
		fmt.Fprintf(os.Stdout, "  %-10s enabled %d  (active: %s)\n", displayClientLabel(p.Client), p.Enabled, active)
	}

	if r.Service != nil && r.Service.Manager != "" && r.Service.Manager != "unsupported" {
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "Service:")

		running := "no"
		if r.Service.Running {
			running = "yes"
		}
		installed := "no"
		if r.Service.Installed {
			installed = "yes"
		}

		pidPart := ""
		if r.Service.PID > 0 {
			pidPart = fmt.Sprintf(" (pid %d)", r.Service.PID)
		}
		fmt.Fprintf(os.Stdout, "  %s  installed %s  running %s%s\n", r.Service.Manager, installed, running, pidPart)
		if strings.TrimSpace(r.Service.StdoutPath) != "" || strings.TrimSpace(r.Service.StderrPath) != "" {
			fmt.Fprintf(os.Stdout, "  logs: stdout=%s\n", orDash(r.Service.StdoutPath))
			fmt.Fprintf(os.Stdout, "        stderr=%s\n", orDash(r.Service.StderrPath))
		}
	}

	if !r.OK {
		fmt.Fprintln(os.Stdout, "")
		fmt.Fprintln(os.Stdout, "Next:")
		fmt.Fprintln(os.Stdout, "  - Check service: clipal service status")
		fmt.Fprintln(os.Stdout, "  - Restart service: clipal service restart")
		fmt.Fprintf(os.Stdout, "  - Health check: curl -fsS http://127.0.0.1:%d/health\n", r.Port)
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
			fmt.Fprintln(os.Stdout, "  - View detailed service output: clipal service status --raw")
		}
	}
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func displayClientLabel(client string) string {
	switch strings.TrimSpace(strings.ToLower(client)) {
	case "claude", "claudecode", "claude-code":
		return "Claude"
	case "openai", "codex":
		return "OpenAI"
	case "gemini":
		return "Gemini"
	default:
		return client
	}
}
