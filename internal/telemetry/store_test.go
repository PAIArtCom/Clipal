package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	if got.Usage["total_tokens"] != float64(30) {
		t.Fatalf("usage = %#v", got.Usage)
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
	if got.Usage["total_tokens"] != float64(30) {
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
