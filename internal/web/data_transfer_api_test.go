package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/telemetry"
	"github.com/lansespirit/Clipal/internal/transfer"
)

func TestDataImportPreviewAndApplyExternalCredential(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(dir, "test", nil)
	body := []byte(`{"files":[{"name":"credential.json","data":"{\"type\":\"codex\",\"email\":\"web@example.com\",\"access_token\":\"token\"}"}],"format":"auto"}`)

	previewReq := httptest.NewRequest(http.MethodPost, "/api/data/import/preview", bytes.NewReader(body))
	preview := httptest.NewRecorder()
	api.HandleDataImportPreview(preview, previewReq)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var plan map[string]any
	if err := json.Unmarshal(preview.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan["format"] != "cliproxyapi" || plan["mode"] != "merge" || plan["credentials"] != float64(1) {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	var applyPayload map[string]any
	if err := json.Unmarshal(body, &applyPayload); err != nil {
		t.Fatal(err)
	}
	applyPayload["plan_id"] = plan["id"]
	body, err := json.Marshal(applyPayload)
	if err != nil {
		t.Fatal(err)
	}

	applyReq := httptest.NewRequest(http.MethodPost, "/api/data/import/apply", bytes.NewReader(body))
	apply := httptest.NewRecorder()
	api.HandleDataImportApply(apply, applyReq)
	if apply.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", apply.Code, apply.Body.String())
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.OpenAI.Providers) != 1 || !cfg.OpenAI.Providers[0].UsesOAuth() {
		t.Fatalf("credential not linked: %#v", cfg.OpenAI.Providers)
	}
}

func TestDataImportPreservesInt64FromRawJSONText(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(dir, "test", nil)
	dataset, err := api.transfer.Export()
	if err != nil {
		t.Fatal(err)
	}
	const large = int64(9007199254740993)
	dataset.Data.Usage.Clients["openai"] = map[string]transfer.ProviderUsage{
		"large": {RequestCount: large},
	}
	raw, err := json.Marshal(dataset)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(dataImportRequest{Files: []dataImportFileRequest{{Name: "backup.json", Data: string(raw)}}, Format: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	preview := httptest.NewRecorder()
	api.HandleDataImportPreview(preview, httptest.NewRequest(http.MethodPost, "/api/data/import/preview", bytes.NewReader(payload)))
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var plan struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	var applyReq dataImportRequest
	if err := json.Unmarshal(payload, &applyReq); err != nil {
		t.Fatal(err)
	}
	applyReq.PlanID = plan.ID
	payload, _ = json.Marshal(applyReq)
	apply := httptest.NewRecorder()
	api.HandleDataImportApply(apply, httptest.NewRequest(http.MethodPost, "/api/data/import/apply", bytes.NewReader(payload)))
	if apply.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", apply.Code, apply.Body.String())
	}
	got := api.telemetry.Snapshot().Clients["openai"]["large"].RequestCount
	if got != large {
		t.Fatalf("request_count=%d want=%d", got, large)
	}
}

func TestDecodeDataImportRequestRejectsTrailingJSONAndOversizedBody(t *testing.T) {
	valid := `{"files":[{"name":"a.json","data":"{}"}]}`
	for _, tc := range []struct {
		name string
		body string
		wrap bool
		want int
	}{
		{name: "trailing", body: valid + ` {}`, want: http.StatusBadRequest},
		{name: "oversized", body: valid, wrap: true, want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/data/import/preview", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			if tc.wrap {
				req.Body = http.MaxBytesReader(w, req.Body, 8)
			}
			_, _, ok := decodeDataImportRequest(w, req)
			if ok || w.Code != tc.want {
				t.Fatalf("ok=%v status=%d body=%s", ok, w.Code, w.Body.String())
			}
		})
	}
}

func TestDataImportWithRuntimeReloadDoesNotReenterTelemetryTransaction(t *testing.T) {
	api, router, _, _ := newRuntimeAPI(t)
	defer func() { _ = router.Stop() }()
	if err := api.telemetry.RecordUsage("openai", "p1", telemetry.UsageSnapshot{UsageDelta: telemetry.UsageDelta{TotalTokens: 7}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	dataset, err := api.transfer.Export()
	if err != nil {
		t.Fatal(err)
	}
	dataset.Data.Clients["openai"] = transfer.Client{Mode: "auto", Providers: []transfer.Provider{{Name: "p2", BaseURL: "https://example.com", APIKeys: []string{"k2"}, AuthType: "api_key", Priority: 1}}}
	dataset.Data.Usage.Clients["openai"] = map[string]transfer.ProviderUsage{"p2": {TotalTokens: 11}}
	raw, _ := json.Marshal(dataset)
	payload, _ := json.Marshal(dataImportRequest{Files: []dataImportFileRequest{{Name: "backup.json", Data: string(raw)}}, Format: "clipal", Mode: transfer.ModeReplace})
	preview := httptest.NewRecorder()
	api.HandleDataImportPreview(preview, httptest.NewRequest(http.MethodPost, "/api/data/import/preview", bytes.NewReader(payload)))
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var plan struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(preview.Body.Bytes(), &plan)
	var request dataImportRequest
	_ = json.Unmarshal(payload, &request)
	request.PlanID = plan.ID
	payload, _ = json.Marshal(request)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		api.HandleDataImportApply(w, httptest.NewRequest(http.MethodPost, "/api/data/import/apply", bytes.NewReader(payload)))
		done <- w
	}()
	select {
	case w := <-done:
		if w.Code != http.StatusOK {
			t.Fatalf("apply status=%d body=%s", w.Code, w.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime reload deadlocked while transfer held telemetry transaction")
	}
	if got := api.telemetry.Snapshot().Clients["openai"]["p2"].TotalTokens; got != 11 {
		t.Fatalf("imported usage=%d", got)
	}
}

func TestDataExportUsesCanonicalEnvelopeAndFilename(t *testing.T) {
	dir := t.TempDir()
	global := config.DefaultGlobalConfig()
	data, _ := json.Marshal(global)
	_ = data
	api := NewAPI(dir, "v-test", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/data/export", nil)
	w := httptest.NewRecorder()
	api.HandleDataExport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Disposition"); got != "attachment; filename=clipal-data.json" {
		t.Fatalf("content disposition=%q", got)
	}
	var envelope map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["schema"] != "clipal.data" || envelope["schema_version"] != float64(1) {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("export should not create config: %v", err)
	}
}

func TestDataImportApplyRejectsMissingAndStalePlanID(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(dir, "test", nil)
	credential := func(email string) string {
		payload, err := json.Marshal(dataImportRequest{
			Files:  []dataImportFileRequest{{Name: "credential.json", Data: `{"type":"codex","email":"` + email + `","access_token":"token"}`}},
			Format: "auto",
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(payload)
	}

	apply := httptest.NewRecorder()
	api.HandleDataImportApply(apply, httptest.NewRequest(http.MethodPost, "/api/data/import/apply", strings.NewReader(credential("a@example.com"))))
	if apply.Code != http.StatusBadRequest || !strings.Contains(apply.Body.String(), "plan_id") {
		t.Fatalf("apply without plan_id status=%d body=%s", apply.Code, apply.Body.String())
	}

	preview := httptest.NewRecorder()
	api.HandleDataImportPreview(preview, httptest.NewRequest(http.MethodPost, "/api/data/import/preview", strings.NewReader(credential("a@example.com"))))
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	var plan struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.ID == "" {
		t.Fatal("preview returned empty plan id")
	}

	// The import data changed after preview, so the previewed plan is stale.
	var stale dataImportRequest
	if err := json.Unmarshal([]byte(credential("b@example.com")), &stale); err != nil {
		t.Fatal(err)
	}
	stale.PlanID = plan.ID
	stalePayload, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	apply = httptest.NewRecorder()
	api.HandleDataImportApply(apply, httptest.NewRequest(http.MethodPost, "/api/data/import/apply", bytes.NewReader(stalePayload)))
	if apply.Code != http.StatusConflict {
		t.Fatalf("stale apply status=%d body=%s", apply.Code, apply.Body.String())
	}

	if _, err := os.Stat(filepath.Join(dir, "openai.yaml")); !os.IsNotExist(err) {
		t.Fatalf("rejected apply mutated configuration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "oauth")); !os.IsNotExist(err) {
		t.Fatalf("rejected apply created oauth state: %v", err)
	}
}
