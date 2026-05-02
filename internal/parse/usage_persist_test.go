package parse

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/pricing"
)

// withTempUsageCachePath redirects usageCachePath to a tempdir for
// the duration of the test, restoring the original on cleanup.
func withTempUsageCachePath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "usage_cache.json")
	orig := usageCachePath
	usageCachePath = func() (string, error) { return path, nil }
	t.Cleanup(func() { usageCachePath = orig })
	return path
}

func TestUsageCacheRoundTrip(t *testing.T) {
	path := withTempUsageCachePath(t)
	ResetUsageCacheForTesting()
	t.Cleanup(ResetUsageCacheForTesting)

	// Seed each in-memory cache.
	usageCacheMu.Lock()
	usageCache["/claude/x.jsonl"] = usageCacheEntry{
		mtime: 100,
		usage: pricing.Usage{Model: "claude-sonnet-4-6", InputTokens: 1000, OutputTokens: 200, Messages: 5},
	}
	usageCacheMu.Unlock()
	codexUsageCacheMu.Lock()
	codexUsageCache["/codex/r.jsonl"] = usageCacheEntry{
		mtime: 200,
		usage: pricing.Usage{Model: "gpt-5.5", InputTokens: 500, CacheReadTokens: 1500, OutputTokens: 50},
	}
	codexUsageCacheMu.Unlock()
	geminiUsageCacheMu.Lock()
	geminiUsageCache["/gemini/s.json"] = geminiUsageCacheEntry{
		mtime: 300,
		usages: []pricing.Usage{
			{Model: "gemini-3-pro-preview", InputTokens: 100, OutputTokens: 10},
			{Model: "gemini-2.5-flash", InputTokens: 50, OutputTokens: 5},
		},
	}
	geminiUsageCacheMu.Unlock()

	if err := SaveUsageCache(); err != nil {
		t.Fatalf("SaveUsageCache: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected cache file at %s: %v", path, err)
	}

	// Wipe in-memory and reload from disk.
	ResetUsageCacheForTesting()
	if err := LoadUsageCache(); err != nil {
		t.Fatalf("LoadUsageCache: %v", err)
	}

	usageCacheMu.Lock()
	got := usageCache["/claude/x.jsonl"]
	usageCacheMu.Unlock()
	if got.mtime != 100 || got.usage.InputTokens != 1000 {
		t.Errorf("Claude entry not restored: %+v", got)
	}

	codexUsageCacheMu.Lock()
	gotC := codexUsageCache["/codex/r.jsonl"]
	codexUsageCacheMu.Unlock()
	if gotC.usage.CacheReadTokens != 1500 || gotC.usage.Model != "gpt-5.5" {
		t.Errorf("Codex entry not restored: %+v", gotC)
	}

	geminiUsageCacheMu.Lock()
	gotG := geminiUsageCache["/gemini/s.json"]
	geminiUsageCacheMu.Unlock()
	if len(gotG.usages) != 2 || gotG.usages[1].Model != "gemini-2.5-flash" {
		t.Errorf("Gemini entry not restored: %+v", gotG)
	}
}

func TestLoadUsageCacheMissingIsNoop(t *testing.T) {
	withTempUsageCachePath(t)
	ResetUsageCacheForTesting()
	t.Cleanup(ResetUsageCacheForTesting)
	if err := LoadUsageCache(); err != nil {
		t.Errorf("missing file should be silently ignored, got %v", err)
	}
}

func TestSaveUsageCacheEmptyDoesNotCreateFile(t *testing.T) {
	path := withTempUsageCachePath(t)
	ResetUsageCacheForTesting()
	t.Cleanup(ResetUsageCacheForTesting)
	if err := SaveUsageCache(); err != nil {
		t.Errorf("SaveUsageCache (empty): %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected no file when nothing to save, got err=%v", err)
	}
}

func TestLoadUsageCacheVersionMismatchIsNoop(t *testing.T) {
	path := withTempUsageCachePath(t)
	ResetUsageCacheForTesting()
	t.Cleanup(ResetUsageCacheForTesting)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadUsageCache(); err != nil {
		t.Errorf("version mismatch should not error: %v", err)
	}
	usageCacheMu.Lock()
	defer usageCacheMu.Unlock()
	if len(usageCache) != 0 {
		t.Errorf("version-mismatched file must not populate cache")
	}
}
