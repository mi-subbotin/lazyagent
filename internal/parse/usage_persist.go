// Persistent usage cache (PRI-63 Phase 3).
//
// In-memory caches in usage.go / usage_codex.go / usage_gemini.go save
// rescans within a process. Across launches we'd otherwise re-walk
// every multi-MB session jsonl — slow when a heavy user has hundreds
// of transcripts. This file persists the (path → mtime → Usage) maps
// to ~/.lazyagent/usage_cache.json on demand.
//
// Best-effort everywhere: a missing / corrupt file collapses to empty
// and we keep going. Writes are atomic (tmp + rename) so a SIGKILL
// can't half-write the file.

package parse

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mi-subbotin/lazyagent/internal/pricing"
)

type usageCacheDisk struct {
	Version int                                       `json:"version"`
	Claude  map[string]usageCacheDiskEntry            `json:"claude,omitempty"`
	Codex   map[string]usageCacheDiskEntry            `json:"codex,omitempty"`
	Gemini  map[string]usageCacheDiskEntryMulti       `json:"gemini,omitempty"`
}

type usageCacheDiskEntry struct {
	Mtime int64         `json:"mtime"`
	Usage pricing.Usage `json:"usage"`
}

type usageCacheDiskEntryMulti struct {
	Mtime  int64           `json:"mtime"`
	Usages []pricing.Usage `json:"usages"`
}

// usageCachePath returns ~/.lazyagent/usage_cache.json (test-overridable).
var usageCachePath = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lazyagent", "usage_cache.json"), nil
}

// LoadUsageCache hydrates the three in-memory caches from disk.
// Missing file → no-op, no error. Mismatched version → no-op.
func LoadUsageCache() error {
	path, err := usageCachePath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var disk usageCacheDisk
	if err := json.Unmarshal(data, &disk); err != nil {
		return err
	}
	if disk.Version != 1 {
		return nil
	}
	usageCacheMu.Lock()
	for k, v := range disk.Claude {
		usageCache[k] = usageCacheEntry{mtime: v.Mtime, usage: v.Usage}
	}
	usageCacheMu.Unlock()
	codexUsageCacheMu.Lock()
	for k, v := range disk.Codex {
		codexUsageCache[k] = usageCacheEntry{mtime: v.Mtime, usage: v.Usage}
	}
	codexUsageCacheMu.Unlock()
	geminiUsageCacheMu.Lock()
	for k, v := range disk.Gemini {
		geminiUsageCache[k] = geminiUsageCacheEntry{mtime: v.Mtime, usages: v.Usages}
	}
	geminiUsageCacheMu.Unlock()
	return nil
}

// SaveUsageCache snapshots the in-memory caches to disk atomically.
// Stale entries (paths whose files were since deleted) are kept —
// they're harmless and a future read will replace them; pruning would
// require an O(N) stat sweep on every save.
func SaveUsageCache() error {
	path, err := usageCachePath()
	if err != nil {
		return err
	}
	disk := usageCacheDisk{Version: 1}

	usageCacheMu.Lock()
	disk.Claude = make(map[string]usageCacheDiskEntry, len(usageCache))
	for k, v := range usageCache {
		disk.Claude[k] = usageCacheDiskEntry{Mtime: v.mtime, Usage: v.usage}
	}
	usageCacheMu.Unlock()

	codexUsageCacheMu.Lock()
	disk.Codex = make(map[string]usageCacheDiskEntry, len(codexUsageCache))
	for k, v := range codexUsageCache {
		disk.Codex[k] = usageCacheDiskEntry{Mtime: v.mtime, Usage: v.usage}
	}
	codexUsageCacheMu.Unlock()

	geminiUsageCacheMu.Lock()
	disk.Gemini = make(map[string]usageCacheDiskEntryMulti, len(geminiUsageCache))
	for k, v := range geminiUsageCache {
		disk.Gemini[k] = usageCacheDiskEntryMulti{Mtime: v.mtime, Usages: v.usages}
	}
	geminiUsageCacheMu.Unlock()

	if len(disk.Claude) == 0 && len(disk.Codex) == 0 && len(disk.Gemini) == 0 {
		// Nothing to save. Don't create an empty file.
		return nil
	}

	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".usage-cache-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// ResetUsageCacheForTesting clears all three in-memory caches.
// Tests use this to isolate cache state between runs.
func ResetUsageCacheForTesting() {
	usageCacheMu.Lock()
	usageCache = map[string]usageCacheEntry{}
	usageCacheMu.Unlock()
	codexUsageCacheMu.Lock()
	codexUsageCache = map[string]usageCacheEntry{}
	codexUsageCacheMu.Unlock()
	geminiUsageCacheMu.Lock()
	geminiUsageCache = map[string]geminiUsageCacheEntry{}
	geminiUsageCacheMu.Unlock()
}
