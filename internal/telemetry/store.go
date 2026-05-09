package telemetry

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	storeFilename          = "usage.json"
	storeVersion           = 3
	defaultPersistInterval = 3 * time.Second
)

type UsageDelta struct {
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
	TotalTokens  int64 `json:"total_tokens,omitempty"`
}

type UsageSnapshot struct {
	UsageDelta
	Usage           map[string]any `json:"usage,omitempty"`
	ReasoningTokens int64          `json:"reasoning_tokens,omitempty"`
	ThoughtsTokens  int64          `json:"thoughts_tokens,omitempty"`
	CostMicros      int64          `json:"cost_micros,omitempty"`
	HasCost         bool           `json:"has_cost,omitempty"`
}

type ProviderRef struct {
	ClientType string
	Provider   string
}

type RecordOptions struct {
	CountRequest bool
	CountSuccess bool
}

func (u UsageDelta) normalized() UsageDelta {
	if u.TotalTokens <= 0 {
		total := u.InputTokens + u.OutputTokens
		if total > 0 {
			u.TotalTokens = total
		}
	}
	return u
}

type ProviderUsage struct {
	RequestCount    int64                      `json:"request_count,omitempty"`
	SuccessCount    int64                      `json:"success_count,omitempty"`
	InputTokens     int64                      `json:"input_tokens,omitempty"`
	OutputTokens    int64                      `json:"output_tokens,omitempty"`
	TotalTokens     int64                      `json:"total_tokens,omitempty"`
	ReasoningTokens int64                      `json:"reasoning_tokens,omitempty"`
	ThoughtsTokens  int64                      `json:"thoughts_tokens,omitempty"`
	TotalCostMicros int64                      `json:"total_cost_micros,omitempty"`
	LastUsedAt      time.Time                  `json:"last_used_at,omitempty"`
	Usage           map[string]any             `json:"usage,omitempty"`
	DailyCosts      map[string]DailyCostBucket `json:"daily_costs,omitempty"`
	HasCost         bool                       `json:"has_cost,omitempty"`
}

type DailyCostBucket struct {
	CostMicros int64 `json:"cost_micros,omitempty"`
	HasCost    bool  `json:"has_cost,omitempty"`
}

type clientUsage struct {
	Providers map[string]ProviderUsage `json:"providers,omitempty"`
}

type storeState struct {
	Version   int                    `json:"version"`
	UpdatedAt time.Time              `json:"updated_at,omitempty"`
	Clients   map[string]clientUsage `json:"clients,omitempty"`
}

type Store struct {
	path            string
	persistInterval time.Duration

	mu          sync.RWMutex
	state       storeState
	dirty       bool
	lastPersist time.Time
	revision    uint64

	persistCh chan struct{}
	closeCh   chan struct{}
	doneCh    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

var afterCloneForFlush func()

func NewStore(configDir string) (*Store, error) {
	configDir = strings.TrimSpace(configDir)
	s := &Store{
		persistInterval: defaultPersistInterval,
		state: storeState{
			Version: storeVersion,
			Clients: map[string]clientUsage{},
		},
	}
	if configDir == "" {
		return s, nil
	}
	s.path = filepath.Join(configDir, storeFilename)
	s.persistCh = make(chan struct{}, 1)
	s.closeCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.startPersistenceWorker()
	if err := s.load(); err != nil {
		return s, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var state storeState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.Version == 0 {
		state.Version = storeVersion
	}
	if state.Clients == nil {
		state.Clients = map[string]clientUsage{}
	}
	for client, usage := range state.Clients {
		if usage.Providers == nil {
			usage.Providers = map[string]ProviderUsage{}
			state.Clients[client] = usage
		}
	}
	s.state = state
	return nil
}

func (s *Store) RecordUsage(clientType string, provider string, snapshot UsageSnapshot, when time.Time) error {
	return s.Record(clientType, provider, snapshot, when, RecordOptions{
		CountRequest: true,
		CountSuccess: true,
	})
}

func (s *Store) Record(clientType string, provider string, snapshot UsageSnapshot, when time.Time, options RecordOptions) error {
	clientType = strings.TrimSpace(clientType)
	provider = strings.TrimSpace(provider)
	if s == nil || clientType == "" || provider == "" || (!options.CountRequest && !options.CountSuccess) {
		return nil
	}

	delta := snapshot.normalized()
	if when.IsZero() {
		when = time.Now()
	}

	s.mu.Lock()
	client := s.state.Clients[clientType]
	if client.Providers == nil {
		client.Providers = map[string]ProviderUsage{}
	}
	entry := client.Providers[provider]
	if options.CountRequest {
		entry.RequestCount++
	}
	if options.CountSuccess {
		entry.SuccessCount++
	}
	entry.InputTokens += delta.InputTokens
	entry.OutputTokens += delta.OutputTokens
	entry.TotalTokens += delta.TotalTokens
	entry.ReasoningTokens += snapshot.ReasoningTokens
	entry.ThoughtsTokens += snapshot.ThoughtsTokens
	if snapshot.HasCost {
		entry.TotalCostMicros += snapshot.CostMicros
		entry.HasCost = true
		if entry.DailyCosts == nil {
			entry.DailyCosts = make(map[string]DailyCostBucket)
		}
		day := usageDayBucket(when)
		bucket := entry.DailyCosts[day]
		bucket.CostMicros += snapshot.CostMicros
		bucket.HasCost = true
		entry.DailyCosts[day] = bucket
	}
	entry.LastUsedAt = when
	if snapshot.Usage != nil {
		entry.Usage = cloneMap(snapshot.Usage)
	}
	client.Providers[provider] = entry
	s.state.Clients[clientType] = client
	s.state.Version = storeVersion
	s.state.UpdatedAt = when
	s.dirty = true
	s.revision++
	s.mu.Unlock()

	s.notifyPersist()
	return nil
}

func (s *Store) ProviderSnapshot(clientType string, provider string) (ProviderUsage, bool) {
	clientType = strings.TrimSpace(clientType)
	provider = strings.TrimSpace(provider)
	if s == nil || clientType == "" || provider == "" {
		return ProviderUsage{}, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	client, ok := s.state.Clients[clientType]
	if !ok || client.Providers == nil {
		return ProviderUsage{}, false
	}
	usage, ok := client.Providers[provider]
	return cloneProviderUsage(usage), ok
}

func (s *Store) ProviderSnapshots(clientType string) map[string]ProviderUsage {
	clientType = strings.TrimSpace(clientType)
	if s == nil || clientType == "" {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	client, ok := s.state.Clients[clientType]
	if !ok || len(client.Providers) == 0 {
		return nil
	}
	out := make(map[string]ProviderUsage, len(client.Providers))
	for name, usage := range client.Providers {
		out[name] = cloneProviderUsage(usage)
	}
	return out
}

func (s *Store) RenameProvider(clientType string, from string, to string) error {
	clientType = strings.TrimSpace(clientType)
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if s == nil || clientType == "" || from == "" || to == "" || from == to {
		return nil
	}

	s.mu.Lock()
	client, ok := s.state.Clients[clientType]
	if !ok || client.Providers == nil {
		s.mu.Unlock()
		return nil
	}
	entry, ok := client.Providers[from]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	if existing, exists := client.Providers[to]; exists {
		entry = mergeProviderUsage(entry, existing)
	}
	delete(client.Providers, from)
	client.Providers[to] = entry
	s.state.Clients[clientType] = client
	s.state.Version = storeVersion
	s.state.UpdatedAt = time.Now()
	s.dirty = true
	s.revision++
	s.mu.Unlock()
	return s.Flush()
}

func (s *Store) DeleteProvider(clientType string, provider string) error {
	clientType = strings.TrimSpace(clientType)
	provider = strings.TrimSpace(provider)
	if s == nil || clientType == "" || provider == "" {
		return nil
	}

	s.mu.Lock()
	client, ok := s.state.Clients[clientType]
	if !ok || client.Providers == nil {
		s.mu.Unlock()
		return nil
	}
	if _, ok := client.Providers[provider]; !ok {
		s.mu.Unlock()
		return nil
	}
	delete(client.Providers, provider)
	if len(client.Providers) == 0 {
		delete(s.state.Clients, clientType)
	} else {
		s.state.Clients[clientType] = client
	}
	s.state.Version = storeVersion
	s.state.UpdatedAt = time.Now()
	s.dirty = true
	s.revision++
	s.mu.Unlock()
	return s.Flush()
}

func (s *Store) DeleteProvidersWithRollback(refs []ProviderRef) (func() error, error) {
	if s == nil {
		return func() error { return nil }, nil
	}

	refs = normalizeProviderRefs(refs)
	if len(refs) == 0 {
		return func() error { return nil }, nil
	}

	deleted := s.deleteProviders(refs)
	if len(deleted) == 0 {
		return func() error { return nil }, nil
	}

	if err := s.Flush(); err != nil {
		if restoreErr := s.restoreProviders(deleted); restoreErr != nil {
			return nil, fmt.Errorf("flush deleted providers: %w (rollback failed: %v)", err, restoreErr)
		}
		return nil, err
	}

	return func() error {
		return s.restoreProviders(deleted)
	}, nil
}

func (s *Store) Flush() error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil
	}

	for {
		s.mu.Lock()
		if !s.dirty {
			s.mu.Unlock()
			return nil
		}
		state := cloneState(s.state)
		revision := s.revision
		s.mu.Unlock()

		if afterCloneForFlush != nil {
			afterCloneForFlush()
		}

		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if err := atomicWriteFile(s.path, data, 0o600); err != nil {
			return err
		}

		now := time.Now()
		s.mu.Lock()
		s.lastPersist = now
		if s.revision == revision {
			s.dirty = false
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()
	}
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	if s.closeCh != nil {
		s.closeOnce.Do(func() {
			close(s.closeCh)
			if s.doneCh != nil {
				<-s.doneCh
			}
		})
	}
	return s.Flush()
}

func (s *Store) notifyPersist() {
	if s == nil || strings.TrimSpace(s.path) == "" || s.persistCh == nil {
		return
	}
	s.startPersistenceWorker()
	select {
	case s.persistCh <- struct{}{}:
	default:
	}
}

func (s *Store) startPersistenceWorker() {
	if s == nil || s.persistCh == nil || s.closeCh == nil || s.doneCh == nil {
		return
	}
	s.startOnce.Do(func() {
		go s.persistenceWorker()
	})
}

func (s *Store) persistenceWorker() {
	defer close(s.doneCh)

	var timer *time.Timer
	var timerC <-chan time.Time
	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer = nil
		timerC = nil
	}
	scheduleFlush := func() {
		if s.persistInterval <= 0 {
			_ = s.Flush()
			return
		}
		if timer != nil {
			return
		}
		timer = time.NewTimer(s.persistInterval)
		timerC = timer.C
	}

	for {
		select {
		case <-s.persistCh:
			scheduleFlush()
		case <-timerC:
			timer = nil
			timerC = nil
			if err := s.Flush(); err != nil {
				scheduleFlush()
			}
		case <-s.closeCh:
			stopTimer()
			return
		}
	}
}

func cloneState(state storeState) storeState {
	out := storeState{
		Version:   state.Version,
		UpdatedAt: state.UpdatedAt,
		Clients:   make(map[string]clientUsage, len(state.Clients)),
	}
	for clientName, client := range state.Clients {
		nextClient := clientUsage{
			Providers: make(map[string]ProviderUsage, len(client.Providers)),
		}
		for providerName, usage := range client.Providers {
			nextClient.Providers[providerName] = cloneProviderUsage(usage)
		}
		out.Clients[clientName] = nextClient
	}
	return out
}

func normalizeProviderRefs(refs []ProviderRef) []ProviderRef {
	seen := make(map[string]struct{}, len(refs))
	out := make([]ProviderRef, 0, len(refs))
	for _, ref := range refs {
		clientType := strings.TrimSpace(ref.ClientType)
		provider := strings.TrimSpace(ref.Provider)
		if clientType == "" || provider == "" {
			continue
		}
		key := clientType + "\x00" + provider
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ProviderRef{ClientType: clientType, Provider: provider})
	}
	return out
}

func (s *Store) deleteProviders(refs []ProviderRef) map[string]map[string]ProviderUsage {
	now := time.Now()
	deleted := make(map[string]map[string]ProviderUsage)

	s.mu.Lock()
	defer s.mu.Unlock()

	var changed bool
	for _, ref := range refs {
		client, ok := s.state.Clients[ref.ClientType]
		if !ok || client.Providers == nil {
			continue
		}
		entry, ok := client.Providers[ref.Provider]
		if !ok {
			continue
		}
		if deleted[ref.ClientType] == nil {
			deleted[ref.ClientType] = make(map[string]ProviderUsage)
		}
		deleted[ref.ClientType][ref.Provider] = cloneProviderUsage(entry)
		delete(client.Providers, ref.Provider)
		if len(client.Providers) == 0 {
			delete(s.state.Clients, ref.ClientType)
		} else {
			s.state.Clients[ref.ClientType] = client
		}
		changed = true
	}
	if !changed {
		return nil
	}
	s.state.Version = storeVersion
	s.state.UpdatedAt = now
	s.dirty = true
	s.revision++
	return deleted
}

func (s *Store) restoreProviders(deleted map[string]map[string]ProviderUsage) error {
	if s == nil || len(deleted) == 0 {
		return nil
	}

	now := time.Now()
	s.mu.Lock()
	var changed bool
	for clientType, providers := range deleted {
		if len(providers) == 0 {
			continue
		}
		client := s.state.Clients[clientType]
		if client.Providers == nil {
			client.Providers = make(map[string]ProviderUsage)
		}
		for provider, usage := range providers {
			if existing, ok := client.Providers[provider]; ok {
				client.Providers[provider] = mergeProviderUsage(existing, usage)
			} else {
				client.Providers[provider] = cloneProviderUsage(usage)
			}
			changed = true
		}
		s.state.Clients[clientType] = client
	}
	if changed {
		s.state.Version = storeVersion
		s.state.UpdatedAt = now
		s.dirty = true
		s.revision++
	}
	s.mu.Unlock()

	if !changed {
		return nil
	}
	return s.Flush()
}

func cloneProviderUsage(usage ProviderUsage) ProviderUsage {
	if usage.Usage != nil {
		usage.Usage = cloneMap(usage.Usage)
	}
	if usage.DailyCosts != nil {
		cloned := make(map[string]DailyCostBucket, len(usage.DailyCosts))
		for key, bucket := range usage.DailyCosts {
			cloned[key] = bucket
		}
		usage.DailyCosts = cloned
	}
	return usage
}

func mergeProviderUsage(left ProviderUsage, right ProviderUsage) ProviderUsage {
	out := cloneProviderUsage(left)
	out.RequestCount += right.RequestCount
	out.SuccessCount += right.SuccessCount
	out.InputTokens += right.InputTokens
	out.OutputTokens += right.OutputTokens
	out.TotalTokens += right.TotalTokens
	out.ReasoningTokens += right.ReasoningTokens
	out.ThoughtsTokens += right.ThoughtsTokens
	out.TotalCostMicros += right.TotalCostMicros
	out.HasCost = out.HasCost || right.HasCost
	if len(right.DailyCosts) > 0 {
		if out.DailyCosts == nil {
			out.DailyCosts = make(map[string]DailyCostBucket, len(right.DailyCosts))
		}
		for day, bucket := range right.DailyCosts {
			current := out.DailyCosts[day]
			current.CostMicros += bucket.CostMicros
			current.HasCost = current.HasCost || bucket.HasCost
			out.DailyCosts[day] = current
		}
	}
	if right.LastUsedAt.After(out.LastUsedAt) {
		out.LastUsedAt = right.LastUsedAt
		if right.Usage != nil {
			out.Usage = cloneMap(right.Usage)
		}
	}
	if out.Usage == nil && right.Usage != nil {
		out.Usage = cloneMap(right.Usage)
	}
	return out
}

func usageDayBucket(when time.Time) string {
	return when.Format("2006-01-02")
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func atomicWriteFile(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, ".clipal-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	success := false
	defer func() {
		_ = f.Close()
		if !success {
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
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	success = true
	return nil
}
