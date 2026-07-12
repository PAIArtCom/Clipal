package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/lansespirit/Clipal/internal/transfer"
)

type dataImportFileRequest struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

type dataImportRequest struct {
	Files  []dataImportFileRequest `json:"files"`
	Format string                  `json:"format,omitempty"`
	Mode   transfer.Mode           `json:"mode,omitempty"`
	PlanID string                  `json:"plan_id,omitempty"`
}

func (a *API) HandleDataExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.transfer == nil {
		writeError(w, "data transfer service is unavailable", http.StatusInternalServerError)
		return
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	data, err := a.transfer.ExportJSON()
	if err != nil {
		writeError(w, fmt.Sprintf("failed to export data: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=clipal-data.json")
	_, _ = w.Write(data)
}

func (a *API) HandleDataImportPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.transfer == nil {
		writeError(w, "data transfer service is unavailable", http.StatusInternalServerError)
		return
	}
	req, inputs, ok := decodeDataImportRequest(w, r)
	if !ok {
		return
	}
	plan, err := a.transfer.Analyze(inputs, req.Format, req.Mode)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, plan)
}

func (a *API) HandleDataImportApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.transfer == nil {
		writeError(w, "data transfer service is unavailable", http.StatusInternalServerError)
		return
	}
	req, inputs, ok := decodeDataImportRequest(w, r)
	if !ok {
		return
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	plan, err := a.transfer.Analyze(inputs, req.Format, req.Mode)
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.PlanID) == "" {
		writeError(w, "plan_id from import preview is required", http.StatusBadRequest)
		return
	}
	if req.PlanID != plan.ID {
		writeError(w, "import data or mode changed after preview; preview again", http.StatusConflict)
		return
	}
	result, err := a.transfer.Apply(plan)
	if err != nil {
		writeError(w, fmt.Sprintf("failed to apply import: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, result)
}

func decodeDataImportRequest(w http.ResponseWriter, r *http.Request) (dataImportRequest, []transfer.Input, bool) {
	var req dataImportRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		status := http.StatusBadRequest
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, fmt.Sprintf("invalid request body: %v", err), status)
		return req, nil, false
	}
	var trailing any
	if err := dec.Decode(&trailing); err == nil {
		writeError(w, "invalid request body: unexpected trailing JSON value", http.StatusBadRequest)
		return req, nil, false
	} else if !errors.Is(err, io.EOF) {
		status := http.StatusBadRequest
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, fmt.Sprintf("invalid request body: %v", err), status)
		return req, nil, false
	}
	if len(req.Files) == 0 {
		writeError(w, "at least one import file is required", http.StatusBadRequest)
		return req, nil, false
	}
	if len(req.Files) > transfer.MaxImportFiles {
		writeError(w, fmt.Sprintf("too many import files: max %d", transfer.MaxImportFiles), http.StatusBadRequest)
		return req, nil, false
	}
	inputs := make([]transfer.Input, 0, len(req.Files))
	for i, file := range req.Files {
		name := strings.TrimSpace(file.Name)
		if name == "" {
			name = fmt.Sprintf("import-%d.json", i+1)
		}
		if strings.TrimSpace(file.Data) == "" {
			writeError(w, fmt.Sprintf("%s has no JSON data", name), http.StatusBadRequest)
			return req, nil, false
		}
		inputs = append(inputs, transfer.Input{Name: name, Data: []byte(file.Data)})
	}
	return req, inputs, true
}
