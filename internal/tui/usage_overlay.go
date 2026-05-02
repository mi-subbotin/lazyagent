package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// usageOverlay aggregates per-session cost into a read-only summary
// (PRI-63 Phase 3). The overlay is opened with `u`, closes with esc /
// enter / q, and reads exclusively from already-loaded `m.items` —
// it never re-walks the disk. `Tab` toggles the time window (all
// time → 30d → 7d → today → all time).
type usageOverlay struct {
	window usageWindow
}

type usageWindow int

const (
	usageWindowAll usageWindow = iota
	usageWindow30d
	usageWindow7d
	usageWindowToday
)

func (w usageWindow) String() string {
	switch w {
	case usageWindow30d:
		return "30d"
	case usageWindow7d:
		return "7d"
	case usageWindowToday:
		return "today"
	default:
		return "all time"
	}
}

func (w usageWindow) cutoff(now time.Time) time.Time {
	switch w {
	case usageWindow30d:
		return now.AddDate(0, 0, -30)
	case usageWindow7d:
		return now.AddDate(0, 0, -7)
	case usageWindowToday:
		y, m, d := now.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	default:
		return time.Time{}
	}
}

func newUsageOverlay() *usageOverlay {
	return &usageOverlay{window: usageWindowAll}
}

func (m Model) updateUsageOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter", "q", "u":
		m.usaging = nil
		return m, nil
	case "tab":
		m.usaging.window = (m.usaging.window + 1) % 4
		return m, nil
	case "shift+tab":
		m.usaging.window = (m.usaging.window + 3) % 4
		return m, nil
	}
	return m, nil
}

// usageStats is what the renderer consumes — pure data, no styling.
// Computed once per render off `m.items`.
type usageStats struct {
	Window         usageWindow
	Total          float64
	Sessions       int
	Unpriced       int // sessions with usage but no rate match
	ByOrigin       map[model.Origin]float64
	TopModels      []usageEntry
	TopProjects    []usageEntry
}

type usageEntry struct {
	Label string
	Cost  float64
	Count int
}

// computeUsageStats walks items and aggregates the cost dimensions.
// Items without usage Meta are skipped; sessions with cost_unpriced=1
// are counted as "unpriced" so the overlay can show the gap.
func computeUsageStats(items []model.Item, win usageWindow, now time.Time) usageStats {
	cutoff := win.cutoff(now)
	st := usageStats{
		Window:   win,
		ByOrigin: map[model.Origin]float64{},
	}
	byModel := map[string]*usageEntry{}
	byProject := map[string]*usageEntry{}
	for _, it := range items {
		if it.Kind != model.KindSession {
			continue
		}
		// Time filter: lastUpdated is RFC3339 in Meta.
		if !cutoff.IsZero() {
			ts := it.Meta["lastUpdated"]
			if ts == "" {
				continue
			}
			t, err := time.Parse(time.RFC3339, ts)
			if err != nil || t.Before(cutoff) {
				continue
			}
		}
		costStr := it.Meta["cost_usd"]
		if costStr == "" {
			if it.Meta["cost_unpriced"] == "1" {
				st.Unpriced++
			}
			continue
		}
		var cost float64
		if _, err := fmt.Sscanf(costStr, "%f", &cost); err != nil {
			continue
		}
		st.Total += cost
		st.Sessions++
		st.ByOrigin[it.Origin] += cost

		modelName := it.Meta["usage_model"]
		if modelName == "" {
			modelName = "(unknown)"
		}
		entry := byModel[modelName]
		if entry == nil {
			entry = &usageEntry{Label: modelName}
			byModel[modelName] = entry
		}
		entry.Cost += cost
		entry.Count++

		project := it.Meta["project"]
		if project == "" {
			project = "(unknown)"
		}
		pe := byProject[project]
		if pe == nil {
			pe = &usageEntry{Label: project}
			byProject[project] = pe
		}
		pe.Cost += cost
		pe.Count++
	}
	st.TopModels = topEntries(byModel, 5)
	st.TopProjects = topEntries(byProject, 5)
	return st
}

func topEntries(m map[string]*usageEntry, n int) []usageEntry {
	out := make([]usageEntry, 0, len(m))
	for _, e := range m {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cost > out[j].Cost })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// usageOverlayText renders the overlay body. Pure (no styling) so the
// overlay is unit-testable; the View() wrapper boxes it with the
// shared overlay frame.
func usageOverlayText(st usageStats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage · %s\n", st.Window.String())
	b.WriteString(strings.Repeat("─", 56) + "\n")
	if st.Sessions == 0 {
		b.WriteString("No priced sessions in this window.\n")
		if st.Unpriced > 0 {
			fmt.Fprintf(&b, "(%d session(s) with usage but no rate match)\n", st.Unpriced)
		}
		b.WriteString("\nTab: cycle window · esc: close\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Total      $%.2f across %d sessions", st.Total, st.Sessions)
	if st.Unpriced > 0 {
		fmt.Fprintf(&b, " (+%d unpriced)", st.Unpriced)
	}
	b.WriteString("\n")
	if len(st.ByOrigin) > 0 {
		// Stable origin order so the line doesn't jitter on filter cycles.
		origins := []model.Origin{model.OriginClaude, model.OriginCodex, model.OriginGemini}
		parts := []string{}
		for _, o := range origins {
			if c, ok := st.ByOrigin[o]; ok && c > 0 {
				parts = append(parts, fmt.Sprintf("%s $%.2f", originShort(o), c))
			}
		}
		if len(parts) > 0 {
			fmt.Fprintf(&b, "By origin  %s\n", strings.Join(parts, " · "))
		}
	}
	b.WriteString("\nTop models\n")
	if len(st.TopModels) == 0 {
		b.WriteString("  —\n")
	}
	for _, e := range st.TopModels {
		fmt.Fprintf(&b, "  %-26s  $%7.2f  (%d)\n", truncate(e.Label, 26), e.Cost, e.Count)
	}
	b.WriteString("\nTop projects\n")
	if len(st.TopProjects) == 0 {
		b.WriteString("  —\n")
	}
	for _, e := range st.TopProjects {
		fmt.Fprintf(&b, "  %-26s  $%7.2f  (%d)\n", truncate(e.Label, 26), e.Cost, e.Count)
	}
	b.WriteString("\nTab: cycle window · esc: close\n")
	return b.String()
}

func originShort(o model.Origin) string {
	switch o {
	case model.OriginClaude:
		return "Claude"
	case model.OriginCodex:
		return "Codex"
	case model.OriginGemini:
		return "Gemini"
	default:
		return o.String()
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}
