package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/service"
)

func TestHandleServiceStatus_InstalledButStoppedIsNotOK(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)
	stubServiceGetStatus(t, func(context.Context, service.Options) (service.Status, string, error) {
		return service.Status{
			Manager:   "systemd",
			Installed: true,
			Loaded:    true,
			Running:   false,
			Detail:    "active=inactive sub=dead",
		}, "ActiveState=inactive", nil
	})

	resp := runServiceStatusRequest(t, api)
	if resp.OS != runtime.GOOS {
		t.Fatalf("os = %q, want %q", resp.OS, runtime.GOOS)
	}
	if !resp.Supported || !resp.Installed {
		t.Fatalf("response = %#v", resp)
	}
	if resp.OK {
		t.Fatalf("expected stopped service to report ok=false: %#v", resp)
	}
	if !resp.Loaded || resp.Running {
		t.Fatalf("expected loaded=true running=false: %#v", resp)
	}
	if resp.Detail != "active=inactive sub=dead" {
		t.Fatalf("detail = %q", resp.Detail)
	}
	if resp.Output != "ActiveState=inactive" {
		t.Fatalf("output = %q", resp.Output)
	}
}

func TestHandleServiceStatus_RunningServiceIsOK(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)
	stubServiceGetStatus(t, func(context.Context, service.Options) (service.Status, string, error) {
		return service.Status{Manager: "launchd", Installed: true, Loaded: true, Running: true}, "state=running", nil
	})

	resp := runServiceStatusRequest(t, api)
	if !resp.OK || !resp.Supported || !resp.Installed {
		t.Fatalf("response = %#v", resp)
	}
	if !resp.Loaded || !resp.Running {
		t.Fatalf("expected loaded=true running=true: %#v", resp)
	}
}

func TestHandleServiceStatus_UnsupportedManager(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)
	stubServiceGetStatus(t, func(context.Context, service.Options) (service.Status, string, error) {
		return service.Status{Manager: "unsupported"}, "", nil
	})

	resp := runServiceStatusRequest(t, api)
	if resp.Supported {
		t.Fatalf("expected unsupported manager to report supported=false: %#v", resp)
	}
	if resp.Installed || resp.OK || resp.Loaded || resp.Running {
		t.Fatalf("unexpected installed/ok state: %#v", resp)
	}
}

func TestHandleServiceStatus_NotInstalledIncludesInstallGuidance(t *testing.T) {
	api := NewAPI(t.TempDir(), "test", nil)
	stubServiceGetStatus(t, func(context.Context, service.Options) (service.Status, string, error) {
		return service.Status{Manager: "systemd", Installed: false}, "", nil
	})

	resp := runServiceStatusRequest(t, api)
	if !resp.Supported {
		t.Fatalf("response = %#v", resp)
	}
	if resp.Installed || resp.OK || resp.Loaded || resp.Running {
		t.Fatalf("unexpected installed/ok state: %#v", resp)
	}
	if resp.InstallCommand == "" {
		t.Fatalf("expected install command in response: %#v", resp)
	}
}

func stubServiceGetStatus(t *testing.T, fn func(context.Context, service.Options) (service.Status, string, error)) {
	t.Helper()
	orig := serviceGetStatusFunc
	serviceGetStatusFunc = fn
	t.Cleanup(func() {
		serviceGetStatusFunc = orig
	})
}

func runServiceStatusRequest(t *testing.T, api *API) ServiceStatusResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/service/status", nil)
	w := httptest.NewRecorder()
	api.HandleServiceStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp ServiceStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal: %v\nbody=%s", err, w.Body.String())
	}
	return resp
}
