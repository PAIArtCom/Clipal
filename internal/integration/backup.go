package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (m Manager) createBackup(product ProductID, targetPath string, additionalPaths ...string) (backupSnapshot, error) {
	now := time.Now().UTC()
	root := m.backupProductRoot(product)
	dir := filepath.Join(root, now.Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return backupSnapshot{}, fmt.Errorf("create backup dir: %w", err)
	}

	snap := backupSnapshot{
		Product:    product,
		TargetPath: targetPath,
		CreatedAt:  now,
		BackupDir:  dir,
	}

	original, err := os.ReadFile(targetPath)
	if err == nil {
		snap.TargetExisted = true
		snap.Original = original
	} else if !os.IsNotExist(err) {
		return backupSnapshot{}, fmt.Errorf("read target for backup: %w", err)
	}

	for _, path := range additionalPaths {
		file, err := readBackupFile(path)
		if err != nil {
			return backupSnapshot{}, err
		}
		snap.AdditionalFiles = append(snap.AdditionalFiles, file)
	}
	if err := writeSnapshotFiles(root, snap); err != nil {
		return backupSnapshot{}, err
	}
	return snap, nil
}

func (m Manager) loadLatestBackup(product ProductID) (backupSnapshot, error) {
	root := m.backupProductRoot(product)
	metaPath := filepath.Join(root, "latest.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return backupSnapshot{}, err
	}

	var snap backupSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return backupSnapshot{}, fmt.Errorf("decode latest backup metadata: %w", err)
	}

	originalPath := filepath.Join(root, "latest-original.bin")
	original, err := os.ReadFile(originalPath)
	if err == nil {
		snap.Original = original
	} else if !os.IsNotExist(err) {
		return backupSnapshot{}, fmt.Errorf("read latest backup original: %w", err)
	}
	return snap, nil
}

func (m Manager) hasLatestBackup(product ProductID) bool {
	_, err := os.Stat(filepath.Join(m.backupProductRoot(product), "latest.json"))
	return err == nil
}

func writeSnapshotFiles(root string, snap backupSnapshot) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create backup root: %w", err)
	}

	meta := snap
	meta.Original = nil
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal backup metadata: %w", err)
	}
	metaData = append(metaData, '\n')

	if err := os.WriteFile(filepath.Join(snap.BackupDir, "metadata.json"), metaData, 0o600); err != nil {
		return fmt.Errorf("write backup metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(snap.BackupDir, "original.bin"), snap.Original, 0o600); err != nil {
		return fmt.Errorf("write backup original: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, "latest.json"), metaData, 0o600); err != nil {
		return fmt.Errorf("write latest backup metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, "latest-original.bin"), snap.Original, 0o600); err != nil {
		return fmt.Errorf("write latest backup original: %w", err)
	}
	return nil
}

func readBackupFile(path string) (backupFile, error) {
	file := backupFile{TargetPath: path}
	raw, err := os.ReadFile(path)
	if err == nil {
		file.Existed = true
		file.Original = raw
	} else if !os.IsNotExist(err) {
		return backupFile{}, fmt.Errorf("read %s: %w", path, err)
	}
	return file, nil
}

func restoreBackupFile(snap backupFile) error {
	if snap.Existed {
		if err := os.MkdirAll(filepath.Dir(snap.TargetPath), 0o755); err != nil {
			return fmt.Errorf("prepare rollback target: %w", err)
		}
		if err := os.WriteFile(snap.TargetPath, snap.Original, 0o600); err != nil {
			return fmt.Errorf("restore backup: %w", err)
		}
		return nil
	}
	if err := os.Remove(snap.TargetPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove target during rollback: %w", err)
	}
	return nil
}

func restoreBackupSnapshot(snap backupSnapshot) error {
	if err := restoreBackupFile(backupFile{TargetPath: snap.TargetPath, Existed: snap.TargetExisted, Original: snap.Original}); err != nil {
		return err
	}
	for _, file := range snap.AdditionalFiles {
		if err := restoreBackupFile(file); err != nil {
			return err
		}
	}
	return nil
}
