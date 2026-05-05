// Package usage scans per-tool session logs for mentions of item names
// and stamps each item with the most recent file mtime that referenced
// it. The TUI reads Item.LastSeen to render the "(unused Nd)" badge.
//
// Scanning strategy: every session file's raw bytes are searched for
// the JSON-quoted token `"<item.Name>"`. False positives are accepted
// in v1 — we err on "looks recent" rather than "wrongly unused". File
// mtime is used as a coarse approximation of when the mention occurred;
// per-line timestamps are out of scope.
//
// Cache: ~/.lazyagent/usage.json keyed by (path, mtime). On a 1h TTL,
// unchanged session files are reused without re-scanning.
package usage

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// cacheTTL bounds how long a cache file is reused before its session
// file list is re-walked from disk. Per-file mtime checks happen
// regardless; the TTL just forces a re-discovery in case new session
// files appeared.
const cacheTTL = time.Hour

// fileEntry is one cached session file's scan result.
type fileEntry struct {
	Path             string               `json:"path"`
	ModTime          time.Time            `json:"mtime"`
	LastSeenPerName  map[string]time.Time `json:"last_seen_per_name"`
}

type cacheFile struct {
	SessionFiles []fileEntry `json:"session_files"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

// LoadLastSeen mutates items in place, setting LastSeen to the most
// recent time any scanned session file mentioned the item's Name.
// Items with empty Name, KindSession, or KindMemory are skipped.
// Errors are non-fatal — partial data is preferable to a missing badge.
func LoadLastSeen(items []model.Item) error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return err
	}
	return loadLastSeen(items, home)
}

func loadLastSeen(items []model.Item, home string) error {
	candidates := candidateItems(items)
	if len(candidates) == 0 {
		return nil
	}
	files, err := discoverSessionFiles(home)
	if err != nil {
		return err
	}
	cachePath := filepath.Join(home, ".lazyagent", "usage.json")
	cache := readCache(cachePath)

	cachedByPath := map[string]fileEntry{}
	for _, e := range cache.SessionFiles {
		cachedByPath[e.Path] = e
	}

	names := make([]string, 0, len(candidates))
	for _, idx := range candidates {
		names = append(names, items[idx].Name)
	}

	results := scanFiles(files, names, cachedByPath)

	maxByName := map[string]time.Time{}
	for _, e := range results {
		for n, t := range e.LastSeenPerName {
			if cur, ok := maxByName[n]; !ok || t.After(cur) {
				maxByName[n] = t
			}
		}
	}
	for _, idx := range candidates {
		if t, ok := maxByName[items[idx].Name]; ok {
			items[idx].LastSeen = t
		}
	}

	writeCache(cachePath, cacheFile{SessionFiles: sortedEntries(results), UpdatedAt: time.Now()})
	return nil
}

// candidateItems returns indices of items eligible for last-seen
// stamping. Sessions and memory files have no "name" the way a skill
// or agent does, so scanning them would just chase noise.
func candidateItems(items []model.Item) []int {
	out := make([]int, 0, len(items))
	for i := range items {
		it := &items[i]
		if it.Name == "" {
			continue
		}
		if it.Kind == model.KindSession || it.Kind == model.KindMemory {
			continue
		}
		out = append(out, i)
	}
	return out
}

// discoverSessionFiles walks every supported tool's session-log
// location and returns the list of files to scan. Codex sessions are
// skipped pending a SQLite/JSONL transcript parser — see PRI-95 follow-up.
func discoverSessionFiles(home string) ([]string, error) {
	var out []string
	out = append(out, claudeSessionFiles(home)...)
	out = append(out, geminiSessionFiles(home)...)
	// TODO(PRI-95 follow-up): codex sessions live in
	// ~/.codex/state_5.sqlite (metadata) plus per-day rollout JSONL
	// files under ~/.codex/sessions/. The metadata DB carries no
	// transcript; the rollout JSONL would scan but routing them by
	// item.Origin is non-trivial. Skipped in this PR.
	return out, nil
}

func claudeSessionFiles(home string) []string {
	root := filepath.Join(home, ".claude", "projects")
	var out []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		projDir := filepath.Join(root, e.Name())
		_ = filepath.WalkDir(projDir, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if filepath.Ext(p) == ".jsonl" {
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

func geminiSessionFiles(home string) []string {
	tmpDir := filepath.Join(home, ".gemini", "tmp")
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		bucket := filepath.Join(tmpDir, e.Name())
		chats := filepath.Join(bucket, "chats")
		if files, err := os.ReadDir(chats); err == nil {
			for _, f := range files {
				if !f.IsDir() && filepath.Ext(f.Name()) == ".json" {
					out = append(out, filepath.Join(chats, f.Name()))
				}
			}
		}
		logs := filepath.Join(bucket, "logs.json")
		if info, err := os.Stat(logs); err == nil && !info.IsDir() {
			out = append(out, logs)
		}
	}
	return out
}

// scanFiles scans all session files concurrently. Each file's result
// reuses the cache when (path, mtime) is unchanged; otherwise the file
// is re-read and substring-matched against every candidate name.
func scanFiles(files []string, names []string, cache map[string]fileEntry) []fileEntry {
	out := make([]fileEntry, len(files))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		mt := info.ModTime()
		if cached, ok := cache[path]; ok && cached.ModTime.Equal(mt) {
			out[i] = cached
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, path string, mt time.Time) {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = scanFile(path, mt, names)
		}(i, path, mt)
	}
	wg.Wait()
	filtered := out[:0]
	for _, e := range out {
		if e.Path != "" {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

func scanFile(path string, mt time.Time, names []string) fileEntry {
	entry := fileEntry{Path: path, ModTime: mt, LastSeenPerName: map[string]time.Time{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return entry
	}
	for _, n := range names {
		if n == "" {
			continue
		}
		quoted := []byte(`"` + n + `"`)
		if bytes.Contains(data, quoted) {
			entry.LastSeenPerName[n] = mt
		}
	}
	return entry
}

func readCache(path string) cacheFile {
	var c cacheFile
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return c
		}
		return c
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return cacheFile{}
	}
	if !c.UpdatedAt.IsZero() && time.Since(c.UpdatedAt) > cacheTTL {
		// Keep per-file entries — the per-file mtime check still
		// gates reuse — but a TTL miss is fine, callers re-walk
		// regardless.
		return c
	}
	return c
}

func writeCache(path string, c cacheFile) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".usage.*.json")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return
	}
	_ = os.Rename(tmpPath, path)
}

func sortedEntries(in []fileEntry) []fileEntry {
	out := append([]fileEntry(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
