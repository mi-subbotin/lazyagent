// Package budget estimates the passive-context token cost of an Item
// (PRI-66). "Passive" means: tokens an LLM consumes simply because the
// item is installed, regardless of whether the user invokes it. For
// Skills / Agents / Memory / Prompts that's the markdown body. For
// MCP servers it's the *config* size as a proxy — actual tool-schema
// payloads are emitted at session start by each server and we can't
// observe them without a live connection.
//
// The tokenizer is `o200k_base` (GPT-4o vintage) for everything. It's
// not exact for Claude or Gemini, but published Claude/Gemini
// tokenizers are not redistributable in vendor-neutral form, and the
// rough estimate is what users actually need to plan budget. Off by
// ±10–15% is acceptable for a planning surface.
//
// Encoder is loaded lazily on first call and reused; the on-disk
// vocab is ~3 MB and embedding it cold is fine for an interactive
// TUI. Errors at encoder load time degrade to a chars/4 heuristic so
// the overlay still renders something.
package budget

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/pkoukk/tiktoken-go"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

var (
	encOnce sync.Once
	enc     *tiktoken.Tiktoken
	encErr  error
)

func loadEncoder() (*tiktoken.Tiktoken, error) {
	encOnce.Do(func() {
		enc, encErr = tiktoken.GetEncoding("o200k_base")
	})
	return enc, encErr
}

// CountTokens returns the o200k_base token count for s. Falls back to
// chars/4 when the encoder couldn't load — the rounding is consistent
// enough for planning. Empty string returns 0.
func CountTokens(s string) int {
	if s == "" {
		return 0
	}
	e, err := loadEncoder()
	if err != nil {
		return (len(s) + 3) / 4
	}
	return len(e.Encode(s, nil, nil))
}

// estimateCacheEntry memoizes per-path counts keyed by mtime so the
// overlay can reopen cheaply.
type estimateCacheEntry struct {
	mtime  int64
	tokens int
}

var (
	estimateCacheMu sync.Mutex
	estimateCache   = map[string]estimateCacheEntry{}
)

// EstimateItem returns the passive-context token cost of an item.
// Returns 0 + ok=false for items that don't sit in passive context
// (Hooks, Sessions). The caller filters those before display.
func EstimateItem(it model.Item) (int, bool) {
	switch it.Kind {
	case model.KindSession, model.KindHook:
		return 0, false
	}
	switch it.Storage {
	case model.StorageFile:
		return countFile(it.Path), true
	case model.StorageDir:
		// Skills (and similar dir-shaped items): the markdown body is
		// the part the LLM loads on activation. Sub-assets in the
		// directory (scripts, data) are referenced lazily and don't
		// fall into the passive budget.
		body := filepath.Join(filepath.Dir(it.Path), "SKILL.md")
		if _, err := os.Stat(body); err == nil {
			return countFile(body), true
		}
		return countFile(it.Path), true
	case model.StorageEntry:
		// MCP entries / Codex profiles: tokens of the serialized JSON
		// or TOML config. The actual tool schemas a server advertises
		// at runtime are not in our reach; this is a proxy.
		if it.RawJSON != "" {
			return CountTokens(it.RawJSON), true
		}
		if it.RawTOML != "" {
			return CountTokens(it.RawTOML), true
		}
		// Last-ditch: serialize Meta as a hint that the item is a
		// non-trivial entry rather than 0.
		if len(it.Meta) > 0 {
			b, _ := json.Marshal(it.Meta)
			return CountTokens(string(b)), true
		}
		return 0, true
	}
	return 0, true
}

func countFile(path string) int {
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	mtime := info.ModTime().Unix()
	estimateCacheMu.Lock()
	if hit, ok := estimateCache[path]; ok && hit.mtime == mtime {
		estimateCacheMu.Unlock()
		return hit.tokens
	}
	estimateCacheMu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	tokens := CountTokens(string(data))
	estimateCacheMu.Lock()
	estimateCache[path] = estimateCacheEntry{mtime: mtime, tokens: tokens}
	estimateCacheMu.Unlock()
	return tokens
}

// Group aggregates tokens by (Origin, Kind, Scope). Returned in
// stable iteration order so overlay rendering is deterministic.
type Group struct {
	Origin model.Origin
	Kind   model.Kind
	Scope  model.Scope
	Tokens int
	Items  int
}

// Summary is the full budget breakdown plus a grand total.
type Summary struct {
	Groups []Group
	Total  int
	Items  int
	// Lossy is the count of items whose tokens couldn't be estimated
	// (read errors, missing files). Lets the overlay surface a hint
	// when the total feels too low.
	Lossy int
}

// Estimate walks items, runs EstimateItem on each, and rolls them up
// into per-(Origin,Kind,Scope) groups. Skipped (Sessions/Hooks)
// items are silently dropped.
func Estimate(items []model.Item) Summary {
	type key struct {
		o model.Origin
		k model.Kind
		s model.Scope
	}
	agg := map[key]*Group{}
	order := []key{}
	var sum Summary
	for _, it := range items {
		tok, ok := EstimateItem(it)
		if !ok {
			continue
		}
		if tok == 0 {
			sum.Lossy++
		}
		k := key{it.Origin, it.Kind, it.Scope}
		g, exists := agg[k]
		if !exists {
			g = &Group{Origin: it.Origin, Kind: it.Kind, Scope: it.Scope}
			agg[k] = g
			order = append(order, k)
		}
		g.Tokens += tok
		g.Items++
		sum.Total += tok
		sum.Items++
	}
	sum.Groups = make([]Group, 0, len(order))
	for _, k := range order {
		sum.Groups = append(sum.Groups, *agg[k])
	}
	return sum
}

// FormatTokens renders n as 1.2k / 3.4M for compact UI rendering.
// Mirrors parse.FormatTokens but kept local to avoid an import cycle
// (parse imports pricing, the overlay imports budget).
func FormatTokens(n int) string {
	if n >= 1_000_000 {
		return formatFloat(float64(n)/1_000_000) + "M"
	}
	if n >= 1_000 {
		return formatFloat(float64(n)/1_000) + "k"
	}
	return itoa(n)
}

func formatFloat(f float64) string {
	if f >= 100 {
		return itoa(int(f))
	}
	// One decimal place for compact rendering.
	whole := int(f)
	tenths := int((f - float64(whole)) * 10)
	if tenths < 0 {
		tenths = 0
	}
	return itoa(whole) + "." + itoa(tenths)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ResetEstimateCache clears the per-path memo so tests are isolated.
func ResetEstimateCache() {
	estimateCacheMu.Lock()
	estimateCache = map[string]estimateCacheEntry{}
	estimateCacheMu.Unlock()
}

