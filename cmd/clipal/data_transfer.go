package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/transfer"
)

const maxCLIImportFileBytes = transfer.MaxImportFileBytes

func runDataExport(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("clipal export", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() { printDataExportUsage(stdout) }
	configDir := fs.String("config-dir", "", "configuration directory")
	output := fs.String("o", "clipal-data.json", "output path, or - for stdout")
	fs.StringVar(output, "output", "clipal-data.json", "output path, or - for stdout")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	dir := strings.TrimSpace(*configDir)
	if dir == "" {
		dir = config.GetConfigDir()
	}
	var data []byte
	endpoint, running, probeErr := runningTransferEndpoint(dir)
	if probeErr != nil {
		return probeErr
	}
	if running {
		var err error
		data, err = callDataExportAPI(endpoint + "/api/data/export")
		if err != nil {
			return fmt.Errorf("export through running Clipal instance: %w", err)
		}
	} else {
		service, err := transfer.NewService(dir, version, nil, nil)
		if err != nil {
			return err
		}
		data, err = service.ExportJSON()
		if err != nil {
			return err
		}
	}
	if *output == "-" {
		_, err := stdout.Write(data)
		return err
	}
	if err := writeExportFile(*output, data); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Exported clipal.data/v1 to %s\n", *output)
	return nil
}

func runDataImport(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("clipal import", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() { printDataImportUsage(stdout) }
	configDir := fs.String("config-dir", "", "configuration directory")
	format := fs.String("format", transfer.FormatAuto, "input format")
	mode := fs.String("mode", "", "replace or merge")
	dryRun := fs.Bool("dry-run", false, "preview without applying")
	yes := fs.Bool("yes", false, "apply without confirmation")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("at least one input file is required")
	}
	if fs.NArg() > transfer.MaxImportFiles {
		return fmt.Errorf("too many import files: max %d", transfer.MaxImportFiles)
	}
	inputs := make([]transfer.Input, 0, fs.NArg())
	var totalBytes int64
	for _, path := range fs.Args() {
		name := filepath.Base(path)
		if len(name) > transfer.MaxImportFilenameBytes {
			return fmt.Errorf("import file name exceeds %d bytes: %s", transfer.MaxImportFilenameBytes, name)
		}
		data, err := readImportFile(path)
		if err != nil {
			return err
		}
		totalBytes += int64(len(data))
		if totalBytes > transfer.MaxImportTotalBytes {
			return fmt.Errorf("import files exceed %d total bytes", transfer.MaxImportTotalBytes)
		}
		inputs = append(inputs, transfer.Input{Name: name, Data: data})
	}
	dir := strings.TrimSpace(*configDir)
	if dir == "" {
		dir = config.GetConfigDir()
	}
	endpoint, running, probeErr := runningTransferEndpoint(dir)
	if probeErr != nil {
		return probeErr
	}
	if running {
		return runDataImportViaDaemon(endpoint, inputs, *format, transfer.Mode(*mode), *dryRun, *yes, stdin, stdout)
	}
	service, err := transfer.NewService(dir, version, nil, nil)
	if err != nil {
		return err
	}
	plan, err := service.Analyze(inputs, *format, transfer.Mode(*mode))
	if err != nil {
		return err
	}
	if err := writeJSONOutput(stdout, plan); err != nil {
		return err
	}
	if *dryRun {
		return nil
	}
	if err := confirmDataImport(*yes, stdin, stdout); err != nil {
		return err
	}
	result, err := service.Apply(plan)
	if err != nil {
		return err
	}
	return writeJSONOutput(stdout, result)
}

type dataImportAPIFile struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

type dataImportAPIRequest struct {
	Files  []dataImportAPIFile `json:"files"`
	Format string              `json:"format,omitempty"`
	Mode   transfer.Mode       `json:"mode,omitempty"`
	PlanID string              `json:"plan_id,omitempty"`
}

func runDataImportViaDaemon(endpoint string, inputs []transfer.Input, format string, mode transfer.Mode, dryRun, yes bool, stdin io.Reader, stdout io.Writer) error {
	req := dataImportAPIRequest{Format: format, Mode: mode, Files: make([]dataImportAPIFile, 0, len(inputs))}
	for _, input := range inputs {
		req.Files = append(req.Files, dataImportAPIFile{Name: input.Name, Data: string(input.Data)})
	}
	var plan transfer.ImportPlan
	if err := callDataImportAPI(endpoint+"/api/data/import/preview", req, &plan); err != nil {
		return fmt.Errorf("preview through running Clipal instance: %w", err)
	}
	if err := writeJSONOutput(stdout, &plan); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	if err := confirmDataImport(yes, stdin, stdout); err != nil {
		return err
	}
	req.PlanID = plan.ID
	var result transfer.ApplyResult
	if err := callDataImportAPI(endpoint+"/api/data/import/apply", req, &result); err != nil {
		return fmt.Errorf("apply through running Clipal instance: %w", err)
	}
	return writeJSONOutput(stdout, &result)
}

func confirmDataImport(yes bool, stdin io.Reader, stdout io.Writer) error {
	if yes {
		return nil
	}
	fmt.Fprint(stdout, "Apply this import plan? [y/N] ")
	answer, _ := bufio.NewReader(stdin).ReadString('\n')
	if normalized := strings.ToLower(strings.TrimSpace(answer)); normalized != "y" && normalized != "yes" {
		return errors.New("import cancelled")
	}
	return nil
}

func runningTransferEndpoint(configDir string) (string, bool, error) {
	cfg, err := config.Load(configDir)
	if err != nil {
		return "", false, nil
	}
	host := strings.TrimSpace(cfg.Global.ListenAddr)
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	base := (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(cfg.Global.Port))}).String()
	connection, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(cfg.Global.Port)), 300*time.Millisecond)
	if err != nil {
		return "", false, nil
	}
	_ = connection.Close()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(base + "/api/status")
	if err != nil {
		return "", false, fmt.Errorf("a process is listening on Clipal's configured address but its status could not be verified: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("a process is listening on Clipal's configured address but returned HTTP %d for /api/status", resp.StatusCode)
	}
	var status struct {
		ConfigDir string `json:"config_dir"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return "", false, fmt.Errorf("decode running Clipal status: %w", err)
	}
	if !sameConfigDir(status.ConfigDir, configDir) {
		return "", false, fmt.Errorf("the configured address belongs to a Clipal instance using %q, not %q", status.ConfigDir, configDir)
	}
	return base, true, nil
}

func sameConfigDir(a, b string) bool {
	a, errA := filepath.Abs(strings.TrimSpace(a))
	b, errB := filepath.Abs(strings.TrimSpace(b))
	return errA == nil && errB == nil && filepath.Clean(a) == filepath.Clean(b)
}

func callDataImportAPI(endpoint string, request any, response any) error {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(request); err != nil {
		return err
	}
	if body.Len() > transfer.MaxJSONImportRequestBytes {
		return fmt.Errorf("encoded import request exceeds %d bytes", transfer.MaxJSONImportRequestBytes)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body.Bytes()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Clipal-UI", "1")
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	return json.NewDecoder(resp.Body).Decode(response)
}

func callDataExportAPI(endpoint string) ([]byte, error) {
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Get(endpoint)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCLIImportFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCLIImportFileBytes {
		return nil, fmt.Errorf("export exceeds %d bytes", maxCLIImportFileBytes)
	}
	return data, nil
}

func printDataExportUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: clipal export [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Export a complete clipal.data/v1 backup.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "flags:")
	fmt.Fprintln(w, "  --config-dir string   configuration directory")
	fmt.Fprintln(w, "  -o, --output string   output path, or - for stdout (default clipal-data.json)")
}

func printDataImportUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: clipal import [flags] <file> [file...]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Preview and import Clipal or external credential data.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "flags:")
	fmt.Fprintln(w, "  --config-dir string   configuration directory")
	fmt.Fprintln(w, "  --format string       input format (default auto)")
	fmt.Fprintln(w, "  --mode string         replace or merge (default selected by format)")
	fmt.Fprintln(w, "  --dry-run             preview without applying")
	fmt.Fprintln(w, "  --yes                 apply without confirmation")
}

func readImportFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxCLIImportFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) > maxCLIImportFileBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maxCLIImportFileBytes)
	}
	return data, nil
}

func writeExportFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".clipal-export-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func writeJSONOutput(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
