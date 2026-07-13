package transfer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/oauth"
	"github.com/lansespirit/Clipal/internal/telemetry"
	"gopkg.in/yaml.v3"
)

var ErrImportBaseStateChanged = errors.New("current data changed since preview; preview again")

type Service struct {
	configDir       string
	version         string
	registry        *Registry
	usage           *telemetry.Store
	reload          func() error
	coordinateApply func(func(reload func() error) error) error
	mu              sync.Mutex
	hooks           serviceHooks
}

type ServiceOption func(*Service)

// WithApplyCoordinator lets a runtime serialize configuration reloads around
// the complete import transaction. The coordinator must invoke operation once
// and pass a reload function that is safe to call while its session is held.
func WithApplyCoordinator(coordinator func(operation func(reload func() error) error) error) ServiceOption {
	return func(service *Service) {
		service.coordinateApply = coordinator
	}
}

type serviceHooks struct {
	beforeSaveCredential func(index int) error
	beforeWriteConfig    func(index int, name string) error
	beforeUsageFlush     func() error
}

func NewService(configDir, version string, usage *telemetry.Store, reload func() error, options ...ServiceOption) (*Service, error) {
	if strings.TrimSpace(configDir) == "" {
		return nil, fmt.Errorf("config directory is required")
	}
	if usage == nil {
		var err error
		usage, err = telemetry.NewStore(configDir)
		if err != nil {
			return nil, fmt.Errorf("load usage data: %w", err)
		}
	}
	service := &Service{configDir: configDir, version: version, registry: NewRegistry(), usage: usage, reload: reload}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

func (s *Service) Export() (*Dataset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, err := config.Load(s.configDir)
	if err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}
	credentials, err := s.listCredentials()
	if err != nil {
		return nil, err
	}
	return datasetFromState(cfg, credentials, s.usage.Snapshot(), s.version, time.Now()), nil
}

func (s *Service) ExportJSON() ([]byte, error) {
	dataset, err := s.Export()
	if err != nil {
		return nil, err
	}
	return s.registry.Encode(FormatClipal, dataset)
}

func (s *Service) Analyze(inputs []Input, format string, mode Mode) (*ImportPlan, error) {
	dataset, report, err := s.registry.Decode(format, inputs)
	if err != nil {
		return nil, err
	}
	native := report.Format == FormatClipal
	if native {
		if err := configFromDataset(dataset).Validate(); err != nil {
			return nil, fmt.Errorf("invalid imported configuration: %w", err)
		}
	}
	if err := validateCredentials(dataset, native); err != nil {
		return nil, err
	}
	if mode == "" {
		if native {
			mode = ModeReplace
		} else {
			mode = ModeMerge
		}
	}
	if mode != ModeReplace && mode != ModeMerge {
		return nil, fmt.Errorf("mode must be replace or merge")
	}
	if !native && mode != ModeMerge {
		return nil, fmt.Errorf("%s imports are credentials-only and require merge mode", report.Format)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, credentials, usage, err := s.currentState()
	if err != nil {
		return nil, err
	}
	plan := &ImportPlan{Format: report.Format, Mode: mode, Native: native, Files: len(inputs), Credentials: len(dataset.Data.Credentials), Warnings: append([]string(nil), report.Warnings...), Dataset: dataset}
	for _, client := range dataset.Data.Clients {
		if native {
			plan.Clients++
		}
		plan.Providers += len(client.Providers)
	}
	plan.UsageProviders = usageProviderCount(dataset.Data.Usage)
	plan.Changes = importChanges(current, credentials, usage, dataset, native, mode)
	plan.baseState = importStateFingerprint(current, credentials, usage)
	plan.ID = planID(dataset, plan.Format, plan.Mode, plan.baseState)
	return plan, nil
}

// AnalyzeCredentials creates a standard merge plan for already-decoded OAuth
// credentials. Focused import surfaces use this after applying their own
// selection filters, while Service.Apply remains the only persistence path.
func (s *Service) AnalyzeCredentials(credentials []oauth.Credential, warnings []string, fileCount int) (*ImportPlan, error) {
	dataset := emptyDataset()
	for _, credential := range credentials {
		dataset.Data.Credentials = append(dataset.Data.Credentials, credentialFromOAuth(credential))
	}
	if len(dataset.Data.Credentials) == 0 {
		return nil, fmt.Errorf("no usable credentials selected for import")
	}
	if err := validateCredentials(dataset, false); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, existingCredentials, usage, err := s.currentState()
	if err != nil {
		return nil, err
	}
	plan := &ImportPlan{
		Format:      FormatMixed,
		Mode:        ModeMerge,
		Files:       fileCount,
		Credentials: len(credentials),
		Warnings:    append([]string(nil), warnings...),
		Dataset:     dataset,
	}
	plan.Changes = importChanges(current, existingCredentials, usage, dataset, false, ModeMerge)
	plan.baseState = importStateFingerprint(current, existingCredentials, usage)
	plan.ID = planID(dataset, plan.Format, plan.Mode, plan.baseState)
	return plan, nil
}

func validateCredentials(dataset *Dataset, native bool) error {
	seen := make(map[string]struct{}, len(dataset.Data.Credentials))
	for i, item := range dataset.Data.Credentials {
		cred := item.toOAuth()
		switch cred.Provider {
		case config.OAuthProviderClaude, config.OAuthProviderCodex, config.OAuthProviderGemini, config.OAuthProviderAntigravity:
		default:
			return fmt.Errorf("credential %d has unsupported provider %q", i, item.Provider)
		}
		if cred.Ref == "" {
			return fmt.Errorf("credential %d ref is required", i)
		}
		if cred.AccessToken == "" && cred.RefreshToken == "" {
			return fmt.Errorf("credential %s/%s has no access or refresh token", cred.Provider, cred.Ref)
		}
		key := string(cred.Provider) + "\x00" + cred.Ref
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate credential %s/%s", cred.Provider, cred.Ref)
		}
		seen[key] = struct{}{}
	}
	if native {
		cfg := configFromDataset(dataset)
		for _, client := range []*config.ClientConfig{&cfg.Claude, &cfg.OpenAI, &cfg.Gemini} {
			for _, provider := range client.Providers {
				if !provider.UsesOAuth() {
					continue
				}
				key := string(provider.NormalizedOAuthProvider()) + "\x00" + provider.NormalizedOAuthRef()
				if _, ok := seen[key]; !ok {
					return fmt.Errorf("oauth provider %q references missing credential %s/%s", provider.Name, provider.NormalizedOAuthProvider(), provider.NormalizedOAuthRef())
				}
			}
		}
	}
	return nil
}

func (s *Service) Apply(plan *ImportPlan) (*ApplyResult, error) {
	if plan == nil || plan.Dataset == nil {
		return nil, fmt.Errorf("analyzed import plan is required")
	}
	if plan.ID != planID(plan.Dataset, plan.Format, plan.Mode, plan.baseState) {
		return nil, fmt.Errorf("import plan does not match its dataset")
	}
	var result *ApplyResult
	operation := func(reload func() error) error {
		var err error
		result, err = s.apply(plan, reload)
		return err
	}
	if s.coordinateApply != nil {
		if err := s.coordinateApply(operation); err != nil {
			return nil, err
		}
		return result, nil
	}
	if err := operation(s.reload); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) apply(plan *ImportPlan, reload func() error) (*ApplyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Own the credential store for the whole transaction so concurrent token
	// refreshes cannot land between the oauth snapshot and a later restore.
	releaseStore := oauth.LockStoreForTransfer()
	defer releaseStore()

	current, err := config.Load(s.configDir)
	if err != nil {
		return nil, fmt.Errorf("load current configuration: %w", err)
	}
	store := oauth.NewTransferStore(s.configDir)
	credentials, err := listCredentialsFromStore(store)
	if err != nil {
		return nil, err
	}
	if plan.baseState != "" && plan.baseState != importStateFingerprint(current, credentials, s.usage.Snapshot()) {
		return nil, ErrImportBaseStateChanged
	}
	usageTx, err := s.usage.BeginTransfer()
	if err != nil {
		return nil, fmt.Errorf("start usage transaction: %w", err)
	}
	usageFinished := false
	defer func() {
		if !usageFinished {
			_ = usageTx.Rollback()
		}
	}()
	backupNames := config.WatchedConfigFilenames()
	configBackup, err := snapshotFiles(s.configDir, backupNames)
	if err != nil {
		return nil, fmt.Errorf("snapshot configuration: %w", err)
	}
	oauthBackup, err := snapshotDirectory(filepath.Join(s.configDir, "oauth"))
	if err != nil {
		return nil, fmt.Errorf("snapshot credentials: %w", err)
	}
	preserveOAuthBackup := false
	defer func() {
		if !preserveOAuthBackup {
			oauthBackup.cleanup()
		}
	}()
	rollback := func(base error) error {
		var failures []string
		if err := oauthBackup.restore(); err != nil {
			message := "credentials: " + err.Error()
			if oauthBackup.backup != "" {
				// The live oauth directory may be partial now; keep the only
				// intact copy of the original credentials for manual recovery.
				preserveOAuthBackup = true
				message += fmt.Sprintf(" (original credentials preserved at %s)", oauthBackup.backup)
			}
			failures = append(failures, message)
		}
		if err := usageTx.Rollback(); err != nil {
			failures = append(failures, "usage: "+err.Error())
		}
		usageFinished = true
		// Restore configuration files after stateful stores so the on-disk
		// configuration is the final rollback step visible to reload watchers.
		if err := configBackup.restore(); err != nil {
			failures = append(failures, "configuration/data files: "+err.Error())
		}
		if len(failures) > 0 {
			return fmt.Errorf("%w (rollback failed: %s)", base, strings.Join(failures, "; "))
		}
		return base
	}

	target := current
	if plan.Native {
		incoming := configFromDataset(plan.Dataset)
		if plan.Mode == ModeReplace {
			target = incoming
		} else {
			target = mergeConfig(current, incoming)
		}
		if plan.Mode == ModeReplace {
			if err := clearDirectory(filepath.Join(s.configDir, "oauth")); err != nil {
				return nil, rollback(fmt.Errorf("clear credentials: %w", err))
			}
		}
	}

	saved := 0
	credentialResults := make([]CredentialApplyResult, 0, len(plan.Dataset.Data.Credentials))
	for index, transferCredential := range plan.Dataset.Data.Credentials {
		if s.hooks.beforeSaveCredential != nil {
			if err := s.hooks.beforeSaveCredential(index); err != nil {
				return nil, rollback(fmt.Errorf("save credential: %w", err))
			}
		}
		cred := transferCredential.toOAuth()
		originalRef := cred.Ref
		if err := store.Save(&cred); err != nil {
			return nil, rollback(fmt.Errorf("save %s credential %s: %w", cred.Provider, originalRef, err))
		}
		if target != nil {
			relinkCredentialRef(target, store, cred.Provider, originalRef, &cred)
		}
		if !plan.Native {
			linked, action := linkExternalCredential(target, store, &cred)
			credentialResults = append(credentialResults, CredentialApplyResult{
				Provider: string(cred.Provider), Ref: cred.Ref, Email: cred.Email,
				ProviderName: linked.Name, ProviderAction: action,
			})
		}
		saved++
	}
	if target == nil {
		return nil, rollback(fmt.Errorf("target configuration is unavailable"))
	}
	if err := target.Validate(); err != nil {
		return nil, rollback(fmt.Errorf("validate imported configuration: %w", err))
	}
	if err := writeConfigWithHook(s.configDir, target, s.hooks.beforeWriteConfig); err != nil {
		return nil, rollback(fmt.Errorf("save imported configuration: %w", err))
	}
	if plan.Native {
		usageTx.Restore(plan.Dataset.Data.Usage.toTelemetry(), plan.Mode == ModeMerge)
		if s.hooks.beforeUsageFlush != nil {
			if err := s.hooks.beforeUsageFlush(); err != nil {
				return nil, rollback(fmt.Errorf("save imported usage: %w", err))
			}
		}
		if err := usageTx.Flush(); err != nil {
			return nil, rollback(fmt.Errorf("save imported usage: %w", err))
		}
	}
	if reload != nil {
		if err := reload(); err != nil {
			return nil, rollback(fmt.Errorf("reload runtime configuration: %w", err))
		}
	}
	usageTx.Commit()
	usageFinished = true
	return &ApplyResult{PlanID: plan.ID, Mode: plan.Mode, Providers: plan.Providers, Credentials: saved, UsageProviders: plan.UsageProviders, CredentialResults: credentialResults}, nil
}

func (s *Service) listCredentials() ([]oauth.Credential, error) {
	return listCredentialsFromStore(oauth.NewStore(s.configDir))
}

func (s *Service) currentState() (*config.Config, []oauth.Credential, telemetry.DataSnapshot, error) {
	cfg, err := config.Load(s.configDir)
	if err != nil {
		return nil, nil, telemetry.DataSnapshot{}, fmt.Errorf("load current configuration: %w", err)
	}
	credentials, err := s.listCredentials()
	if err != nil {
		return nil, nil, telemetry.DataSnapshot{}, err
	}
	return cfg, credentials, s.usage.Snapshot(), nil
}

func listCredentialsFromStore(store *oauth.Store) ([]oauth.Credential, error) {
	providers := []config.OAuthProvider{config.OAuthProviderClaude, config.OAuthProviderCodex, config.OAuthProviderGemini, config.OAuthProviderAntigravity}
	var out []oauth.Credential
	for _, provider := range providers {
		items, err := store.List(provider)
		if err != nil {
			return nil, fmt.Errorf("list %s credentials: %w", provider, err)
		}
		out = append(out, items...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Ref < out[j].Ref
	})
	return out, nil
}

func configFromDataset(dataset *Dataset) *config.Config {
	return &config.Config{Global: dataset.Data.Global.toConfig(), Claude: dataset.Data.Clients["claude"].toConfig(), OpenAI: dataset.Data.Clients["openai"].toConfig(), Gemini: dataset.Data.Clients["gemini"].toConfig()}
}

func mergeConfig(current, incoming *config.Config) *config.Config {
	out := *current
	out.Global = incoming.Global
	out.Claude = mergeClient(current.Claude, incoming.Claude)
	out.OpenAI = mergeClient(current.OpenAI, incoming.OpenAI)
	out.Gemini = mergeClient(current.Gemini, incoming.Gemini)
	return &out
}

func mergeClient(current, incoming config.ClientConfig) config.ClientConfig {
	out := incoming
	out.Providers = append([]config.Provider(nil), current.Providers...)
	indices := map[string]int{}
	for i := range out.Providers {
		indices[out.Providers[i].Name] = i
	}
	for _, provider := range incoming.Providers {
		if i, ok := indices[provider.Name]; ok {
			out.Providers[i] = provider
		} else {
			indices[provider.Name] = len(out.Providers)
			out.Providers = append(out.Providers, provider)
		}
	}
	return out
}

// relinkCredentialRef repoints providers at the imported credential when the
// store had to disambiguate its ref, but only providers that belong to the
// same account: a provider already bound to a different identity (or whose
// oldRef still resolves to a different account's credential) keeps its
// binding instead of being rewired to the imported account.
func relinkCredentialRef(cfg *config.Config, store *oauth.Store, provider config.OAuthProvider, oldRef string, cred *oauth.Credential) {
	if cfg == nil || cred == nil {
		return
	}
	identity := oauth.AccountIdentityKey(cred)
	oldRefIdentity := ""
	if cred.Ref != oldRef && store != nil {
		if existing, err := store.Load(provider, oldRef); err == nil && existing != nil {
			oldRefIdentity = oauth.AccountIdentityKey(existing)
		}
	}
	for _, client := range []*config.ClientConfig{&cfg.Claude, &cfg.OpenAI, &cfg.Gemini} {
		for i := range client.Providers {
			p := &client.Providers[i]
			if !p.UsesOAuth() || p.NormalizedOAuthProvider() != provider || p.NormalizedOAuthRef() != oldRef {
				continue
			}
			boundIdentity := p.NormalizedOAuthIdentity()
			if boundIdentity == "" {
				boundIdentity = oldRefIdentity
			}
			if boundIdentity != "" && boundIdentity != identity {
				continue
			}
			p.OAuthRef = cred.Ref
			p.OAuthIdentity = identity
		}
	}
}

func linkExternalCredential(cfg *config.Config, store *oauth.Store, cred *oauth.Credential) (config.Provider, string) {
	if cfg == nil || cred == nil {
		return config.Provider{}, ""
	}
	var client *config.ClientConfig
	switch cred.Provider {
	case config.OAuthProviderCodex:
		client = &cfg.OpenAI
	case config.OAuthProviderClaude:
		client = &cfg.Claude
	case config.OAuthProviderGemini, config.OAuthProviderAntigravity:
		client = &cfg.Gemini
	default:
		return config.Provider{}, ""
	}
	identity := oauth.AccountIdentityKey(cred)
	for i := range client.Providers {
		p := &client.Providers[i]
		if !p.UsesOAuth() || p.NormalizedOAuthProvider() != cred.Provider {
			continue
		}
		if p.NormalizedOAuthRef() == cred.Ref {
			p.OAuthIdentity = identity
			return *p, "reused"
		}
		matchesIdentity := p.NormalizedOAuthIdentity() != "" && p.NormalizedOAuthIdentity() == identity
		if !matchesIdentity && store != nil {
			if existing, err := store.Load(cred.Provider, p.NormalizedOAuthRef()); err == nil {
				matchesIdentity = oauth.AccountIdentityKey(existing) == identity
			}
		}
		if matchesIdentity {
			p.OAuthRef, p.OAuthIdentity = cred.Ref, identity
			return *p, "relinked"
		}
	}
	name := desiredOAuthProviderName(cred)
	base := name
	used := map[string]bool{}
	maxPriority := 0
	for _, p := range client.Providers {
		used[p.Name] = true
		if p.Priority > maxPriority {
			maxPriority = p.Priority
		}
	}
	for n := 2; used[name]; n++ {
		name = fmt.Sprintf("%s-%d", base, n)
	}
	enabled := true
	client.Providers = append(client.Providers, config.Provider{Name: name, AuthType: config.ProviderAuthTypeOAuth, OAuthProvider: cred.Provider, OAuthRef: cred.Ref, OAuthIdentity: oauth.AccountIdentityKey(cred), Priority: maxPriority + 1, Enabled: &enabled})
	return client.Providers[len(client.Providers)-1], "created"
}

func desiredOAuthProviderName(cred *oauth.Credential) string {
	if cred == nil {
		return "oauth-account"
	}
	providerPart := slugProviderName(string(cred.Provider))
	identityPart := slugProviderName(cred.Email)
	if cred.Provider == config.OAuthProviderGemini || cred.Provider == config.OAuthProviderAntigravity {
		projectPart := slugProviderName(cred.AccountID)
		if projectPart == "" {
			projectPart = slugProviderName(cred.Metadata["project_id"])
		}
		if identityPart != "" && projectPart != "" {
			identityPart += "-" + projectPart
		} else if projectPart != "" {
			identityPart = projectPart
		}
	}
	if identityPart == "" {
		identityPart = slugProviderName(cred.Ref)
	}
	name := strings.Trim(providerPart+"-"+identityPart, "-")
	if name == "" {
		name = "oauth-account"
	}
	if len(name) > 64 {
		name = strings.Trim(name[:64], "-")
	}
	return name
}

func slugProviderName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	lastDash := false
	for _, r := range value {
		alphaNumeric := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if alphaNumeric {
			_, _ = out.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			_ = out.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func usageProviderCount(snapshot Usage) int {
	total := 0
	for _, providers := range snapshot.Clients {
		total += len(providers)
	}
	return total
}

func writeConfig(dir string, cfg *config.Config) error {
	return writeConfigWithHook(dir, cfg, nil)
}

func writeConfigWithHook(dir string, cfg *config.Config, beforeWrite func(index int, name string) error) error {
	files := []struct {
		name  string
		value any
	}{{"config.yaml", cfg.Global}, {"claude.yaml", cfg.Claude}, {"openai.yaml", cfg.OpenAI}, {"gemini.yaml", cfg.Gemini}}
	for index, file := range files {
		if beforeWrite != nil {
			if err := beforeWrite(index, file.name); err != nil {
				return err
			}
		}
		data, err := yaml.Marshal(file.value)
		if err != nil {
			return fmt.Errorf("marshal %s: %w", file.name, err)
		}
		if err := atomicWrite(filepath.Join(dir, file.name), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

type fileSnapshot struct {
	path   string
	data   []byte
	mode   fs.FileMode
	exists bool
}
type filesSnapshot []fileSnapshot

func snapshotFiles(dir string, names []string) (filesSnapshot, error) {
	out := make(filesSnapshot, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			out = append(out, fileSnapshot{path: path})
			continue
		}
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, fileSnapshot{path: path, data: data, mode: info.Mode().Perm(), exists: true})
	}
	return out, nil
}
func (s filesSnapshot) restore() error {
	for _, file := range s {
		if !file.exists {
			if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err := atomicWrite(file.path, file.data, file.mode); err != nil {
			return err
		}
	}
	return nil
}

type dirSnapshot struct {
	target, contentTarget, backup string
	linkTarget                    string
	exists, symlink               bool
}

func snapshotDirectory(target string) (dirSnapshot, error) {
	s := dirSnapshot{target: target}
	rootInfo, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	s.contentTarget = target
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		s.linkTarget, err = os.Readlink(target)
		if err != nil {
			return s, err
		}
		s.symlink = true
		if filepath.IsAbs(s.linkTarget) {
			s.contentTarget = s.linkTarget
		} else {
			s.contentTarget = filepath.Join(filepath.Dir(target), s.linkTarget)
		}
	}
	info, err := os.Stat(s.contentTarget)
	if err != nil {
		return s, err
	}
	if !info.IsDir() {
		return s, fmt.Errorf("%s is not a directory", target)
	}
	backup, err := os.MkdirTemp(filepath.Dir(target), ".clipal-transfer-oauth-*")
	if err != nil {
		return s, err
	}
	s.backup, s.exists = backup, true
	if err := copyDirectory(s.contentTarget, backup); err != nil {
		s.cleanup()
		return dirSnapshot{}, err
	}
	return s, nil
}
func (s dirSnapshot) restore() error {
	if err := clearDirectory(s.contentTarget); err != nil {
		return err
	}
	if !s.exists {
		return os.RemoveAll(s.target)
	}
	if err := os.MkdirAll(s.contentTarget, 0o700); err != nil {
		return err
	}
	if err := copyDirectory(s.backup, s.contentTarget); err != nil {
		return err
	}
	if s.symlink {
		info, err := os.Lstat(s.target)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			current, readErr := os.Readlink(s.target)
			if readErr == nil && current == s.linkTarget {
				return nil
			}
		}
		if err := os.RemoveAll(s.target); err != nil {
			return err
		}
		return os.Symlink(s.linkTarget, s.target)
	}
	return nil
}
func (s dirSnapshot) cleanup() {
	if s.backup != "" {
		_ = os.RemoveAll(s.backup)
	}
}
func copyDirectory(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported credential storage entry type: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return atomicWrite(target, data, info.Mode().Perm())
	})
}

func clearDirectory(path string) error {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(path, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".clipal-transfer-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
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
	return os.Rename(tmpName, path)
}
