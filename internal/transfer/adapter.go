package transfer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/lansespirit/Clipal/internal/oauth"
)

const (
	FormatAuto        = "auto"
	FormatClipal      = "clipal-v1"
	FormatCLIProxyAPI = "cliproxyapi"
	FormatSub2API     = "sub2api"
	FormatCodexAuth   = "codex-auth"
	FormatMixed       = "mixed"
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
	var dataset Dataset
	if err := dec.Decode(&dataset); err != nil {
		return nil, nil, fmt.Errorf("decode %s: %w", FormatClipal, err)
	}
	if err := requireJSONEOF(dec); err != nil {
		return nil, nil, fmt.Errorf("decode %s: %w", FormatClipal, err)
	}
	if err := validateDataset(&dataset); err != nil {
		return nil, nil, err
	}
	return &dataset, &Report{Format: FormatClipal}, nil
}
func (nativeAdapter) Encode(dataset *Dataset, _ EncodeOptions) ([]byte, error) {
	if err := validateDataset(dataset); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(dataset, "", "  ")
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
	if len(dataset.Data.Clients) != 3 {
		for client := range dataset.Data.Clients {
			switch client {
			case "claude", "openai", "gemini":
			default:
				return fmt.Errorf("data.clients.%s is not supported by schema version %d", client, SchemaVersion)
			}
		}
	}
	for _, client := range []string{"claude", "openai", "gemini"} {
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
		return fmt.Errorf("data.usage.clients is required")
	}
	return nil
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

func planID(dataset *Dataset, format string, mode Mode) string {
	// The plan identity covers only behavior-affecting imported data. Adapter
	// metadata such as detection time and producer version must not make a
	// preview impossible to apply on a subsequent request.
	data, _ := json.Marshal(dataset.Data)
	sum := sha256.Sum256(append(append(data, format...), string(mode)...))
	return hex.EncodeToString(sum[:8])
}
