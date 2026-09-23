package pricing

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCatalogMetadataMatchesVendoredSnapshot(t *testing.T) {
	metadata := CatalogMetadata()
	if metadata.Version == "" {
		t.Fatalf("version = %q", metadata.Version)
	}
	if metadata.UpstreamSource != "litellm" || metadata.UpstreamSHA == "" || metadata.GeneratedAt == "" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if len(metadata.ModelSpecsCommit) != 40 {
		t.Fatalf("model_specs_commit = %q", metadata.ModelSpecsCommit)
	}
}

func TestCatalogIntegrityRejectsModifiedContent(t *testing.T) {
	if err := verifyCatalogIntegrity(embeddedCatalog, embeddedCatalogIntegrity); err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), embeddedCatalog...)
	tampered[len(tampered)/2] ^= 1
	if err := verifyCatalogIntegrity(tampered, embeddedCatalogIntegrity); err == nil {
		t.Fatal("modified catalog passed integrity verification")
	}
}

func TestUpdateScriptDoesNotExecuteUpstreamCode(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required")
	}
	_, currentFile, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	repo := filepath.Join(t.TempDir(), "model-specs")
	destination := filepath.Join(t.TempDir(), "clipal")
	for _, dir := range []string{filepath.Join(repo, "dist"), filepath.Join(destination, "internal", "pricing")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(t.TempDir(), "upstream-code-ran")
	files := map[string][]byte{
		"package.json":      []byte(`{"scripts":{"build":"node build.mjs","validate":"node build.mjs"}}`),
		"build.mjs":         []byte(`import fs from "node:fs"; fs.writeFileSync(process.env.UPSTREAM_MARKER, "executed");`),
		"dist/catalog.json": embeddedCatalog,
		"dist/catalog.sha":  []byte(strings.TrimSpace(embeddedCatalogVersion) + "\n"),
		"LICENSE":           []byte("fixture license\n"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(repo, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(env []string, args ...string) ([]byte, error) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), env...)
		return cmd.CombinedOutput()
	}
	for _, args := range [][]string{{"git", "init", "-q"}, {"git", "config", "user.email", "test@example.com"}, {"git", "config", "user.name", "Test"}, {"git", "add", "."}, {"git", "commit", "-qm", "fixture"}} {
		if output, err := run(nil, args...); err != nil {
			t.Fatalf("%v: %v: %s", args, err, output)
		}
	}
	currentRef := func(revision string) string {
		output, err := run(nil, "git", "rev-parse", revision)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(output))
	}
	commitFixture := func(message string) string {
		for _, args := range [][]string{{"git", "add", "dist/catalog.json", "dist/catalog.sha"}, {"git", "commit", "-qm", message}} {
			if output, err := run(nil, args...); err != nil {
				t.Fatalf("%v: %v: %s", args, err, output)
			}
		}
		return currentRef("HEAD")
	}
	script := filepath.Join(root, "scripts", "update_model_specs.sh")
	runUpdater := func(ref string) ([]byte, error) {
		cmd := exec.Command("bash", script)
		cmd.Env = append(os.Environ(), "MODEL_SPECS_REPO="+repo, "MODEL_SPECS_REF="+ref, "CLIPAL_ROOT_DIR="+destination, "UPSTREAM_MARKER="+marker)
		return cmd.CombinedOutput()
	}
	if output, err := runUpdater(currentRef("HEAD")); err != nil {
		t.Fatalf("clean update failed: %v: %s", err, output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("upstream code was executed: %v", err)
	}
	if output, err := runUpdater(currentRef("HEAD^{tree}")); err == nil {
		t.Fatalf("tree object was accepted as a commit: %s", output)
	}

	if err := os.WriteFile(filepath.Join(repo, "dist", "catalog.sha"), []byte("a8be6f 9ad46d\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := runUpdater(commitFixture("invalid sha whitespace")); err == nil {
		t.Fatalf("catalog.sha with internal whitespace was accepted: %s", output)
	}
	if err := os.WriteFile(filepath.Join(repo, "dist", "catalog.sha"), []byte(strings.TrimSpace(embeddedCatalogVersion)+"\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := runUpdater(commitFixture("invalid sha extra newline")); err == nil {
		t.Fatalf("catalog.sha with extra newline was accepted: %s", output)
	}

	if err := os.WriteFile(filepath.Join(repo, "dist", "catalog.json"), embeddedCatalog, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "dist", "catalog.sha"), []byte(strings.TrimSpace(embeddedCatalogVersion)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := run(nil, "node", "-e", `const fs=require("fs");const p="dist/catalog.json";const c=JSON.parse(fs.readFileSync(p));c.version=" "+c.version;fs.writeFileSync(p,JSON.stringify(c));`); err != nil {
		t.Fatalf("mutate version whitespace: %v: %s", err, output)
	}
	if output, err := runUpdater(commitFixture("invalid version whitespace")); err == nil {
		t.Fatalf("catalog JSON version whitespace was accepted: %s", output)
	}

	if err := os.WriteFile(filepath.Join(repo, "dist", "catalog.json"), embeddedCatalog, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "dist", "catalog.sha"), []byte(strings.TrimSpace(embeddedCatalogVersion)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := run(nil, "node", "-e", `const fs=require("fs");const p="dist/catalog.json";const c=JSON.parse(fs.readFileSync(p));c.generated_at="";fs.writeFileSync(p,JSON.stringify(c));`); err != nil {
		t.Fatalf("mutate empty metadata: %v: %s", err, output)
	}
	if output, err := runUpdater(commitFixture("invalid empty metadata")); err == nil {
		t.Fatalf("empty required metadata was accepted: %s", output)
	}

	if err := os.WriteFile(filepath.Join(repo, "dist", "catalog.json"), embeddedCatalog, 0o644); err != nil {
		t.Fatal(err)
	}
	mutation := `const fs=require("fs"),crypto=require("crypto");const p="dist/catalog.json",s="dist/catalog.sha";const c=JSON.parse(fs.readFileSync(p));c.models[Object.keys(c.models)[0]]=null;c.version=crypto.createHash("sha256").update(JSON.stringify({models:c.models,clients:c.clients})).digest("hex").slice(0,12);fs.writeFileSync(p,JSON.stringify(c));fs.writeFileSync(s,c.version+"\n");`
	if output, err := run(nil, "node", "-e", mutation); err != nil {
		t.Fatalf("mutate invalid model entry: %v: %s", err, output)
	}
	if output, err := runUpdater(commitFixture("invalid model entry")); err == nil {
		t.Fatalf("non-object model entry was accepted: %s", output)
	}

	if err := os.WriteFile(filepath.Join(repo, "dist", "catalog.json"), embeddedCatalog, 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := run(nil, "node", "-e", `const fs=require("fs");const p="dist/catalog.json";const c=JSON.parse(fs.readFileSync(p));c.models[Object.keys(c.models)[0]]={};fs.writeFileSync(p,JSON.stringify(c));`); err != nil {
		t.Fatalf("mutate empty model entry: %v: %s", err, output)
	}
	if output, err := runUpdater(commitFixture("empty model entry")); err == nil {
		t.Fatalf("empty model entry was accepted: %s", output)
	}
}

func TestCostMicrosUsesExactModelInsteadOfFamilyPrefix(t *testing.T) {
	usage := Usage{UncachedInputTokens: 100_000, OutputTokens: 20_000}
	standard, ok := CostMicros("gpt-5.4", PlatformOpenAI, usage)
	if !ok || standard != 550_000 {
		t.Fatalf("gpt-5.4 cost = %d, ok = %v", standard, ok)
	}
	pro, ok := CostMicros("gpt-5.4-pro", PlatformOpenAI, usage)
	if !ok || pro != 6_600_000 {
		t.Fatalf("gpt-5.4-pro cost = %d, ok = %v", pro, ok)
	}
	if _, ok := CostMicros("gpt-5.4-unknown", PlatformOpenAI, usage); ok {
		t.Fatal("unexpected prefix-based model match")
	}
}

func TestCostMicrosNormalizesProtocolWrapperAndChecksPlatform(t *testing.T) {
	usage := Usage{UncachedInputTokens: 1_000, OutputTokens: 200}
	cost, ok := CostMicros("models/gemini-3.1-pro", PlatformGemini, usage)
	if !ok || cost != 4_400 {
		t.Fatalf("cost = %d, ok = %v", cost, ok)
	}
	if _, ok := CostMicros("models/gemini-3.1-pro", PlatformOpenAI, usage); !ok {
		t.Fatal("OpenAI-compatible protocol did not resolve Gemini price")
	}
}

func TestCostMicrosSupportsOpenAICompatiblePricePlatforms(t *testing.T) {
	usage := Usage{UncachedInputTokens: 1_000_000, OutputTokens: 1_000_000}
	cost, ok := CostMicros("deepseek-chat", PlatformOpenAI, usage)
	if !ok || cost != 700_000 {
		t.Fatalf("deepseek-chat cost = %d, ok = %v", cost, ok)
	}
	if _, ok := CostMicros("deepseek-chat", PlatformAnthropic, usage); ok {
		t.Fatal("Claude protocol resolved a non-Anthropic platform")
	}
}

func TestCostMicrosUsesLongContextAndServiceTierRates(t *testing.T) {
	usage := Usage{
		UncachedInputTokens: 250_000,
		CachedInputTokens:   50_000,
		OutputTokens:        10_000,
		ServiceTier:         ServiceTierPriority,
	}
	cost, ok := CostMicros("azure/gpt-5.5", PlatformOpenAI, usage)
	if !ok || cost != 6_000_000 {
		t.Fatalf("cost = %d, ok = %v", cost, ok)
	}
}

func TestCostMicrosRejectsAmbiguousLongContextServiceTier(t *testing.T) {
	usage := Usage{UncachedInputTokens: 300_000, OutputTokens: 10_000, ServiceTier: ServiceTierPriority}
	if _, ok := CostMicros("gpt-5.5", PlatformOpenAI, usage); ok {
		t.Fatal("priced request without priority plus long-context combination rates")
	}
}

func TestCostMicrosUsesBatchCacheAndLongContextBatchRates(t *testing.T) {
	usage := Usage{UncachedInputTokens: 100_000, CachedInputTokens: 100_000, OutputTokens: 10_000, ServiceTier: ServiceTierBatch}
	// Batch rates: input $1, cache read $0.10, output $5 per MTok.
	if cost, ok := CostMicros("gpt-6-sol", PlatformOpenAI, usage); !ok || cost != 160_000 {
		t.Fatalf("batch cost = %d, ok = %v", cost, ok)
	}
	usage.UncachedInputTokens = 300_000
	// Batch above 272K: input $2, cache read $0.20, output $7.50 per MTok.
	if cost, ok := CostMicros("gpt-6-sol", PlatformOpenAI, usage); !ok || cost != 695_000 {
		t.Fatalf("long-context batch cost = %d, ok = %v", cost, ok)
	}
}

func TestCostMicrosFallsBackToInputRateForCacheRead(t *testing.T) {
	cost, ok := CostMicros("gpt-3.5-turbo", PlatformOpenAI, Usage{CachedInputTokens: 1_000})
	if !ok || cost != 500 {
		t.Fatalf("cache fallback cost = %d, ok = %v", cost, ok)
	}
}

func TestRatesForUsageRejectsUnpricedLongTierCombinations(t *testing.T) {
	r := func(value string) rate {
		parsed, err := decimalRat(value)
		if err != nil {
			t.Fatal(err)
		}
		return rate{value: parsed}
	}
	tests := []struct {
		name  string
		spec  modelSpec
		usage Usage
	}{
		{name: "batch above 272k", spec: modelSpec{Input: r("1"), BatchInput: r(".5"), Above272KInput: r("2")}, usage: Usage{UncachedInputTokens: 300_000, ServiceTier: ServiceTierBatch}},
		{name: "flex above 200k", spec: modelSpec{Input: r("1"), FlexInput: r(".5"), Above200KInput: r("2")}, usage: Usage{UncachedInputTokens: 250_000, ServiceTier: ServiceTierFlex}},
		{name: "priority cache read partial combination", spec: modelSpec{Input: r("1"), CacheRead: r(".1"), PriorityCacheRead: r(".2"), Above272KInput: r("2"), Above272KCacheRead: r(".3"), Above272KPriorityInput: r("4")}, usage: Usage{UncachedInputTokens: 280_000, CachedInputTokens: 1, ServiceTier: ServiceTierPriority}},
		{name: "flex cache write partial combination", spec: modelSpec{Input: r("1"), CacheWrite: r("1.25"), FlexCacheWrite: r(".75"), Above272KInput: r("2"), Above272KCacheWrite: r("2.5"), Above272KFlexInput: r("1")}, usage: Usage{UncachedInputTokens: 280_000, CacheWriteTokens: 1, ServiceTier: ServiceTierFlex}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := ratesForUsage(tc.spec, tc.usage); ok {
				t.Fatal("accepted ambiguous price combination")
			}
		})
	}
}

func TestCostMicrosUsesOneHourCacheWriteRate(t *testing.T) {
	usage := Usage{CacheWrite1HTokens: 1_000_000}
	cost, ok := CostMicros("claude-opus-4-6", PlatformAnthropic, usage)
	if !ok || cost != 10_000_000 {
		t.Fatalf("one-hour cache cost = %d, ok = %v", cost, ok)
	}
}

func TestCostMicrosScalesOneHourCacheWriteForLongContext(t *testing.T) {
	usage := Usage{CacheWrite1HTokens: 1_000_000}
	cost, ok := CostMicros("claude-sonnet-4-5", PlatformAnthropic, usage)
	if !ok || cost != 12_000_000 {
		t.Fatalf("long one-hour cache cost = %d, ok = %v", cost, ok)
	}
}

func TestCostMicrosPrefersProviderSpecificEntry(t *testing.T) {
	usage := Usage{UncachedInputTokens: 1_000_000, OutputTokens: 1_000_000}
	cost, ok := CostMicrosForProvider("deepseek/deepseek-chat", PlatformOpenAI, "openrouter", usage)
	if !ok || cost != 1_210_000 {
		t.Fatalf("OpenRouter cost = %d, ok = %v", cost, ok)
	}
}

func TestCostMicrosUsesCacheAudioReasoningAndSearchRates(t *testing.T) {
	usage := Usage{
		UncachedInputTokens: 10_000,
		AudioInputTokens:    4_000,
		CachedInputTokens:   3_000,
		OutputTokens:        2_000,
		ReasoningTokens:     500,
		SearchQueries:       2,
		SearchContextSize:   "medium",
	}
	cost, ok := CostMicros("gemini-2.5-flash", PlatformGemini, usage)
	if !ok || cost != 80_890 {
		t.Fatalf("cost = %d, ok = %v", cost, ok)
	}
}

func TestCostMicrosRejectsPartialPriceWhenSearchRateIsMissing(t *testing.T) {
	_, spec, ok := resolveModelWithProvider("gpt-3.5-turbo", PlatformOpenAI, "")
	if !ok {
		t.Fatal("expected priced fixture model")
	}
	if len(spec.SearchContext) != 0 {
		t.Fatal("fixture unexpectedly has a search price")
	}
	if _, ok := CostMicros("gpt-3.5-turbo", PlatformOpenAI, Usage{UncachedInputTokens: 1, SearchQueries: 1, SearchContextSize: "medium"}); ok {
		t.Fatal("accepted a partial price without the reported search charge")
	}
}

func TestCostMicrosRejectsNegativeUsageInsteadOfPartiallyPricing(t *testing.T) {
	if _, ok := CostMicros("gpt-3.5-turbo", PlatformOpenAI, Usage{UncachedInputTokens: -1, OutputTokens: 100}); ok {
		t.Fatal("accepted negative usage and partially priced the request")
	}
	if _, ok := CostMicros("gemini-2.5-flash", PlatformGemini, Usage{UncachedInputTokens: 1, AudioInputTokens: 2}); ok {
		t.Fatal("accepted audio input larger than total uncached input")
	}
	if _, ok := CostMicros("gpt-5.4", PlatformOpenAI, Usage{OutputTokens: 1, ReasoningTokens: 2}); ok {
		t.Fatal("accepted reasoning output larger than total output")
	}
}

func TestDecimalRatParsesScientificNotationExactly(t *testing.T) {
	value, err := decimalRat("1.75e-7")
	if err != nil {
		t.Fatal(err)
	}
	if got := value.RatString(); got != "7/40000000" {
		t.Fatalf("value = %s", got)
	}
}

func TestDollarsToMicrosRoundsExactlyAndRejectsOverflow(t *testing.T) {
	if got, ok := DollarsToMicros("0.0001245"); !ok || got != 125 {
		t.Fatalf("half-micro rounding = %d, ok = %v", got, ok)
	}
	if _, ok := DollarsToMicros("9223372036854.776"); ok {
		t.Fatal("accepted amount above MaxInt64 micros")
	}
	if _, ok := DollarsToMicros("NaN"); ok {
		t.Fatal("accepted NaN")
	}
	for _, raw := range []string{"1e100000000", "1e19", "1e-19", "1e2junk", "1e-9223372036854775808", strings.Repeat("1", 65)} {
		if _, ok := DollarsToMicros(raw); ok {
			t.Fatalf("accepted resource-intensive amount %q", raw)
		}
	}
}
