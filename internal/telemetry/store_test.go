package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreRecordRenameDeleteAndReload(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	if err := store.RecordUsage("openai", "p1", UsageSnapshot{
		UsageDelta:      UsageDelta{InputTokens: 10, OutputTokens: 20},
		Usage:           map[string]any{"input_tokens": 10.0, "output_tokens": 20.0},
		ReasoningTokens: 7,
		ThoughtsTokens:  3,
		CostMicros:      1_250_000,
		HasCost:         true,
		CostSource:      CostSourceCatalog,
		CatalogVersion:  "catalog-v1",
	}, now); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	got, ok := store.ProviderSnapshot("openai", "p1")
	if !ok {
		t.Fatalf("ProviderSnapshot missing")
	}
	if got.TotalTokens != 30 || got.RequestCount != 1 || got.SuccessCount != 1 {
		t.Fatalf("snapshot = %#v", got)
	}
	if got.Usage == nil {
		t.Fatalf("expected raw usage to be preserved")
	}
	if got.ReasoningTokens != 7 || got.ThoughtsTokens != 3 {
		t.Fatalf("expected reasoning/thought tokens to be preserved: %#v", got)
	}
	if !got.HasCost || got.TotalCostMicros != 1_250_000 {
		t.Fatalf("expected spend to be preserved: %#v", got)
	}
	if !reflect.DeepEqual(got.CostSources, []string{CostSourceCatalog}) || !reflect.DeepEqual(got.CatalogVersions, []string{"catalog-v1"}) {
		t.Fatalf("expected cost attribution, got %#v", got)
	}
	if bucket, ok := got.DailyCosts["2026-04-08"]; !ok || !bucket.HasCost || bucket.CostMicros != 1_250_000 {
		t.Fatalf("expected daily spend bucket, got %#v", got.DailyCosts)
	}

	if err := store.RenameProvider("openai", "p1", "p2"); err != nil {
		t.Fatalf("RenameProvider: %v", err)
	}
	if _, ok := store.ProviderSnapshot("openai", "p1"); ok {
		t.Fatalf("old provider snapshot should be gone")
	}
	if got, ok := store.ProviderSnapshot("openai", "p2"); !ok || got.TotalTokens != 30 || got.TotalCostMicros != 1_250_000 || got.ReasoningTokens != 7 || got.ThoughtsTokens != 3 {
		t.Fatalf("renamed snapshot = %#v ok=%v", got, ok)
	}

	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}
	if got, ok := reloaded.ProviderSnapshot("openai", "p2"); !ok || got.TotalTokens != 30 || got.TotalCostMicros != 1_250_000 || got.ReasoningTokens != 7 || got.ThoughtsTokens != 3 {
		t.Fatalf("reloaded snapshot = %#v ok=%v", got, ok)
	}

	if err := reloaded.DeleteProvider("openai", "p2"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	if err := reloaded.Flush(); err != nil {
		t.Fatalf("Flush after delete: %v", err)
	}
	if _, ok := reloaded.ProviderSnapshot("openai", "p2"); ok {
		t.Fatalf("deleted provider snapshot should be gone")
	}

	data, err := os.ReadFile(filepath.Join(dir, storeFilename))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) == "" {
		t.Fatalf("expected persisted data")
	}
}

func TestRecordUsageSaturatesCostInsteadOfOverflowing(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	const maxInt64 = int64(^uint64(0) >> 1)
	for _, cost := range []int64{maxInt64 - 10, 20} {
		if err := store.RecordUsage("openai", "p1", UsageSnapshot{CostMicros: cost, HasCost: true}, now); err != nil {
			t.Fatal(err)
		}
	}
	got, ok := store.ProviderSnapshot("openai", "p1")
	if !ok || got.TotalCostMicros != maxInt64 || got.DailyCosts[usageDayBucket(now)].CostMicros != maxInt64 {
		t.Fatalf("cost did not saturate: %#v", got)
	}
}

func TestRecordUsageSaturatesAllCounters(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const maxInt64 = int64(^uint64(0) >> 1)
	store.state.Clients["openai"] = clientUsage{Providers: map[string]ProviderUsage{"p1": {
		RequestCount: maxInt64, SuccessCount: maxInt64, InputTokens: maxInt64, OutputTokens: maxInt64,
		TotalTokens: maxInt64, ReasoningTokens: maxInt64, ThoughtsTokens: maxInt64,
	}}}
	if err := store.RecordUsage("openai", "p1", UsageSnapshot{UsageDelta: UsageDelta{InputTokens: 1, OutputTokens: 1, TotalTokens: 1}, ReasoningTokens: 1, ThoughtsTokens: 1}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := store.ProviderSnapshot("openai", "p1")
	if got.RequestCount != maxInt64 || got.SuccessCount != maxInt64 || got.InputTokens != maxInt64 || got.OutputTokens != maxInt64 || got.TotalTokens != maxInt64 || got.ReasoningTokens != maxInt64 || got.ThoughtsTokens != maxInt64 {
		t.Fatalf("counter overflow: %#v", got)
	}
}

func TestStoreTracksMixedCostAttribution(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, snapshot := range []UsageSnapshot{
		{CostMicros: 1, HasCost: true, CostSource: CostSourceCatalog, CatalogVersion: "v1"},
		{CostMicros: 2, HasCost: true, CostSource: CostSourceCatalog, CatalogVersion: "v2"},
		{CostMicros: 3, HasCost: true, CostSource: CostSourceUpstream},
	} {
		if err := store.RecordUsage("openai", "p1", snapshot, now); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := store.ProviderSnapshot("openai", "p1")
	if !reflect.DeepEqual(got.CostSources, []string{CostSourceCatalog, CostSourceUpstream}) || !reflect.DeepEqual(got.CatalogVersions, []string{"v1", "v2"}) {
		t.Fatalf("mixed attribution = %#v", got)
	}
	bucket := got.DailyCosts[usageDayBucket(now)]
	if !reflect.DeepEqual(bucket.CostSources, got.CostSources) || !reflect.DeepEqual(bucket.CatalogVersions, got.CatalogVersions) {
		t.Fatalf("daily attribution = %#v", bucket)
	}
}

func TestStoreIgnoresInvalidCostAndDowngradesInvalidAttribution(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, snapshot := range []UsageSnapshot{
		{CostMicros: -1, HasCost: true, CostSource: CostSourceUpstream},
		{CostMicros: 2, HasCost: true, CostSource: "untrusted"},
		{CostMicros: 3, HasCost: true, CostSource: CostSourceCatalog},
	} {
		if err := store.RecordUsage("openai", "p1", snapshot, now); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := store.ProviderSnapshot("openai", "p1")
	if got.TotalCostMicros != 5 || !got.HasCost {
		t.Fatalf("invalid cost was not ignored: %#v", got)
	}
	if !reflect.DeepEqual(got.CostSources, []string{CostSourceLegacyUnknown}) || len(got.CatalogVersions) != 0 {
		t.Fatalf("invalid attribution was not downgraded: %#v", got)
	}
	bucket := got.DailyCosts[usageDayBucket(now)]
	if bucket.CostMicros != 5 || !reflect.DeepEqual(bucket.CostSources, []string{CostSourceLegacyUnknown}) {
		t.Fatalf("daily attribution was not sanitized: %#v", bucket)
	}
}

func TestStoreMarksLegacyCostAttributionUnknown(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"version":3,"clients":{"openai":{"providers":{"p1":{"total_cost_micros":7,"has_cost":true,"daily_costs":{"2026-01-01":{"cost_micros":7,"has_cost":true}}}}}}}`
	if err := os.WriteFile(filepath.Join(dir, storeFilename), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := store.ProviderSnapshot("openai", "p1")
	if !reflect.DeepEqual(got.CostSources, []string{CostSourceLegacyUnknown}) || !reflect.DeepEqual(got.DailyCosts["2026-01-01"].CostSources, []string{CostSourceLegacyUnknown}) {
		t.Fatalf("legacy attribution = %#v", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	persisted, err := os.ReadFile(filepath.Join(dir, storeFilename))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(persisted), `"version": 4`) || !strings.Contains(string(persisted), `"legacy_unknown"`) {
		t.Fatalf("migration was not persisted: %s", persisted)
	}
}

func TestNormalizeCostAttributionSortsAndDeduplicates(t *testing.T) {
	usage := normalizeCostAttribution(ProviderUsage{
		HasCost: true, CostSources: []string{CostSourceUpstream, CostSourceCatalog, CostSourceCatalog}, CatalogVersions: []string{"v2", "v1", "v2"},
		DailyCosts: map[string]DailyCostBucket{"2026-01-01": {HasCost: true, CostSources: []string{CostSourceUpstream, CostSourceUpstream}, CatalogVersions: []string{"v2", "v1", "v1"}}},
	})
	if !reflect.DeepEqual(usage.CostSources, []string{CostSourceCatalog, CostSourceUpstream}) || !reflect.DeepEqual(usage.CatalogVersions, []string{"v1", "v2"}) {
		t.Fatalf("provider attribution = %#v", usage)
	}
	bucket := usage.DailyCosts["2026-01-01"]
	if !reflect.DeepEqual(bucket.CostSources, []string{CostSourceUpstream}) || !reflect.DeepEqual(bucket.CatalogVersions, []string{"v1", "v2"}) {
		t.Fatalf("daily attribution = %#v", bucket)
	}
}

func TestStoreFlushPersistsConcurrentMutation(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	store.persistInterval = time.Hour
	store.lastPersist = time.Date(2026, 4, 8, 11, 59, 0, 0, time.UTC)
	if err := store.RecordUsage("openai", "p1", UsageSnapshot{UsageDelta: UsageDelta{InputTokens: 1, OutputTokens: 2}, Usage: map[string]any{"input_tokens": 1.0, "output_tokens": 2.0}}, time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	triggered := false
	afterCloneForFlush = func() {
		if triggered {
			return
		}
		triggered = true

		store.mu.Lock()
		client := store.state.Clients["openai"]
		entry := client.Providers["p1"]
		entry.RequestCount++
		entry.SuccessCount++
		entry.InputTokens += 10
		entry.OutputTokens += 20
		entry.TotalTokens += 30
		entry.LastUsedAt = time.Date(2026, 4, 8, 12, 1, 0, 0, time.UTC)
		entry.Usage = map[string]any{"input_tokens": 10.0, "output_tokens": 20.0, "total_tokens": 30.0}
		client.Providers["p1"] = entry
		store.state.Clients["openai"] = client
		store.state.UpdatedAt = entry.LastUsedAt
		store.dirty = true
		store.revision++
		store.mu.Unlock()
	}
	defer func() { afterCloneForFlush = nil }()

	if err := store.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, storeFilename))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var persisted storeState
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got := persisted.Clients["openai"].Providers["p1"]
	if got.RequestCount != 2 || got.SuccessCount != 2 || got.TotalTokens != 33 {
		t.Fatalf("persisted snapshot = %#v", got)
	}
}

func TestConcurrentFlushesCannotWriteOutOfOrder(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.persistInterval = time.Hour
	if err := store.RecordUsage("openai", "p1", UsageSnapshot{UsageDelta: UsageDelta{TotalTokens: 1}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	firstClone := make(chan struct{})
	secondClone := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	afterCloneForFlush = func() {
		switch calls.Add(1) {
		case 1:
			close(firstClone)
			<-releaseFirst
		case 2:
			close(secondClone)
		}
	}
	defer func() { afterCloneForFlush = nil }()
	errs := make(chan error, 2)
	go func() { errs <- store.Flush() }()
	<-firstClone
	if err := store.RecordUsage("openai", "p1", UsageSnapshot{UsageDelta: UsageDelta{TotalTokens: 2}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	go func() { errs <- store.Flush() }()
	select {
	case <-secondClone:
		t.Fatal("second flush cloned state while first flush was writing")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	reloaded, err := NewStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reloaded.ProviderSnapshot("openai", "p1")
	if got.TotalTokens != 3 {
		t.Fatalf("persisted stale state: %#v", got)
	}
}

func TestTransferWaitsForInFlightFlush(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.persistInterval = time.Hour
	if err := store.RecordUsage("openai", "p1", UsageSnapshot{UsageDelta: UsageDelta{TotalTokens: 1}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cloned := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	afterCloneForFlush = func() {
		if calls.Add(1) == 1 {
			close(cloned)
			<-release
		}
	}
	defer func() { afterCloneForFlush = nil }()
	flushDone := make(chan error, 1)
	go func() { flushDone <- store.Flush() }()
	<-cloned
	txReady := make(chan *TransferTransaction, 1)
	txErr := make(chan error, 1)
	go func() {
		tx, err := store.BeginTransfer()
		if err != nil {
			txErr <- err
			return
		}
		txReady <- tx
	}()
	select {
	case <-txReady:
		t.Fatal("transfer started while an older flush was in flight")
	case err := <-txErr:
		t.Fatal(err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-flushDone; err != nil {
		t.Fatal(err)
	}
	var tx *TransferTransaction
	select {
	case tx = <-txReady:
	case err := <-txErr:
		t.Fatal(err)
	}
	tx.Restore(DataSnapshot{Clients: map[string]map[string]ProviderUsage{"openai": {"p2": {TotalTokens: 2}}}}, false)
	if err := tx.Flush(); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	reloaded, err := NewStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.ProviderSnapshot("openai", "p1"); ok {
		t.Fatal("stale pre-transfer state was persisted")
	}
	if got, ok := reloaded.ProviderSnapshot("openai", "p2"); !ok || got.TotalTokens != 2 {
		t.Fatalf("transfer state missing: %#v, %v", got, ok)
	}
}

func TestStoreRenameProviderPrefersLatestUsagePayload(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	older := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	newer := older.Add(5 * time.Minute)
	if err := store.RecordUsage("openai", "from", UsageSnapshot{UsageDelta: UsageDelta{InputTokens: 1, OutputTokens: 2}, Usage: map[string]any{"prompt_tokens": 1.0, "completion_tokens": 2.0, "total_tokens": 3.0}}, older); err != nil {
		t.Fatalf("RecordUsage from: %v", err)
	}
	if err := store.RecordUsage("openai", "to", UsageSnapshot{UsageDelta: UsageDelta{InputTokens: 10, OutputTokens: 20}, Usage: map[string]any{"prompt_tokens": 10.0, "completion_tokens": 20.0, "total_tokens": 30.0}}, newer); err != nil {
		t.Fatalf("RecordUsage to: %v", err)
	}

	if err := store.RenameProvider("openai", "from", "to"); err != nil {
		t.Fatalf("RenameProvider: %v", err)
	}

	got, ok := store.ProviderSnapshot("openai", "to")
	if !ok {
		t.Fatalf("expected merged provider snapshot")
	}
	if !got.LastUsedAt.Equal(newer) {
		t.Fatalf("last_used_at = %v want %v", got.LastUsedAt, newer)
	}
	if int64Value(got.Usage["total_tokens"]) != 30 {
		t.Fatalf("usage = %#v", got.Usage)
	}
}

func TestStoreReloadPreservesLargeRawUsageInteger(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordUsage("openai", "p1", UsageSnapshot{Usage: map[string]any{"cost_micros": json.Number("9007199254740993")}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	usage, ok := reloaded.ProviderSnapshot("openai", "p1")
	if !ok {
		t.Fatal("missing provider")
	}
	value, ok := usage.Usage["cost_micros"].(json.Number)
	if !ok || value.String() != "9007199254740993" {
		t.Fatalf("cost_micros = %#v", usage.Usage["cost_micros"])
	}
}

func TestStoreRecordCanSkipSuccessCount(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	if err := store.Record("openai", "p1", UsageSnapshot{}, now, RecordOptions{
		CountRequest: true,
		CountSuccess: false,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, ok := store.ProviderSnapshot("openai", "p1")
	if !ok {
		t.Fatalf("ProviderSnapshot missing")
	}
	if got.RequestCount != 1 || got.SuccessCount != 0 {
		t.Fatalf("snapshot = %#v", got)
	}
}

func TestStoreRecordPersistsAsynchronously(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	store.persistInterval = 10 * time.Millisecond

	if err := store.RecordUsage("openai", "p1", UsageSnapshot{
		UsageDelta: UsageDelta{InputTokens: 1, OutputTokens: 2},
	}, time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		data, err := os.ReadFile(filepath.Join(dir, storeFilename))
		if err == nil && len(data) > 0 {
			var persisted storeState
			if err := json.Unmarshal(data, &persisted); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			got := persisted.Clients["openai"].Providers["p1"]
			if got.TotalTokens == 3 && got.RequestCount == 1 && got.SuccessCount == 1 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for async telemetry persistence")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStoreRecordPersistenceIsThrottledNotDebounced(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer func() { _ = store.Close() }()
	store.persistInterval = 200 * time.Millisecond

	start := time.Now()
	if err := store.RecordUsage("openai", "p1", UsageSnapshot{
		UsageDelta: UsageDelta{InputTokens: 1},
	}, time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("RecordUsage first: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if err := store.RecordUsage("openai", "p1", UsageSnapshot{
		UsageDelta: UsageDelta{OutputTokens: 2},
	}, time.Date(2026, 4, 8, 12, 0, 1, 0, time.UTC)); err != nil {
		t.Fatalf("RecordUsage second: %v", err)
	}

	deadline := start.Add(300 * time.Millisecond)
	for {
		data, err := os.ReadFile(filepath.Join(dir, storeFilename))
		if err == nil && len(data) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for first scheduled telemetry persistence")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStoreDeleteProvidersWithRollbackRestoresDeletedUsage(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	older := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	newer := older.Add(5 * time.Minute)
	if err := store.RecordUsage("openai", "p1", UsageSnapshot{
		UsageDelta: UsageDelta{InputTokens: 1, OutputTokens: 2},
		Usage:      map[string]any{"prompt_tokens": 1.0, "completion_tokens": 2.0, "total_tokens": 3.0},
		CostMicros: 1_250_000,
		HasCost:    true,
	}, older); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	restore, err := store.DeleteProvidersWithRollback([]ProviderRef{
		{ClientType: "openai", Provider: "p1"},
		{ClientType: "openai", Provider: "p1"},
		{ClientType: "openai", Provider: "missing"},
	})
	if err != nil {
		t.Fatalf("DeleteProvidersWithRollback: %v", err)
	}
	if _, ok := store.ProviderSnapshot("openai", "p1"); ok {
		t.Fatalf("expected provider snapshot to be deleted")
	}

	if err := store.RecordUsage("openai", "p1", UsageSnapshot{
		UsageDelta: UsageDelta{InputTokens: 10, OutputTokens: 20},
		Usage:      map[string]any{"prompt_tokens": 10.0, "completion_tokens": 20.0, "total_tokens": 30.0},
		CostMicros: 3_000_000,
		HasCost:    true,
	}, newer); err != nil {
		t.Fatalf("RecordUsage after delete: %v", err)
	}

	if err := restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, ok := store.ProviderSnapshot("openai", "p1")
	if !ok {
		t.Fatalf("expected restored provider snapshot")
	}
	if got.RequestCount != 2 || got.SuccessCount != 2 || got.TotalTokens != 33 {
		t.Fatalf("restored snapshot = %#v", got)
	}
	if !got.HasCost || got.TotalCostMicros != 4_250_000 {
		t.Fatalf("restored spend = %#v", got)
	}
	if !got.LastUsedAt.Equal(newer) {
		t.Fatalf("last_used_at = %v want %v", got.LastUsedAt, newer)
	}
	if int64Value(got.Usage["total_tokens"]) != 30 {
		t.Fatalf("usage = %#v", got.Usage)
	}

	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}
	if got, ok := reloaded.ProviderSnapshot("openai", "p1"); !ok || got.TotalTokens != 33 {
		t.Fatalf("reloaded snapshot = %#v ok=%v", got, ok)
	}
}
