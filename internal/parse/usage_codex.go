// Codex transcript usage extraction (PRI-63 Phase 1).
//
// Codex stores each session's conversation as a JSONL rollout file at
// ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<UUID>.jsonl. The UUID
// suffix matches the `id` column in state_5.sqlite, which is what the
// codex Source already uses to identify a thread. We don't index the
// SQLite db for usage — we re-read the matching rollout instead, since
// the SQLite schema doesn't carry token counts.
//
// Usage shape: every few turns Codex emits an `event_msg` with
// `payload.type=token_count`. Most have `info: null` (they only report
// rate-limit pressure). The ones with non-null `info` carry a
// `total_token_usage` object that is *cumulative* across the session.
// We take the LAST non-null `info` as the session total — no summing.
//
// Model is read from the most recent `turn_context` event
// (`payload.model`). Sessions can switch models mid-conversation; we
// price against the most recent turn since that's the model the user
// effectively ended on.
//
// Token mapping into pricing.Usage:
//   - input_tokens *includes* cached_input_tokens (verified: total =
//     input + output exactly), so we split into uncached input and
//     cached input to bill correctly when the model entry has a
//     cache_read rate.
//   - reasoning_output_tokens is a *subset* of output_tokens, not
//     additional — don't add it again.

package parse

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mi-subbotin/lazyagent/internal/pricing"
)

var (
	codexUsageCacheMu sync.Mutex
	codexUsageCache   = map[string]usageCacheEntry{}
)

// ReadCodexUsage walks a Codex rollout .jsonl and returns the most
// recent cumulative usage and model. Cached by (path, mtime).
func ReadCodexUsage(path string) (pricing.Usage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return pricing.Usage{}, err
	}
	mtime := info.ModTime().Unix()

	codexUsageCacheMu.Lock()
	if hit, ok := codexUsageCache[path]; ok && hit.mtime == mtime {
		codexUsageCacheMu.Unlock()
		return hit.usage, nil
	}
	codexUsageCacheMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return pricing.Usage{}, err
	}
	defer f.Close()

	var (
		lastInfo *codexTokenInfo
		model    string
		messages int
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var head struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(line, &head); err != nil {
			continue
		}
		switch head.Type {
		case "turn_context":
			var tc struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(head.Payload, &tc); err == nil && tc.Model != "" {
				model = tc.Model
			}
		case "event_msg":
			var em struct {
				Type string `json:"type"`
				Info *struct {
					TotalTokenUsage codexTokenInfo `json:"total_token_usage"`
				} `json:"info"`
			}
			if err := json.Unmarshal(head.Payload, &em); err != nil {
				continue
			}
			if em.Type != "token_count" || em.Info == nil {
				continue
			}
			info := em.Info.TotalTokenUsage
			lastInfo = &info
		case "response_item":
			messages++
		}
	}
	if err := sc.Err(); err != nil {
		return pricing.Usage{}, err
	}

	u := pricing.Usage{Model: model, Messages: messages}
	if lastInfo != nil {
		// input_tokens is total input including cached; pricing.Usage
		// expects InputTokens to be the uncached portion so cache_read
		// can be billed at its discounted rate.
		cached := lastInfo.CachedInputTokens
		if cached > lastInfo.InputTokens {
			cached = lastInfo.InputTokens
		}
		u.InputTokens = lastInfo.InputTokens - cached
		u.CacheReadTokens = cached
		u.OutputTokens = lastInfo.OutputTokens
	}

	codexUsageCacheMu.Lock()
	codexUsageCache[path] = usageCacheEntry{mtime: mtime, usage: u}
	codexUsageCacheMu.Unlock()
	return u, nil
}

type codexTokenInfo struct {
	InputTokens            int64 `json:"input_tokens"`
	CachedInputTokens      int64 `json:"cached_input_tokens"`
	OutputTokens           int64 `json:"output_tokens"`
	ReasoningOutputTokens  int64 `json:"reasoning_output_tokens"`
	TotalTokens            int64 `json:"total_tokens"`
}

// FindCodexRollout walks ~/.codex/sessions and returns the rollout
// path whose filename ends with -<sessionID>.jsonl. Empty string if
// not found. Result is not cached at this layer; callers building a
// large index should batch via BuildCodexRolloutIndex.
func FindCodexRollout(codexHome, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	idx := buildCodexRolloutIndex(codexHome)
	return idx[sessionID]
}

var (
	codexRolloutIdxMu   sync.Mutex
	codexRolloutIdx     map[string]string
	codexRolloutIdxRoot string
)

func buildCodexRolloutIndex(codexHome string) map[string]string {
	codexRolloutIdxMu.Lock()
	defer codexRolloutIdxMu.Unlock()
	if codexRolloutIdx != nil && codexRolloutIdxRoot == codexHome {
		return codexRolloutIdx
	}
	out := map[string]string{}
	root := filepath.Join(codexHome, "sessions")
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		// rollout-<ts>-<UUID>.jsonl — UUID is 36 chars (8-4-4-4-12).
		base := strings.TrimSuffix(name, ".jsonl")
		if len(base) < 36 {
			return nil
		}
		uuid := base[len(base)-36:]
		out[uuid] = path
		return nil
	})
	codexRolloutIdx = out
	codexRolloutIdxRoot = codexHome
	return out
}

// ResetCodexRolloutIndex is a test helper that clears the memoized
// index so a different codexHome can be probed in the same process.
func ResetCodexRolloutIndex() {
	codexRolloutIdxMu.Lock()
	codexRolloutIdx = nil
	codexRolloutIdxRoot = ""
	codexRolloutIdxMu.Unlock()
}
