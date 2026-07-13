package oauth

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lansespirit/Clipal/internal/config"
)

type Store struct {
	rootDir            string
	bypassTransferLock bool
}

type storedCredential struct {
	path string
	cred Credential
}

const (
	credentialFileSeparator  = "--"
	maxCredentialFileNameLen = 255
)

func NewStore(configDir string) *Store {
	return &Store{rootDir: strings.TrimSpace(configDir)}
}

func (s *Store) Save(cred *Credential) error {
	if s == nil {
		return fmt.Errorf("store is nil")
	}
	if cred == nil {
		return fmt.Errorf("credential is nil")
	}
	defer s.lockShared()()
	provider := normalizeProvider(cred.Provider)
	ref := strings.TrimSpace(cred.Ref)
	if provider == "" {
		return fmt.Errorf("provider is required")
	}
	if ref == "" {
		return fmt.Errorf("ref is required")
	}
	toSave := cred.Clone()
	toSave.Provider = provider
	toSave.Ref = ref
	existing, existingPath, entries, err := s.findMatchingAccount(toSave)
	if err != nil {
		return err
	}
	if existing != nil {
		toSave = mergeCredentialForUpdate(existing, toSave)
	}
	if existing == nil && s.hasConflictingRef(entries, toSave.Ref) {
		toSave.Ref = s.disambiguateRef(entries, toSave)
	}

	data, err := json.MarshalIndent(toSave, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credential: %w", err)
	}
	data = append(data, '\n')
	targetPath := s.preferredPath(provider, toSave.Email, toSave.Ref)
	if err := atomicWriteFile(targetPath, data, 0o600); err != nil {
		return err
	}
	if existingPath != "" && existingPath != targetPath {
		// #nosec G703 -- existingPath is discovered from Clipal-managed oauth provider directories.
		if err := os.Remove(existingPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	*cred = *toSave.Clone()
	return nil
}

func (s *Store) findMatchingAccount(cred *Credential) (*Credential, string, []storedCredential, error) {
	if s == nil || cred == nil {
		return nil, "", nil, nil
	}
	entries, err := s.scanCredentials(cred.Provider)
	if err != nil {
		return nil, "", nil, err
	}

	bestIndex := -1
	bestScore := credentialMatchNone
	bestKey := ""
	ambiguousWeakMatch := false
	for i := range entries {
		score := credentialUpdateMatchScore(&entries[i].cred, cred)
		if score == credentialMatchNone {
			continue
		}
		if bestIndex == -1 || score > bestScore || (score == bestScore && storedCredentialLess(entries[bestIndex], entries[i])) {
			bestIndex = i
			bestScore = score
			bestKey = canonicalAccountIdentityKey(&entries[i].cred)
			ambiguousWeakMatch = false
			continue
		}
		if score == bestScore && isWeakIdentityOnlyMatch(score) {
			candidateKey := canonicalAccountIdentityKey(&entries[i].cred)
			if bestKey == "" || candidateKey == "" || candidateKey != bestKey {
				ambiguousWeakMatch = true
			}
		}
	}
	if bestIndex >= 0 {
		if ambiguousWeakMatch && isWeakIdentityOnlyMatch(bestScore) {
			return nil, "", entries, nil
		}
		return entries[bestIndex].cred.Clone(), entries[bestIndex].path, entries, nil
	}
	return nil, "", entries, nil
}

func isWeakIdentityOnlyMatch(score int) bool {
	return score == int(accountIdentityWeakMatch)*credentialMatchStep
}

func (s *Store) Load(provider config.OAuthProvider, ref string) (*Credential, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	defer s.lockShared()()
	path, err := s.resolvePath(provider, ref)
	if err != nil {
		return nil, err
	}
	return s.loadFile(path)
}

func (s *Store) List(provider config.OAuthProvider) ([]Credential, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	defer s.lockShared()()
	entries, err := s.scanCredentials(provider)
	if err != nil {
		return nil, err
	}
	out := make([]Credential, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.cred)
	}
	return out, nil
}

func (s *Store) Delete(provider config.OAuthProvider, ref string) error {
	if s == nil {
		return fmt.Errorf("store is nil")
	}
	defer s.lockShared()()
	path, err := s.resolvePath(provider, ref)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Store) DeleteWithRollback(provider config.OAuthProvider, ref string) (func() error, error) {
	if s == nil {
		return nil, fmt.Errorf("store is nil")
	}
	defer s.lockShared()()
	path, err := s.resolvePath(provider, ref)
	if os.IsNotExist(err) {
		return func() error { return nil }, nil
	}
	if err != nil {
		return nil, err
	}
	backup, err := snapshotFile(path)
	if err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return func() error {
		defer s.lockShared()()
		return backup.restore()
	}, nil
}

func (s *Store) preferredPath(provider config.OAuthProvider, email string, ref string) string {
	return filepath.Join(s.dir(provider), credentialFileName(email, ref))
}

func (s *Store) dir(provider config.OAuthProvider) string {
	return filepath.Join(s.rootDir, "oauth", string(normalizeProvider(provider)))
}

func (s *Store) resolvePath(provider config.OAuthProvider, ref string) (string, error) {
	provider = normalizeProvider(provider)
	ref = strings.TrimSpace(ref)
	if provider == "" || ref == "" {
		return "", os.ErrNotExist
	}

	dir := s.dir(provider)
	if path, err := s.resolveReadablePath(dir, provider, ref); err == nil {
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	return "", os.ErrNotExist
}

func (s *Store) resolveReadablePath(dir string, provider config.OAuthProvider, ref string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", os.ErrNotExist
		}
		return "", err
	}

	suffix := credentialFileSeparator + sanitizeCredentialFileComponent(ref) + ".json"
	var firstErr error
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		cred, err := s.loadFile(path)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if cred.Provider == provider && cred.Ref == ref {
			return path, nil
		}
	}
	if firstErr != nil {
		return "", firstErr
	}
	return "", os.ErrNotExist
}

func (s *Store) loadFile(path string) (*Credential, error) {
	// #nosec G703 -- path is derived from Clipal-managed oauth provider directories.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cred Credential
	if err := json.Unmarshal(data, &cred); err != nil {
		return nil, fmt.Errorf("unmarshal credential: %w", err)
	}
	cred.Provider = normalizeProvider(cred.Provider)
	cred.Ref = strings.TrimSpace(cred.Ref)
	return cred.Clone(), nil
}

func (s *Store) scanCredentials(provider config.OAuthProvider) ([]storedCredential, error) {
	provider = normalizeProvider(provider)
	dir := s.dir(provider)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	out := make([]storedCredential, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		cred, err := s.loadFile(path)
		if err != nil {
			continue
		}
		if cred.Provider != provider {
			continue
		}
		out = append(out, storedCredential{
			path: path,
			cred: *cred,
		})
	}
	return out, nil
}

func storedCredentialLess(best storedCredential, candidate storedCredential) bool {
	if candidate.cred.LastRefresh.After(best.cred.LastRefresh) {
		return true
	}
	if best.cred.LastRefresh.After(candidate.cred.LastRefresh) {
		return false
	}
	if candidate.cred.ExpiresAt.After(best.cred.ExpiresAt) {
		return true
	}
	if best.cred.ExpiresAt.After(candidate.cred.ExpiresAt) {
		return false
	}
	if strings.TrimSpace(candidate.cred.Ref) != strings.TrimSpace(best.cred.Ref) {
		return strings.TrimSpace(candidate.cred.Ref) < strings.TrimSpace(best.cred.Ref)
	}
	return filepath.Base(candidate.path) < filepath.Base(best.path)
}

func (s *Store) hasConflictingRef(entries []storedCredential, ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.cred.Ref) == ref {
			return true
		}
	}
	return false
}

func (s *Store) disambiguateRef(entries []storedCredential, cred *Credential) string {
	base := slugify(strings.TrimSpace(cred.Ref))
	if base == "" {
		base = "account"
	}
	for _, suffix := range credentialRefDisambiguators(cred) {
		candidate := slugify(base + "-" + suffix)
		if candidate != "" && !s.hasConflictingRef(entries, candidate) {
			return candidate
		}
	}
	for n := 2; ; n++ {
		candidate := slugify(fmt.Sprintf("%s-%d", base, n))
		if candidate != "" && !s.hasConflictingRef(entries, candidate) {
			return candidate
		}
	}
}

func credentialRefDisambiguators(cred *Credential) []string {
	if cred == nil {
		return nil
	}
	candidates := []string{
		normalizeIdentityString(cred.AccountID),
		normalizeIdentityString(geminiCredentialProjectID(cred)),
		normalizeIdentityString(credentialMetadataValue(cred, "organization_id")),
		normalizeEmailIdentity(cred.Email),
	}
	out := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = slugify(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func credentialFileName(email string, ref string) string {
	email = sanitizeCredentialFileComponent(email)
	if email == "" {
		email = "account"
	}
	ref = sanitizeCredentialFileComponent(ref)
	if ref == "" {
		ref = "credential"
	}
	maxEmailLen := maxCredentialFileNameLen - len(credentialFileSeparator) - len(ref) - len(".json")
	if maxEmailLen < 1 {
		maxEmailLen = 1
	}
	if len(email) > maxEmailLen {
		email = email[:maxEmailLen]
	}
	email = strings.Trim(email, ". ")
	if email == "" {
		email = "account"
	}
	return email + credentialFileSeparator + ref + ".json"
}

func sanitizeCredentialFileComponent(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range v {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			_ = b.WriteByte('_')
		case r < 0x20 || r == 0x7f:
			_ = b.WriteByte('_')
		default:
			_, _ = b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

type fileSnapshot struct {
	path    string
	data    []byte
	existed bool
	perm    fs.FileMode
}

func snapshotFile(path string) (fileSnapshot, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileSnapshot{path: path, perm: 0o600}, nil
		}
		return fileSnapshot{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{
		path:    path,
		data:    data,
		existed: true,
		perm:    fi.Mode().Perm(),
	}, nil
}

func (s fileSnapshot) restore() error {
	if !s.existed {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return atomicWriteFile(s.path, s.data, s.perm)
}

func atomicWriteFile(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	// #nosec G703 -- dir is derived from Clipal-managed oauth provider directories.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, ".clipal-oauth-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	success := false
	defer func() {
		_ = f.Close()
		if !success {
			// #nosec G703 -- tmp is created by os.CreateTemp under Clipal-managed oauth storage.
			_ = os.Remove(tmp)
		}
	}()

	if err := f.Chmod(perm); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// #nosec G703 -- tmp and path are created under Clipal-managed oauth provider directories.
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	success = true
	return nil
}
