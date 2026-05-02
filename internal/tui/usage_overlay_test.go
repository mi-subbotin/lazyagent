package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func sessionItem(origin model.Origin, project, modelName, costUSD, lastUpdated string) model.Item {
	return model.Item{
		Origin: origin,
		Kind:   model.KindSession,
		Name:   "session",
		Meta: map[string]string{
			"project":     project,
			"usage_model": modelName,
			"cost_usd":    costUSD,
			"lastUpdated": lastUpdated,
		},
	}
}

func TestComputeUsageStatsAllTime(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	items := []model.Item{
		sessionItem(model.OriginClaude, "lazyagent", "claude-opus-4-7", "5.00", "2026-05-01T10:00:00Z"),
		sessionItem(model.OriginClaude, "lazyagent", "claude-sonnet-4-6", "1.50", "2026-04-15T10:00:00Z"),
		sessionItem(model.OriginCodex, "kb", "gpt-5.5", "2.00", "2026-04-01T10:00:00Z"),
		sessionItem(model.OriginGemini, "kb", "gemini-3-pro-preview", "0.75", "2026-03-01T10:00:00Z"),
		// Item without cost should be skipped.
		{Origin: model.OriginClaude, Kind: model.KindSession, Meta: map[string]string{}},
		// Unpriced session — counted but not included in total.
		{Origin: model.OriginClaude, Kind: model.KindSession, Meta: map[string]string{"cost_unpriced": "1", "lastUpdated": "2026-05-01T10:00:00Z"}},
		// Non-session items skipped.
		{Origin: model.OriginClaude, Kind: model.KindSkill},
	}
	st := computeUsageStats(items, usageWindowAll, now)
	if st.Sessions != 4 {
		t.Errorf("Sessions=%d; want 4", st.Sessions)
	}
	if st.Unpriced != 1 {
		t.Errorf("Unpriced=%d; want 1", st.Unpriced)
	}
	wantTotal := 5.00 + 1.50 + 2.00 + 0.75
	if d := st.Total - wantTotal; d > 1e-9 || d < -1e-9 {
		t.Errorf("Total=%f; want %f", st.Total, wantTotal)
	}
	if st.ByOrigin[model.OriginClaude] != 6.50 {
		t.Errorf("Claude total=%f; want 6.50", st.ByOrigin[model.OriginClaude])
	}
	// Top model = claude-opus-4-7 ($5.00)
	if len(st.TopModels) == 0 || st.TopModels[0].Label != "claude-opus-4-7" {
		t.Errorf("top model = %+v; want claude-opus-4-7", st.TopModels)
	}
	// Top project = lazyagent ($6.50)
	if len(st.TopProjects) == 0 || st.TopProjects[0].Label != "lazyagent" {
		t.Errorf("top project = %+v; want lazyagent", st.TopProjects)
	}
}

func TestComputeUsageStatsTimeWindow(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	items := []model.Item{
		sessionItem(model.OriginClaude, "p", "m", "10.00", "2026-05-10T08:00:00Z"), // today
		sessionItem(model.OriginClaude, "p", "m", "20.00", "2026-05-05T08:00:00Z"), // 5d old
		sessionItem(model.OriginClaude, "p", "m", "30.00", "2026-04-15T08:00:00Z"), // 25d old
		sessionItem(model.OriginClaude, "p", "m", "40.00", "2026-01-01T08:00:00Z"), // ancient
	}
	if st := computeUsageStats(items, usageWindowToday, now); st.Total != 10.00 {
		t.Errorf("today total=%f; want 10", st.Total)
	}
	if st := computeUsageStats(items, usageWindow7d, now); st.Total != 30.00 {
		t.Errorf("7d total=%f; want 30 (10+20)", st.Total)
	}
	if st := computeUsageStats(items, usageWindow30d, now); st.Total != 60.00 {
		t.Errorf("30d total=%f; want 60 (10+20+30)", st.Total)
	}
	if st := computeUsageStats(items, usageWindowAll, now); st.Total != 100.00 {
		t.Errorf("all-time total=%f; want 100", st.Total)
	}
}

func TestUsageOverlayTextEmpty(t *testing.T) {
	st := computeUsageStats(nil, usageWindow7d, time.Now())
	body := usageOverlayText(st)
	if !strings.Contains(body, "No priced sessions") {
		t.Errorf("expected empty-state message, got:\n%s", body)
	}
	if !strings.Contains(body, "7d") {
		t.Errorf("expected window label '7d' in header, got:\n%s", body)
	}
}

func TestUsageOverlayTextRendersTotalsAndTops(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	items := []model.Item{
		sessionItem(model.OriginClaude, "lazyagent", "claude-opus-4-7", "12.34", "2026-05-09T08:00:00Z"),
		sessionItem(model.OriginCodex, "kb", "gpt-5.5", "1.00", "2026-05-08T08:00:00Z"),
	}
	st := computeUsageStats(items, usageWindowAll, now)
	body := usageOverlayText(st)
	if !strings.Contains(body, "$13.34") {
		t.Errorf("expected total $13.34 in body, got:\n%s", body)
	}
	if !strings.Contains(body, "claude-opus-4-7") {
		t.Errorf("expected top model claude-opus-4-7, got:\n%s", body)
	}
	if !strings.Contains(body, "lazyagent") {
		t.Errorf("expected top project lazyagent, got:\n%s", body)
	}
	if !strings.Contains(body, "Claude $12.34") {
		t.Errorf("expected per-origin breakdown, got:\n%s", body)
	}
}

func TestUsageWindowCutoff(t *testing.T) {
	now := time.Date(2026, 5, 10, 15, 30, 0, 0, time.UTC)
	if c := usageWindowToday.cutoff(now); c.Day() != 10 || c.Hour() != 0 {
		t.Errorf("today cutoff=%v; want start of day", c)
	}
	if c := usageWindow7d.cutoff(now); now.Sub(c) < 7*24*time.Hour-time.Hour {
		t.Errorf("7d cutoff=%v; expected ~7d ago", c)
	}
	if c := usageWindowAll.cutoff(now); !c.IsZero() {
		t.Errorf("all-time cutoff should be zero, got %v", c)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hell…"},
		{"abc", 1, "…"},
		{"abc", 0, "…"},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.n); got != c.want {
			t.Errorf("truncate(%q,%d) = %q; want %q", c.in, c.n, got, c.want)
		}
	}
}
