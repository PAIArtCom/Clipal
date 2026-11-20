package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/PAIArtCom/Clipal/internal/proxy"
)

func (a *API) HandleProviderTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.runtime == nil {
		writeError(w, "provider runtime is unavailable", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Step     string `json:"step"`
		ID       string `json:"id"`
		Model    string `json:"model"`
		Protocol string `json:"protocol"`
		Key      int    `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid provider test request", http.StatusBadRequest)
		return
	}
	client, name, _ := extractClientProviderSubresource(r.URL.EscapedPath())
	var result any
	var err error
	switch req.Step {
	case "models":
		result, err = a.runtime.FetchProviderTestModels(r.Context(), proxy.ClientType(client), name, req.Key)
	case "generate":
		result, err = a.runtime.RunProviderTest(r.Context(), proxy.ClientType(client), name, req.ID, req.Model, req.Protocol)
	default:
		writeError(w, "invalid provider test step", http.StatusBadRequest)
		return
	}
	if err != nil {
		status := http.StatusBadGateway
		reason := ""
		upstreamStatus := 0
		detail := ""
		var failure *proxy.ProviderTestFailure
		switch {
		case errors.Is(err, proxy.ErrProviderTestUnavailable):
			status = http.StatusBadRequest
			reason = "unavailable"
		case errors.Is(err, proxy.ErrProviderTestModel):
			status = http.StatusBadRequest
			reason = "model_not_found"
		case errors.Is(err, proxy.ErrProviderTestProtocol):
			status = http.StatusBadRequest
			reason = "protocol"
		case errors.Is(err, proxy.ErrProviderTestBusy):
			status = http.StatusConflict
			reason = "test_busy"
		case errors.Is(err, proxy.ErrProviderTestExpired):
			status = http.StatusConflict
			reason = "expired"
		case errors.As(err, &failure):
			reason, upstreamStatus, detail = failure.Reason, failure.UpstreamStatus, failure.Detail
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		writeJSON(w, struct {
			ErrorResponse
			UpstreamStatus int    `json:"upstream_status,omitempty"`
			Detail         string `json:"detail,omitempty"`
		}{ErrorResponse{Error: err.Error(), Status: status, Reason: reason}, upstreamStatus, detail})
		return
	}
	writeJSON(w, result)
}
