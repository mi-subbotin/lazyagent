// Package pricing turns per-message token usage into a USD cost using
// a static rates table embedded at build time. The table lives in
// rates.json next to this file; PRs update it when vendors publish new
// tiers. Lazyagent never makes a network call to compute cost — the
// VISION.md offline-first promise outranks staying perfectly current.
//
// When a model is missing from the table, Cost returns ok=false and the
// caller renders an `unpriced` badge with a raw token count; never
// guessing keeps the displayed number trustworthy.
package pricing

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed rates.json
var ratesJSON []byte

// Rate is the per-million-token USD price for one model. Cache-create
// and cache-read default to input pricing when omitted (most non-
// Anthropic vendors don't bill cached tokens differently).
type Rate struct {
	Input       float64 `json:"input"`
	Output      float64 `json:"output"`
	CacheCreate float64 `json:"cache_create,omitempty"`
	CacheRead   float64 `json:"cache_read,omitempty"`
}

type ratesFile struct {
	Models map[string]Rate `json:"models"`
}

var (
	loadedOnce sync.Once
	loaded     ratesFile
)

func load() ratesFile {
	loadedOnce.Do(func() {
		_ = json.Unmarshal(ratesJSON, &loaded)
	})
	return loaded
}

// Usage carries the per-session aggregate token counts. All fields are
// totals across every message in the session — adapters sum them as
// they walk the transcript. Model is the canonical model string lifted
// from the latest message; sessions that switch models mid-conversation
// are priced by the most recent model since that's typically the
// cheapest signal of "what is this session worth?".
type Usage struct {
	Model             string
	InputTokens       int64
	OutputTokens      int64
	CacheCreateTokens int64
	CacheReadTokens   int64
	Messages          int
}

// Total returns the sum of input + output + cache tokens. Useful for
// the unpriced fallback where we still want to show a magnitude.
func (u Usage) Total() int64 {
	return u.InputTokens + u.OutputTokens + u.CacheCreateTokens + u.CacheReadTokens
}

// Cost returns the USD cost of the usage by looking up the model in
// the embedded rates table. Returns ok=false when the model is missing
// from the table — the caller should render the token count plus an
// `unpriced` badge instead of a fake $0.00.
//
// Pricing math is straightforward: each token type is billed at its
// per-million rate, then summed. Cache-create and cache-read fall back
// to the input rate when the model entry doesn't override them.
func Cost(u Usage) (float64, bool) {
	if u.Model == "" {
		return 0, false
	}
	rate, ok := lookup(u.Model)
	if !ok {
		return 0, false
	}
	cacheCreate := rate.CacheCreate
	if cacheCreate == 0 {
		cacheCreate = rate.Input
	}
	cacheRead := rate.CacheRead
	if cacheRead == 0 {
		cacheRead = rate.Input
	}
	const million = 1_000_000.0
	usd := 0.0
	usd += float64(u.InputTokens) / million * rate.Input
	usd += float64(u.OutputTokens) / million * rate.Output
	usd += float64(u.CacheCreateTokens) / million * cacheCreate
	usd += float64(u.CacheReadTokens) / million * cacheRead
	return usd, true
}

// SumCost sums Cost(u) for each Usage in the slice. Returns the
// aggregate USD cost and `allPriced=false` if any entry's model is
// missing from the rates table — partial cost is still useful, but
// the caller can render an "(partial)" badge when allPriced is false.
// An empty slice returns (0, true).
func SumCost(us []Usage) (float64, bool) {
	total := 0.0
	allPriced := true
	for _, u := range us {
		c, ok := Cost(u)
		if !ok {
			allPriced = false
			continue
		}
		total += c
	}
	return total, allPriced
}

// SumTokens returns the combined Total() across a slice of Usage —
// helper for the "unpriced" magnitude fallback.
func SumTokens(us []Usage) int64 {
	var n int64
	for _, u := range us {
		n += u.Total()
	}
	return n
}

// lookup matches a vendor-shaped model string against the rates table.
// We do an exact match first, then strip a trailing date suffix
// (claude-3-5-sonnet-20241022 → claude-3-5-sonnet) and try again, then
// strip a trailing version-only suffix one more time. Stops at three
// passes so a malformed name can't loop.
func lookup(name string) (Rate, bool) {
	r := load()
	if r.Models == nil {
		return Rate{}, false
	}
	if hit, ok := r.Models[name]; ok {
		return hit, true
	}
	candidate := name
	for i := 0; i < 3; i++ {
		idx := strings.LastIndex(candidate, "-")
		if idx <= 0 {
			break
		}
		candidate = candidate[:idx]
		if hit, ok := r.Models[candidate]; ok {
			return hit, true
		}
	}
	return Rate{}, false
}

// Models returns the sorted list of model names in the rates table.
// Test helper / debug surface; the TUI doesn't display this directly.
func Models() []string {
	r := load()
	out := make([]string, 0, len(r.Models))
	for name := range r.Models {
		out = append(out, name)
	}
	return out
}
