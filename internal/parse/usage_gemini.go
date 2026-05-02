// Gemini transcript usage extraction (PRI-63 Phase 2).
//
// Gemini stores each session as a single .json file at
// ~/.gemini/tmp/<projectHash>/chats/session-*.json. Token usage is
// per-message: every message has an optional `tokens` block plus a
// `model` field. Models can change mid-conversation (a session that
// started on gemini-3-pro-preview may finish on gemini-2.5-flash), so
// summing into a single Usage would price the whole transcript against
// the wrong rate. We aggregate per model and let the pricing layer
// sum the per-model costs.
//
// Token semantics (verified empirically):
//   - tokens.input includes tokens.cached (cache_read is a subset)
//   - tokens.thoughts is reasoning, billed at output rate
//   - tokens.total = input + output + thoughts (cached is *within* input)
//
// Mapping into pricing.Usage:
//   InputTokens     = input - cached     (uncached input bills at full)
//   CacheReadTokens = cached             (bills at cache_read rate)
//   OutputTokens    = output + thoughts  (reasoning charged as output)

package parse

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/mi-subbotin/lazyagent/internal/pricing"
)

type geminiUsageCacheEntry struct {
	mtime  int64
	usages []pricing.Usage
}

var (
	geminiUsageCacheMu sync.Mutex
	geminiUsageCache   = map[string]geminiUsageCacheEntry{}
)

// ReadGeminiUsage walks a Gemini session JSON and returns one
// pricing.Usage per model that appeared in the transcript. Models are
// returned in last-seen order — the final entry is the one to surface
// as the session's "current" model. Cached by (path, mtime).
func ReadGeminiUsage(path string) ([]pricing.Usage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	mtime := info.ModTime().Unix()

	geminiUsageCacheMu.Lock()
	if hit, ok := geminiUsageCache[path]; ok && hit.mtime == mtime {
		geminiUsageCacheMu.Unlock()
		return hit.usages, nil
	}
	geminiUsageCacheMu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Messages []struct {
			Model  string `json:"model"`
			Tokens *struct {
				Input    int64 `json:"input"`
				Output   int64 `json:"output"`
				Cached   int64 `json:"cached"`
				Thoughts int64 `json:"thoughts"`
				Tool     int64 `json:"tool"`
				Total    int64 `json:"total"`
			} `json:"tokens"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}

	// Preserve first-seen ordering of models so the *last* entry in the
	// returned slice is the most recently used model in the session.
	type bucket struct {
		idx int
		u   pricing.Usage
	}
	byModel := map[string]*bucket{}
	order := []string{}
	for _, m := range doc.Messages {
		if m.Tokens == nil || m.Model == "" {
			continue
		}
		b, ok := byModel[m.Model]
		if !ok {
			b = &bucket{idx: len(order), u: pricing.Usage{Model: m.Model}}
			byModel[m.Model] = b
			order = append(order, m.Model)
		}
		cached := m.Tokens.Cached
		if cached > m.Tokens.Input {
			cached = m.Tokens.Input
		}
		b.u.InputTokens += m.Tokens.Input - cached
		b.u.CacheReadTokens += cached
		b.u.OutputTokens += m.Tokens.Output + m.Tokens.Thoughts
		b.u.Messages++
	}

	out := make([]pricing.Usage, 0, len(order))
	for _, name := range order {
		out = append(out, byModel[name].u)
	}

	geminiUsageCacheMu.Lock()
	geminiUsageCache[path] = geminiUsageCacheEntry{mtime: mtime, usages: out}
	geminiUsageCacheMu.Unlock()
	return out, nil
}
