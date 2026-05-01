// Usage extraction from session transcripts (PRI-31).
//
// Each Claude session .jsonl carries a per-message `usage` block:
//
//   {"type": "assistant", "message": {"model": "claude-...", "usage": {
//     "input_tokens": 12, "output_tokens": 34,
//     "cache_creation_input_tokens": 0, "cache_read_input_tokens": 56
//   }}, ...}
//
// We sum every assistant turn's tokens and pin the session's model to
// the latest one we see — sessions that drift across models still get
// scored against the model the user actually shipped with.
//
// ReadClaudeUsage caches by (path, mtime) so repeated calls inside one
// process avoid the multi-MB scan; the cache is in-memory only,
// rebuilt on every launch (PRI-31 deferred a state.json persistence
// follow-up). Errors at scan time degrade gracefully: a corrupt line
// is skipped, a missing file returns a zero Usage with the I/O error.

package parse

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"

	"github.com/mi-subbotin/lazyagent/internal/pricing"
)

type usageCacheEntry struct {
	mtime int64
	usage pricing.Usage
}

var (
	usageCacheMu sync.Mutex
	usageCache   = map[string]usageCacheEntry{}
)

// ReadClaudeUsage walks a Claude .jsonl session and returns the
// summed per-message usage plus the most recently observed model
// string. Cached by (path, mtime) — repeat calls in the same process
// return without re-reading the file.
func ReadClaudeUsage(path string) (pricing.Usage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return pricing.Usage{}, err
	}
	mtime := info.ModTime().Unix()

	usageCacheMu.Lock()
	if hit, ok := usageCache[path]; ok && hit.mtime == mtime {
		usageCacheMu.Unlock()
		return hit.usage, nil
	}
	usageCacheMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return pricing.Usage{}, err
	}
	defer f.Close()

	var u pricing.Usage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Model string `json:"model"`
				Usage struct {
					InputTokens              int64 `json:"input_tokens"`
					OutputTokens             int64 `json:"output_tokens"`
					CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Type != "assistant" {
			continue
		}
		if rec.Message.Model != "" {
			u.Model = rec.Message.Model
		}
		u.InputTokens += rec.Message.Usage.InputTokens
		u.OutputTokens += rec.Message.Usage.OutputTokens
		u.CacheCreateTokens += rec.Message.Usage.CacheCreationInputTokens
		u.CacheReadTokens += rec.Message.Usage.CacheReadInputTokens
		u.Messages++
	}
	if err := sc.Err(); err != nil {
		// Partial usage from a long file is still useful — surface the
		// error so the caller can log it but keep the totals we got.
		return u, err
	}

	usageCacheMu.Lock()
	usageCache[path] = usageCacheEntry{mtime: mtime, usage: u}
	usageCacheMu.Unlock()
	return u, nil
}
