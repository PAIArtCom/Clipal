package web

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/lansespirit/Clipal/internal/config"
	oauthpkg "github.com/lansespirit/Clipal/internal/oauth"
	transferpkg "github.com/lansespirit/Clipal/internal/transfer"
)

const (
	maxOAuthImportFiles           = 512
	maxOAuthImportFileBytes       = 1 << 20 // 1 MiB per uploaded credential file
	oauthImportMultipartMaxMemory = 8 << 20 // spill larger payloads to disk
)

type oauthImportCandidate struct {
	cred   *oauthpkg.Credential
	result OAuthImportFileResultResponse
}

func (a *API) HandleImportCLIProxyAPICredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(oauthImportMultipartMaxMemory); err != nil {
		writeError(w, fmt.Sprintf("invalid multipart form: %v", err), http.StatusBadRequest)
		return
	}

	clientType, ok := config.CanonicalClientType(strings.TrimSpace(r.FormValue("client_type")))
	if !ok {
		writeError(w, "invalid client type", http.StatusBadRequest)
		return
	}
	requestedProvider := config.OAuthProvider(strings.ToLower(strings.TrimSpace(r.FormValue("provider"))))
	if requestedProvider == "" {
		writeError(w, "provider is required", http.StatusBadRequest)
		return
	}
	if err := validateOAuthProviderForClient(clientType, requestedProvider); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	headers := r.MultipartForm.File["files"]
	if len(headers) == 0 {
		writeError(w, "no credential files uploaded", http.StatusBadRequest)
		return
	}
	if len(headers) > maxOAuthImportFiles {
		writeError(w, fmt.Sprintf("too many credential files: max %d", maxOAuthImportFiles), http.StatusBadRequest)
		return
	}

	resp := OAuthImportResponse{
		ClientType: clientType,
		Provider:   requestedProvider,
		Results:    make([]OAuthImportFileResultResponse, 0, len(headers)),
	}
	candidates := make([]oauthImportCandidate, 0, len(headers))
	for _, header := range headers {
		fileCandidates := a.parseCLIProxyAPIImportCandidates(header, requestedProvider)
		for _, candidate := range fileCandidates {
			resp.addResult(candidate.result)
			candidates = append(candidates, candidate)
		}
	}

	a.configMu.Lock()
	defer a.configMu.Unlock()
	seen := make([]*oauthpkg.Credential, 0, len(candidates))
	selected := make([]oauthpkg.Credential, 0, len(candidates))
	selectedIndices := make([]int, 0, len(candidates))
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.cred == nil {
			continue
		}

		if oauthImportCandidateSeen(seen, candidate.cred) {
			candidate.result.Status = "skipped"
			candidate.result.Message = "duplicate account in selected files"
			candidate.cred = nil
			resp.recountResult(i, candidate.result)
			continue
		}

		seen = append(seen, candidate.cred.Clone())
		selected = append(selected, *candidate.cred.Clone())
		selectedIndices = append(selectedIndices, i)
	}
	if len(selected) > 0 {
		if a.transfer == nil {
			writeError(w, "data transfer service is unavailable", http.StatusInternalServerError)
			return
		}
		plan, err := a.transfer.AnalyzeCredentials(selected, nil, len(headers))
		if err != nil {
			writeAPIError(w, newAPIError(http.StatusBadRequest, err.Error(), err))
			return
		}
		applied, err := a.transfer.Apply(plan)
		if err != nil {
			writeAPIError(w, newAPIError(http.StatusInternalServerError, fmt.Sprintf("failed to apply credential import: %v", err), err))
			return
		}
		for resultIndex, detail := range applied.CredentialResults {
			candidateIndex := selectedIndices[resultIndex]
			candidate := &candidates[candidateIndex]
			candidate.result.Status = "imported"
			candidate.result.Ref = detail.Ref
			candidate.result.Email = detail.Email
			candidate.result.ProviderName = detail.ProviderName
			candidate.result.ProviderAction = detail.ProviderAction
			switch detail.ProviderAction {
			case "created", "relinked":
				candidate.result.Message = fmt.Sprintf("imported account and %s provider %s", detail.ProviderAction, detail.ProviderName)
				resp.LinkedCount++
			default:
				candidate.result.Message = fmt.Sprintf("imported account and reused provider %s", detail.ProviderName)
			}
			resp.recountResult(candidateIndex, candidate.result)
		}
	}

	resp.Message = summarizeOAuthImport(resp)
	writeJSON(w, resp)
}

func (a *API) parseCLIProxyAPIImportCandidates(header *multipart.FileHeader, requestedProvider config.OAuthProvider) []oauthImportCandidate {
	baseFile := strings.TrimSpace(header.Filename)
	if baseFile == "" {
		baseFile = "credential.json"
	}

	baseResult := OAuthImportFileResultResponse{
		File:   baseFile,
		Status: "skipped",
	}
	if ext := strings.ToLower(filepath.Ext(baseFile)); ext != ".json" {
		baseResult.Message = "skipped non-JSON file"
		return []oauthImportCandidate{{result: baseResult}}
	}

	data, err := readOAuthImportFile(header)
	if err != nil {
		baseResult.Status = "failed"
		baseResult.Message = err.Error()
		return []oauthImportCandidate{{result: baseResult}}
	}

	entries := transferpkg.DecodeCredentialCandidates(data)

	results := make([]oauthImportCandidate, 0, len(entries))
	for _, entry := range entries {
		result := OAuthImportFileResultResponse{
			File:   oauthImportEntryFile(baseFile, entry.Entry),
			Status: "skipped",
		}
		if entry.Status != "ready" {
			result.Status = entry.Status
			result.Message = entry.Message
			results = append(results, oauthImportCandidate{result: result})
			continue
		}

		cred := entry.Credential
		if cred == nil {
			result.Message = "skipped entry without supported OAuth credential data"
			results = append(results, oauthImportCandidate{result: result})
			continue
		}
		if cred.Provider != requestedProvider {
			result.Message = fmt.Sprintf("skipped %s credential while importing %s accounts", cred.Provider, requestedProvider)
			results = append(results, oauthImportCandidate{result: result})
			continue
		}

		result.Provider = cred.Provider
		result.Ref = cred.Ref
		result.Email = cred.Email
		results = append(results, oauthImportCandidate{cred: cred, result: result})
	}
	return results
}

func oauthImportEntryFile(file string, entry string) string {
	file = strings.TrimSpace(file)
	if file == "" {
		file = "credential.json"
	}
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return file
	}
	return file + "#" + entry
}

func readOAuthImportFile(header *multipart.FileHeader) ([]byte, error) {
	f, err := header.Open()
	if err != nil {
		return nil, fmt.Errorf("open uploaded file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	data, err := io.ReadAll(io.LimitReader(f, maxOAuthImportFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read uploaded file: %w", err)
	}
	if len(data) > maxOAuthImportFileBytes {
		return nil, fmt.Errorf("uploaded file exceeds %d bytes", maxOAuthImportFileBytes)
	}
	return data, nil
}

func (resp *OAuthImportResponse) addResult(result OAuthImportFileResultResponse) {
	resp.Results = append(resp.Results, result)
	switch result.Status {
	case "imported":
		resp.ImportedCount++
	case "failed":
		resp.FailedCount++
	default:
		resp.SkippedCount++
	}
}

func (resp *OAuthImportResponse) recountResult(index int, next OAuthImportFileResultResponse) {
	if resp == nil || index < 0 || index >= len(resp.Results) {
		return
	}
	prev := resp.Results[index]
	resp.adjustResultCount(prev.Status, -1)
	resp.adjustResultCount(next.Status, 1)
	resp.Results[index] = next
}

func (resp *OAuthImportResponse) adjustResultCount(status string, delta int) {
	switch status {
	case "imported":
		resp.ImportedCount += delta
	case "failed":
		resp.FailedCount += delta
	default:
		resp.SkippedCount += delta
	}
}

func summarizeOAuthImport(resp OAuthImportResponse) string {
	parts := []string{
		fmt.Sprintf("imported %d account(s)", resp.ImportedCount),
		fmt.Sprintf("linked %d provider(s)", resp.LinkedCount),
	}
	if resp.SkippedCount > 0 {
		parts = append(parts, fmt.Sprintf("skipped %d entry(s)", resp.SkippedCount))
	}
	if resp.FailedCount > 0 {
		parts = append(parts, fmt.Sprintf("failed %d entry(s)", resp.FailedCount))
	}
	return strings.Join(parts, ", ")
}

func oauthImportCandidateSeen(seen []*oauthpkg.Credential, cred *oauthpkg.Credential) bool {
	for _, existing := range seen {
		if sameOAuthImportCandidate(existing, cred) {
			return true
		}
	}
	return false
}

func sameOAuthImportCandidate(a *oauthpkg.Credential, b *oauthpkg.Credential) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Provider != b.Provider {
		return false
	}
	if ref := strings.TrimSpace(a.Ref); ref != "" && ref == strings.TrimSpace(b.Ref) {
		return true
	}
	return oauthpkg.SameAccountIdentity(a, b)
}
