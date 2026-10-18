package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/PAIArtCom/Clipal/internal/config"
)

var takeoverMu sync.Mutex

type takeoverFile struct {
	backupFile
	Planned     []byte
	Remove      bool
	PlannedMode os.FileMode
}

func (m Manager) planTakeover(product ProductID, cfg *config.Config) ([]takeoverFile, error) {
	switch product {
	case ProductCodexCLI:
		return m.planCodex(cfg)
	case ProductClaudeCode:
		return m.planClaude(cfg)
	case ProductAntigravityCLI:
		return m.planAntigravity(cfg)
	case ProductOpenCode:
		path, err := m.opencodeTargetPath()
		if err != nil {
			return nil, err
		}
		return planSingleFile(path, func() (Preview, error) { return m.previewOpenCode(cfg) })
	case ProductContinue:
		path, err := m.continueTargetPath()
		if err != nil {
			return nil, err
		}
		return planSingleFile(path, func() (Preview, error) { return m.previewContinue(cfg) })
	case ProductAider:
		path, err := m.aiderTargetPath()
		if err != nil {
			return nil, err
		}
		return planSingleFile(path, func() (Preview, error) { return m.previewAider(cfg) })
	case ProductGoose:
		path, err := m.gooseTargetPath()
		if err != nil {
			return nil, err
		}
		return planSingleFile(path, func() (Preview, error) { return m.previewGoose(cfg) })
	}
	return nil, fmt.Errorf("unsupported takeover %s", product)
}

func planSingleFile(path string, preview func() (Preview, error)) ([]takeoverFile, error) {
	file, err := readBackupFile(path)
	if err != nil {
		return nil, err
	}
	planned, err := preview()
	if err != nil {
		return nil, err
	}
	return []takeoverFile{{backupFile: file, Planned: []byte(planned.PlannedContent)}}, nil
}

func previewTakeoverFiles(product ProductID, files []takeoverFile) Preview {
	var before, after []backupFile
	for _, file := range files {
		before = append(before, file.backupFile)
		next := file.backupFile
		next.Original = file.Planned
		after = append(after, next)
	}
	if len(files) == 1 {
		return Preview{Product: product, CurrentContent: string(files[0].Original), PlannedContent: string(files[0].Planned)}
	}
	return Preview{Product: product, CurrentContent: formatTakeoverFiles(before), PlannedContent: formatTakeoverFiles(after)}
}

// Stage every file before changing any destination. A failed commit restores all
// destinations already replaced. This is a multi-file transaction, rather than
// the single-file atomic writes used by other packages.
func (m Manager) writeTakeoverFiles(files []takeoverFile, beforeCommit func() error) error {
	staged := make([]string, len(files))
	defer func() {
		for _, path := range staged {
			if path != "" {
				_ = os.Remove(path)
			}
		}
	}()
	for i, file := range files {
		current, err := readBackupFile(file.TargetPath)
		if err != nil {
			return err
		}
		if current.TargetPath != file.TargetPath || current.Existed != file.Existed || current.Mode != file.Mode || !bytes.Equal(current.Original, file.Original) {
			return fmt.Errorf("%s changed after preview; retry", file.TargetPath)
		}
		if file.Remove {
			continue
		}
		if err = os.MkdirAll(filepath.Dir(file.TargetPath), 0700); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(file.TargetPath), ".clipal-takeover-*")
		if err != nil {
			return err
		}
		staged[i] = tmp.Name()
		mode := file.PlannedMode
		if mode == 0 {
			mode = file.Mode
		}
		if mode == 0 {
			mode = 0600
		}
		err = tmp.Chmod(mode)
		if err == nil {
			_, err = tmp.Write(file.Planned)
		}
		if err == nil {
			err = tmp.Sync()
		}
		err = errors.Join(err, tmp.Close())
		if err != nil {
			return err
		}
	}
	if beforeCommit != nil {
		if err := beforeCommit(); err != nil {
			return err
		}
	}
	for i, file := range files {
		var err error
		if m.beforeTakeoverWrite != nil {
			err = m.beforeTakeoverWrite(i, file.TargetPath)
		}
		if err == nil {
			current, e := readBackupFile(file.TargetPath)
			if e != nil {
				err = e
			} else if current.TargetPath != file.TargetPath || current.Existed != file.Existed || current.Mode != file.Mode || !bytes.Equal(current.Original, file.Original) {
				err = fmt.Errorf("%s changed during takeover; retry", file.TargetPath)
			}
		}
		if err == nil {
			if file.Remove {
				err = os.Remove(file.TargetPath)
				if os.IsNotExist(err) {
					err = nil
				}
			} else {
				err = os.Rename(staged[i], file.TargetPath)
			}
		}
		if err != nil {
			var recovery error
			for j := i - 1; j >= 0; j-- {
				recovery = errors.Join(recovery, restoreBackupFile(files[j].backupFile))
			}
			return &takeoverWriteError{cause: fmt.Errorf("write %s: %w", file.TargetPath, err), recovery: recovery}
		}
	}
	return nil
}

func (m Manager) loadTakeoverCycle(product ProductID) (backupSnapshot, error) {
	data, err := os.ReadFile(filepath.Join(m.backupProductRoot(product), "cycle.json"))
	if err != nil {
		return backupSnapshot{}, err
	}
	var snap backupSnapshot
	if err = json.Unmarshal(data, &snap); err != nil {
		return snap, err
	}
	if snap.Product != product {
		return snap, fmt.Errorf("invalid takeover cycle product")
	}
	// Originals belong to the immutable archive, not to a mutable latest pointer.
	original, err := os.ReadFile(filepath.Join(snap.BackupDir, "original.bin"))
	if err != nil {
		return snap, err
	}
	snap.Original = original
	return snap, nil
}

func snapshotFiles(snap backupSnapshot) []backupFile {
	return append([]backupFile{{TargetPath: snap.TargetPath, Existed: snap.TargetExisted, Original: snap.Original, Mode: snap.Mode}}, snap.AdditionalFiles...)
}

func (m Manager) loadTakeoverBackup(product ProductID) (backupSnapshot, error) {
	snap, err := m.loadTakeoverCycle(product)
	if err == nil {
		return snap, nil
	}
	if !os.IsNotExist(err) {
		return snap, err
	}
	snap, err = m.loadLatestBackup(product)
	if err != nil {
		return snap, err
	}
	if product == ProductClaudeCode && len(snap.AdditionalFiles) == 0 {
		// Older Claude backups stored the onboarding file separately.
		var meta claudeHomeConfigBackup
		data, e := os.ReadFile(filepath.Join(snap.BackupDir, claudeHomeConfigBackupMetaName))
		if e == nil {
			if e = json.Unmarshal(data, &meta); e != nil {
				return snap, e
			}
			original, e := os.ReadFile(filepath.Join(snap.BackupDir, claudeHomeConfigBackupBodyName))
			if e != nil {
				return snap, e
			}
			snap.AdditionalFiles = append(snap.AdditionalFiles, backupFile{TargetPath: meta.TargetPath, Existed: meta.Existed, Original: original})
		} else if !os.IsNotExist(e) {
			return snap, e
		}
	}
	return snap, nil
}

func (m Manager) applyTakeover(product ProductID, cfg *config.Config) (Result, error) {
	files, err := m.planTakeover(product, cfg)
	if err != nil {
		return Result{}, err
	}
	status, err := m.takeoverStatus(product, cfg)
	if err != nil {
		return Result{}, err
	}
	if product == ProductOpenCode && status.Verification == "blocked" {
		return Result{}, fmt.Errorf("resolve the OpenCode provider restriction before applying: %s", status.Warning)
	}
	if status.State == StateConfigured {
		return Result{Product: product, Status: status, Message: "already configured"}, nil
	}
	snap, cycleErr := m.loadTakeoverCycle(product)
	newCycle := os.IsNotExist(cycleErr)
	if cycleErr != nil && !newCycle {
		return Result{}, cycleErr
	}
	if !newCycle {
		baseline := snapshotFiles(snap)
		if len(baseline) != len(files) {
			return Result{}, fmt.Errorf("restore the active takeover before changing its files")
		}
		for i, file := range files {
			if baseline[i].TargetPath != file.TargetPath {
				return Result{}, fmt.Errorf("restore the active takeover before changing its target")
			}
		}
	}
	// Adopt legacy baselines, extending them only for newly managed files.
	var legacyBaseline *backupSnapshot
	if newCycle && m.legacyTakeoverManaged(product, files) {
		legacy, e := m.loadTakeoverBackup(product)
		if e == nil {
			paths := map[string]bool{}
			for _, file := range files {
				paths[file.TargetPath] = true
			}
			for _, baseline := range snapshotFiles(legacy) {
				if !paths[baseline.TargetPath] {
					return Result{}, fmt.Errorf("restore the legacy takeover before changing its files")
				}
			}
			legacyBaseline = &legacy
		} else if !os.IsNotExist(e) {
			return Result{}, e
		}
	}

	root := m.backupProductRoot(product)
	var metadata []backupFile
	for _, name := range []string{"latest.json", "latest-original.bin", "cycle.json"} {
		file, e := readBackupFile(filepath.Join(root, name))
		if e != nil {
			return Result{}, e
		}
		metadata = append(metadata, file)
	}
	err = m.writeTakeoverFiles(files, func() error {
		if newCycle {
			paths := make([]string, 0, len(files)-1)
			for _, file := range files[1:] {
				paths = append(paths, file.TargetPath)
			}
			var e error
			snap, e = m.createBackup(product, files[0].TargetPath, paths...)
			if e != nil {
				return e
			}
			if legacyBaseline != nil {
				old := map[string]backupFile{}
				for _, file := range snapshotFiles(*legacyBaseline) {
					old[file.TargetPath] = file
				}
				if baseline, ok := old[snap.TargetPath]; ok {
					snap.Original = baseline.Original
					snap.TargetExisted = baseline.Existed
					snap.Mode = baseline.Mode
				}
				for i, file := range snap.AdditionalFiles {
					if baseline, ok := old[file.TargetPath]; ok {
						snap.AdditionalFiles[i] = baseline
					}
				}
				if e = writeSnapshotFiles(root, snap); e != nil {
					return e
				}
			}
		}
		body, e := json.MarshalIndent(snap, "", "  ")
		if e != nil {
			return e
		}
		return os.WriteFile(filepath.Join(root, "cycle.json"), body, 0600)
	})
	if err != nil {
		var failure *takeoverWriteError
		if errors.As(err, &failure) && failure.recovery != nil {
			return Result{}, fmt.Errorf("%w; active backup retained for recovery", err)
		}
		for _, file := range metadata {
			err = errors.Join(err, restoreBackupFile(file))
		}
		return Result{}, err
	}
	updated, err := m.takeoverStatus(product, cfg)
	if err != nil {
		// The transaction committed; an inspection failure must not report that
		// the write failed or hide the baseline needed to restore it.
		status.State = StateError
		status.BackupAvailable = true
		status.Warning = "Configuration written; inspection: " + err.Error()
	} else {
		status = updated
	}
	return Result{Product: product, Status: status, Message: "Configuration written; restart the client or open a new terminal. Runtime routing has not been verified."}, nil
}

func (m Manager) legacyTakeoverManaged(product ProductID, files []takeoverFile) bool {
	switch product {
	case ProductCodexCLI:
		body, err := readTOMLMap(files[0].TargetPath)
		return err == nil && body["model_provider"] == "clipal"
	case ProductClaudeCode:
		body, err := readJSONMap(files[0].TargetPath)
		env, _ := body["env"].(map[string]any)
		return err == nil && strings.HasSuffix(claudeEnvValue(env, "ANTHROPIC_BASE_URL"), "/clipal")
	case ProductOpenCode:
		doc, err := readJSONCMap(files[0].TargetPath)
		if err != nil {
			return false
		}
		model, _ := doc["model"].(string)
		return strings.HasPrefix(model, "clipal/")
	case ProductContinue:
		doc, err := readYAMLMap(files[0].TargetPath)
		return err == nil && findContinueClipalModel(doc) != nil
	case ProductAider:
		doc, err := readYAMLMap(files[0].TargetPath)
		base, _ := doc["openai-api-base"].(string)
		return err == nil && strings.HasSuffix(base, "/clipal")
	case ProductGoose:
		doc, err := readJSONMap(files[0].TargetPath)
		base, _ := doc["base_url"].(string)
		return err == nil && strings.HasSuffix(base, "/clipal/v1/chat/completions")
	case ProductAntigravityCLI:
		for _, file := range files[1:] {
			if strings.Contains(string(file.Original), antigravityBlockStart) {
				return true
			}
		}
	}
	return false
}

func (m Manager) rollbackTakeover(product ProductID, cfg *config.Config) (Result, error) {
	snap, err := m.loadTakeoverBackup(product)
	if err != nil {
		return Result{}, err
	}
	var files []takeoverFile
	for _, baseline := range snapshotFiles(snap) {
		current, e := readBackupFile(baseline.TargetPath)
		if e != nil {
			return Result{}, e
		}
		if current.TargetPath != baseline.TargetPath {
			return Result{}, fmt.Errorf("rollback target %s now points elsewhere", baseline.TargetPath)
		}
		file := takeoverFile{backupFile: current, Planned: baseline.Original, Remove: !baseline.Existed}
		file.PlannedMode = baseline.Mode
		files = append(files, file)
	}
	// Ending a cycle is part of the same transaction, so failed restoration never
	// discards the only active baseline. Archives remain available for recovery.
	for _, name := range []string{"cycle.json", "latest.json", "latest-original.bin"} {
		current, e := readBackupFile(filepath.Join(m.backupProductRoot(product), name))
		if e != nil {
			return Result{}, e
		}
		files = append(files, takeoverFile{backupFile: current, Remove: true})
	}
	if err = m.writeTakeoverFiles(files, nil); err != nil {
		return Result{}, err
	}
	status, err := m.takeoverStatus(product, cfg)
	if err != nil {
		status = Status{Product: product, State: StateNotConfigured, TargetPath: snap.TargetPath, Warning: "Original files restored; inspection: " + err.Error()}
	}
	return Result{Product: product, Status: status, Message: "rollback completed"}, nil
}

// A failed recovery must retain its active baseline instead of discarding the
// only route back to the original files.
type takeoverWriteError struct {
	cause    error
	recovery error
}

func (e *takeoverWriteError) Error() string { return errors.Join(e.cause, e.recovery).Error() }
func (e *takeoverWriteError) Unwrap() error { return errors.Join(e.cause, e.recovery) }
