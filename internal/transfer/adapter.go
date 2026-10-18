package transfer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/oauth"
	"github.com/PAIArtCom/Clipal/internal/telemetry"
)

const (
	FormatAuto                 = "auto"
	FormatClipal               = "clipal-v1"
	FormatCLIProxyAPI          = "cliproxyapi"
	FormatSub2API              = "sub2api"
	FormatCodexAuth            = "codex-auth"
	FormatMixed                = "mixed"
	maxCatalogVersionsPerUsage = 64
)

type Decoder interface {
	ID() string
	Detect(inputs []Input) Confidence
	Decode(inputs []Input, opts DecodeOptions) (*Dataset, *Report, error)
}

type Encoder interface {
	ID() string
	Encode(dataset *Dataset, opts EncodeOptions) ([]byte, error)
}

// CredentialCandidate is the adapter-layer result used by the focused OAuth
// import UI. It keeps format/error interpretation out of Web handlers while
// preserving per-entry feedback for bundle formats.
type CredentialCandidate struct {
	Entry      string
	Status     string
	Message    string
	Credential *oauth.Credential
}

func DecodeCredentialCandidates(data []byte) []CredentialCandidate {
	entries, err := oauth.ParseOAuthImportEntries(data)
	if err != nil {
		candidate := CredentialCandidate{Status: "skipped", Message: "skipped file without supported OAuth credential data"}
		if !errors.Is(err, oauth.ErrOAuthImportNotCredential) {
			candidate.Status, candidate.Message = "failed", err.Error()
		}
		return []CredentialCandidate{candidate}
	}
	if len(entries) == 0 {
		return []CredentialCandidate{{Status: "skipped", Message: "skipped file without supported OAuth credential data"}}
	}
	out := make([]CredentialCandidate, 0, len(entries))
	for _, entry := range entries {
		candidate := CredentialCandidate{Entry: entry.Entry, Status: "skipped", Credential: entry.Credential}
		switch {
		case errors.Is(entry.Err, oauth.ErrOAuthImportNotCredential):
			candidate.Message = "skipped entry without supported OAuth credential data"
		case errors.Is(entry.Err, oauth.ErrOAuthImportUnsupportedType):
			candidate.Message = entry.Err.Error()
		case errors.Is(entry.Err, oauth.ErrOAuthImportDisabledCredential):
			candidate.Message = "skipped disabled OAuth credential"
		case entry.Err != nil:
			candidate.Status, candidate.Message = "failed", entry.Err.Error()
		case entry.Credential == nil:
			candidate.Message = "skipped entry without supported OAuth credential data"
		default:
			candidate.Status = "ready"
		}
		out = append(out, candidate)
	}
	return out
}

type Registry struct {
	decoders []Decoder
	encoders map[string]Encoder
}

func NewRegistry() *Registry {
	native := nativeAdapter{}
	return &Registry{
		decoders: []Decoder{
			native,
			externalCredentialAdapter{id: FormatCodexAuth, parse: parseCodexAuth},
			externalCredentialAdapter{id: FormatSub2API, parse: parseSub2API},
			externalCredentialAdapter{id: FormatCLIProxyAPI, parse: parseCLIProxyAPI},
		},
		encoders: map[string]Encoder{FormatClipal: native},
	}
}

func (r *Registry) Decode(format string, inputs []Input) (*Dataset, *Report, error) {
	if r == nil {
		r = NewRegistry()
	}
	format = normalizeFormat(format)
	if len(inputs) == 0 {
		return nil, nil, fmt.Errorf("at least one input file is required")
	}
	if err := validateInputs(inputs); err != nil {
		return nil, nil, err
	}
	// All supported formats are JSON. Reject malformed files before detection,
	// where an entry-level parser error could otherwise look like a format match.
	for _, input := range inputs {
		if !json.Valid(input.Data) {
			return nil, nil, fmt.Errorf("invalid JSON in import file %q", input.Name)
		}
	}
	if format != FormatAuto {
		for _, decoder := range r.decoders {
			if decoder.ID() == format {
				return decoder.Decode(inputs, DecodeOptions{})
			}
		}
		return nil, nil, fmt.Errorf("unsupported import format %q", format)
	}
	selected := make([]Decoder, 0, len(inputs))
	for _, input := range inputs {
		best := ConfidenceNone
		var decoderForInput Decoder
		for _, decoder := range r.decoders {
			if confidence := decoder.Detect([]Input{input}); confidence > best {
				best, decoderForInput = confidence, decoder
			}
		}
		if decoderForInput == nil || best == ConfidenceNone {
			return nil, nil, fmt.Errorf("could not detect a supported import format for %s", input.Name)
		}
		selected = append(selected, decoderForInput)
	}
	allSame := true
	for i := 1; i < len(selected); i++ {
		allSame = allSame && selected[i].ID() == selected[0].ID()
	}
	if allSame {
		return selected[0].Decode(inputs, DecodeOptions{})
	}
	for _, decoder := range selected {
		if decoder.ID() == FormatClipal {
			return nil, nil, fmt.Errorf("a native Clipal dataset cannot be mixed with other input files")
		}
	}
	dataset := emptyDataset()
	report := &Report{Format: FormatMixed}
	for i, decoder := range selected {
		decoded, part, err := decoder.Decode([]Input{inputs[i]}, DecodeOptions{})
		if err != nil {
			return nil, nil, err
		}
		dataset.Data.Credentials = append(dataset.Data.Credentials, decoded.Data.Credentials...)
		report.Warnings = append(report.Warnings, part.Warnings...)
	}
	return dataset, report, nil
}

func validateInputs(inputs []Input) error {
	if len(inputs) > MaxImportFiles {
		return fmt.Errorf("too many import files: max %d", MaxImportFiles)
	}
	var total int64
	for i, input := range inputs {
		name := strings.TrimSpace(input.Name)
		if len(name) > MaxImportFilenameBytes {
			return fmt.Errorf("import file %d name exceeds %d bytes", i+1, MaxImportFilenameBytes)
		}
		if len(input.Data) > MaxImportFileBytes {
			return fmt.Errorf("%s exceeds %d bytes", name, MaxImportFileBytes)
		}
		total += int64(len(input.Data))
		if total > MaxImportTotalBytes {
			return fmt.Errorf("import files exceed %d total bytes", MaxImportTotalBytes)
		}
	}
	return nil
}

func (r *Registry) Encode(format string, dataset *Dataset) ([]byte, error) {
	if r == nil {
		r = NewRegistry()
	}
	format = normalizeFormat(format)
	encoder := r.encoders[format]
	if encoder == nil {
		return nil, fmt.Errorf("unsupported export format %q", format)
	}
	return encoder.Encode(dataset, EncodeOptions{})
}

func normalizeFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "auto":
		return FormatAuto
	case "clipal", "clipal-v1", "clipal.data/v1":
		return FormatClipal
	case "cli-proxy-api", "cliproxyapi":
		return FormatCLIProxyAPI
	case "sub2api":
		return FormatSub2API
	case "codex", "codex-auth", "auth.json":
		return FormatCodexAuth
	default:
		return strings.ToLower(strings.TrimSpace(format))
	}
}

type nativeAdapter struct{}

type nativeDatasetWire struct {
	Schema        string         `json:"schema"`
	SchemaVersion int            `json:"schema_version"`
	ExportedAt    time.Time      `json:"exported_at"`
	Producer      Producer       `json:"producer"`
	Data          nativeDataWire `json:"data"`
}

type nativeDataWire struct {
	Global      Global            `json:"global"`
	Clients     map[string]Client `json:"clients"`
	Credentials []Credential      `json:"credentials"`
	Usage       json.RawMessage   `json:"usage"`
}

func (nativeAdapter) ID() string { return FormatClipal }
func (nativeAdapter) Detect(inputs []Input) Confidence {
	if len(inputs) != 1 {
		return ConfidenceNone
	}
	var header struct {
		Schema        string `json:"schema"`
		SchemaVersion int    `json:"schema_version"`
	}
	if json.Unmarshal(inputs[0].Data, &header) == nil && header.Schema == SchemaName {
		return ConfidenceCertain
	}
	return ConfidenceNone
}
func (nativeAdapter) Decode(inputs []Input, _ DecodeOptions) (*Dataset, *Report, error) {
	if len(inputs) != 1 {
		return nil, nil, fmt.Errorf("%s imports exactly one file", FormatClipal)
	}
	dec := json.NewDecoder(bytes.NewReader(inputs[0].Data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	var wire nativeDatasetWire
	if err := dec.Decode(&wire); err != nil {
		return nil, nil, fmt.Errorf("decode %s: %w", FormatClipal, err)
	}
	if err := requireJSONEOF(dec); err != nil {
		return nil, nil, fmt.Errorf("decode %s: %w", FormatClipal, err)
	}
	dataset := &Dataset{
		Schema: wire.Schema, SchemaVersion: wire.SchemaVersion, ExportedAt: wire.ExportedAt, Producer: wire.Producer,
		Data: Data{Global: wire.Data.Global, Clients: wire.Data.Clients, Credentials: wire.Data.Credentials},
	}
	usage, warnings := decodeAndSanitizeUsage(wire.Data.Usage)
	dataset.Data.Usage = usageFromTelemetry(usage.toTelemetry())
	if err := validateDataset(dataset); err != nil {
		return nil, nil, err
	}
	return dataset, &Report{Format: FormatClipal, Warnings: warnings}, nil
}
func (nativeAdapter) Encode(dataset *Dataset, _ EncodeOptions) ([]byte, error) {
	if dataset == nil {
		return nil, fmt.Errorf("dataset is required")
	}
	clean := *dataset
	clean.Data = dataset.Data
	clean.Data.Clients = maps.Clone(dataset.Data.Clients)
	if err := validateDataset(&clean); err != nil {
		return nil, err
	}
	clean.Data.Usage, _ = sanitizeUsage(dataset.Data.Usage)
	clean.Data.Usage = usageFromTelemetry(clean.Data.Usage.toTelemetry())
	data, err := json.MarshalIndent(&clean, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", FormatClipal, err)
	}
	return append(data, '\n'), nil
}

func validateDataset(dataset *Dataset) error {
	if dataset == nil {
		return fmt.Errorf("dataset is required")
	}
	if dataset.Schema != SchemaName {
		return fmt.Errorf("unsupported schema %q", dataset.Schema)
	}
	if dataset.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported %s schema version %d", SchemaName, dataset.SchemaVersion)
	}
	if dataset.Producer.Name == "" {
		return fmt.Errorf("producer.name is required")
	}
	if dataset.Producer.Version == "" {
		return fmt.Errorf("producer.version is required")
	}
	if dataset.ExportedAt.IsZero() {
		return fmt.Errorf("exported_at is required")
	}
	if dataset.Data.Clients == nil {
		return fmt.Errorf("data.clients is required")
	}

	// Accept old exports, but keep one canonical channel in every new export.
	if legacy, ok := dataset.Data.Clients["gemini"]; ok {
		if current, exists := dataset.Data.Clients["google"]; exists && !reflect.DeepEqual(current, legacy) {
			return fmt.Errorf("data.clients.google and data.clients.gemini contain conflicting configuration")
		}
		dataset.Data.Clients["google"] = legacy
		delete(dataset.Data.Clients, "gemini")
	}
	for client := range dataset.Data.Clients {
		canonical, ok := config.CanonicalClientType(client)
		if !ok || canonical != client {
			return fmt.Errorf("data.clients.%s is not supported by schema version %d", client, SchemaVersion)
		}
	}
	for _, client := range []string{"claude", "openai", "google"} {
		entry, ok := dataset.Data.Clients[client]
		if !ok {
			return fmt.Errorf("data.clients.%s is required", client)
		}
		if entry.Providers == nil {
			return fmt.Errorf("data.clients.%s.providers is required", client)
		}
	}
	if dataset.Data.Credentials == nil {
		return fmt.Errorf("data.credentials is required")
	}
	if dataset.Data.Usage.Clients == nil {
		dataset.Data.Usage.Clients = map[string]map[string]ProviderUsage{}
	}
	return nil
}

func decodeAndSanitizeUsage(raw json.RawMessage) (Usage, []string) {
	empty := Usage{Clients: map[string]map[string]ProviderUsage{}}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return empty, []string{"ignored missing usage history"}
	}
	var root struct {
		Clients map[string]json.RawMessage `json:"clients"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil || root.Clients == nil {
		return empty, []string{"ignored malformed usage history"}
	}
	out := Usage{Clients: make(map[string]map[string]ProviderUsage, len(root.Clients))}
	warnings := []string{}
	clients := sortedRawKeys(root.Clients)
	for _, client := range clients {
		var providers map[string]json.RawMessage
		if err := decodeUseNumber(root.Clients[client], &providers); err != nil {
			warnings = append(warnings, fmt.Sprintf("ignored malformed usage client %q", client))
			continue
		}
		cleanProviders := make(map[string]ProviderUsage, len(providers))
		for _, provider := range sortedRawKeys(providers) {
			var usage ProviderUsage
			if err := decodeUseNumber(providers[provider], &usage); err != nil {
				warnings = append(warnings, fmt.Sprintf("ignored malformed usage for %s/%s", client, provider))
				continue
			}
			clean, itemWarnings := sanitizeProviderUsage(client, provider, usage)
			warnings = append(warnings, itemWarnings...)
			cleanProviders[provider] = clean
		}
		out.Clients[client] = cleanProviders
	}
	return out, warnings
}

func sanitizeUsage(usage Usage) (Usage, []string) {
	out := Usage{Clients: make(map[string]map[string]ProviderUsage, len(usage.Clients))}
	warnings := []string{}
	for _, client := range sortedRawKeys(usage.Clients) {
		providers := usage.Clients[client]
		cleanProviders := make(map[string]ProviderUsage, len(providers))
		for _, provider := range sortedRawKeys(providers) {
			clean, itemWarnings := sanitizeProviderUsage(client, provider, providers[provider])
			warnings = append(warnings, itemWarnings...)
			cleanProviders[provider] = clean
		}
		out.Clients[client] = cleanProviders
	}
	return out, warnings
}

func decodeUseNumber(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func sortedRawKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sanitizeProviderUsage(client, provider string, usage ProviderUsage) (ProviderUsage, []string) {
	path := client + "/" + provider
	warnings := []string{}
	counters := []*int64{&usage.RequestCount, &usage.SuccessCount, &usage.InputTokens, &usage.OutputTokens, &usage.TotalTokens, &usage.ReasoningTokens, &usage.ThoughtsTokens}
	for _, counter := range counters {
		if *counter < 0 {
			*counter = 0
			warnings = appendUniqueWarning(warnings, "ignored negative usage counters for "+path)
		}
	}
	if usage.TotalCostMicros < 0 {
		usage.TotalCostMicros, usage.HasCost = 0, false
		warnings = append(warnings, "ignored negative usage cost for "+path)
	} else if usage.TotalCostMicros > 0 && !usage.HasCost {
		usage.HasCost = true
		warnings = append(warnings, "preserved unattributed usage cost for "+path)
	}
	var changed bool
	usage.CostSources, usage.CatalogVersions, changed = sanitizeCostAttribution(usage.HasCost, usage.CostSources, usage.CatalogVersions)
	if changed {
		warnings = append(warnings, "normalized usage cost attribution for "+path)
	}
	cleanDaily := make(map[string]DailyCostBucket, len(usage.DailyCosts))
	for _, day := range sortedRawKeys(usage.DailyCosts) {
		bucket := usage.DailyCosts[day]
		parsed, err := time.Parse("2006-01-02", day)
		if err != nil || parsed.Format("2006-01-02") != day || bucket.CostMicros < 0 {
			warnings = append(warnings, fmt.Sprintf("ignored invalid daily usage bucket %s/%s", path, day))
			continue
		}
		if bucket.CostMicros > 0 && !bucket.HasCost {
			bucket.HasCost = true
		}
		bucket.CostSources, bucket.CatalogVersions, changed = sanitizeCostAttribution(bucket.HasCost, bucket.CostSources, bucket.CatalogVersions)
		if changed {
			warnings = append(warnings, fmt.Sprintf("normalized daily cost attribution for %s/%s", path, day))
		}
		cleanDaily[day] = bucket
		if bucket.HasCost {
			usage.HasCost = true
			usage.CostSources = appendStringSet(usage.CostSources, bucket.CostSources...)
			usage.CatalogVersions = appendStringSet(usage.CatalogVersions, bucket.CatalogVersions...)
		}
	}
	usage.DailyCosts = cleanDaily
	if usage.HasCost && len(usage.CostSources) == 0 {
		usage.CostSources = []string{telemetry.CostSourceLegacyUnknown}
	}
	return usage, warnings
}

func sanitizeCostAttribution(hasCost bool, sources, versions []string) ([]string, []string, bool) {
	if !hasCost {
		return nil, nil, len(sources) > 0 || len(versions) > 0
	}
	originalSources, originalVersions := append([]string(nil), sources...), append([]string(nil), versions...)
	cleanSources := []string{}
	for _, source := range sources {
		source = strings.TrimSpace(source)
		switch source {
		case telemetry.CostSourceUpstream, telemetry.CostSourceCatalog, telemetry.CostSourceLegacyUnknown:
			cleanSources = appendStringSet(cleanSources, source)
		}
	}
	cleanVersions := []string{}
	for _, version := range versions {
		version = strings.TrimSpace(version)
		if len(version) == 12 && version == strings.ToLower(version) {
			if _, err := hex.DecodeString(version); err == nil {
				cleanVersions = appendStringSet(cleanVersions, version)
			}
		}
	}
	if len(cleanVersions) > maxCatalogVersionsPerUsage {
		cleanVersions = nil
	}
	hasCatalog := containsString(cleanSources, telemetry.CostSourceCatalog)
	if !hasCatalog {
		cleanVersions = nil
	} else if len(cleanVersions) == 0 {
		cleanSources = removeString(cleanSources, telemetry.CostSourceCatalog)
		cleanSources = appendStringSet(cleanSources, telemetry.CostSourceLegacyUnknown)
	}
	if len(cleanSources) == 0 {
		cleanSources = []string{telemetry.CostSourceLegacyUnknown}
	}
	changed := !equalStrings(originalSources, cleanSources) || !equalStrings(originalVersions, cleanVersions)
	return cleanSources, cleanVersions, changed
}

func appendStringSet(values []string, candidates ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(candidates))
	for _, value := range append(append([]string(nil), values...), candidates...) {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func appendUniqueWarning(values []string, warning string) []string {
	if containsString(values, warning) {
		return values
	}
	return append(values, warning)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func removeString(values []string, target string) []string {
	out := values[:0]
	for _, value := range values {
		if value != target {
			out = append(out, value)
		}
	}
	return out
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func requireJSONEOF(dec *json.Decoder) error {
	var trailing any
	if err := dec.Decode(&trailing); err == nil {
		return fmt.Errorf("unexpected trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

type credentialParser func([]byte) ([]oauth.ParsedImportCredential, error)
type externalCredentialAdapter struct {
	id    string
	parse credentialParser
}

func (a externalCredentialAdapter) ID() string { return a.id }
func (a externalCredentialAdapter) Detect(inputs []Input) Confidence {
	if len(inputs) == 0 {
		return ConfidenceNone
	}
	for _, input := range inputs {
		entries, err := a.parse(input.Data)
		if err != nil || len(entries) == 0 {
			return ConfidenceNone
		}
	}
	return ConfidenceCertain
}
func (a externalCredentialAdapter) Decode(inputs []Input, _ DecodeOptions) (*Dataset, *Report, error) {
	dataset := emptyDataset()
	report := &Report{Format: a.id}
	for _, input := range inputs {
		entries, err := a.parse(input.Data)
		if err != nil {
			return nil, nil, fmt.Errorf("decode %s file %s: %w", a.id, input.Name, err)
		}
		for _, entry := range entries {
			if entry.Err != nil {
				report.Warnings = append(report.Warnings, importEntryLabel(input.Name, entry.Entry)+": "+entry.Err.Error())
				continue
			}
			if entry.Credential != nil {
				dataset.Data.Credentials = append(dataset.Data.Credentials, credentialFromOAuth(*entry.Credential))
			}
		}
	}
	if len(dataset.Data.Credentials) == 0 {
		return nil, nil, fmt.Errorf("%s import contains no usable credentials", a.id)
	}
	return dataset, report, nil
}

func parseCLIProxyAPI(data []byte) ([]oauth.ParsedImportCredential, error) {
	cred, err := oauth.ParseCLIProxyAPICredential(data)
	if err != nil {
		return nil, err
	}
	return []oauth.ParsedImportCredential{{Credential: cred}}, nil
}
func parseCodexAuth(data []byte) ([]oauth.ParsedImportCredential, error) {
	cred, err := oauth.ParseCodexNativeAuthCredential(data)
	if err != nil {
		return nil, err
	}
	return []oauth.ParsedImportCredential{{Credential: cred}}, nil
}
func parseSub2API(data []byte) ([]oauth.ParsedImportCredential, error) {
	return oauth.ParseSub2APIExportEntries(data)
}

func importEntryLabel(file, entry string) string {
	if strings.TrimSpace(entry) == "" {
		return file
	}
	return file + "#" + entry
}

func planID(dataset *Dataset, format string, mode Mode, baseState ...string) string {
	// The plan identity covers only behavior-affecting imported data. Adapter
	// metadata such as detection time and producer version must not make a
	// preview impossible to apply on a subsequent request.
	data, _ := json.Marshal(dataset.Data)
	payload := append(append(data, format...), string(mode)...)
	if len(baseState) > 0 {
		payload = append(payload, baseState[0]...)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:8])
}

func cloneImportPlan(plan *ImportPlan) (*ImportPlan, error) {
	data, err := json.Marshal(plan.Dataset)
	if err != nil {
		return nil, fmt.Errorf("clone import plan: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var dataset Dataset
	if err := decoder.Decode(&dataset); err != nil {
		return nil, fmt.Errorf("clone import plan: %w", err)
	}
	out := *plan
	out.Dataset = &dataset
	out.Warnings = append([]string(nil), plan.Warnings...)
	return &out, nil
}
