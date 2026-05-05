package tui

import (
	"strings"
	"testing"
)

func TestRenderTokenBadge_NoiseFloor(t *testing.T) {
	if got := renderTokenBadge(0); got != "" {
		t.Errorf("zero tokens should render no badge, got %q", got)
	}
	if got := renderTokenBadge(100); got != "" {
		t.Errorf("sub-noise-floor (100) should render nothing, got %q", got)
	}
}

func TestRenderTokenBadge_Tiers(t *testing.T) {
	cases := []struct {
		n        int
		contains string
	}{
		{2_000, "~2"},
		{15_000, "~15"},
		{40_000, "~40"},
	}
	for _, c := range cases {
		got := renderTokenBadge(c.n)
		if !strings.Contains(got, c.contains) {
			t.Errorf("renderTokenBadge(%d)=%q, want substring %q", c.n, got, c.contains)
		}
	}
}

func TestItemTokenEstimate_SkipsUnsupportedKinds(t *testing.T) {
	m := newTestModel(t, fixtureItems(), "")
	for _, it := range m.items {
		got := m.itemTokenEstimate(it)
		switch it.Kind.String() {
		case "Skills", "Agents", "Memory", "Commands":
			// Either an estimate or -1 (file unreadable in tests). Both ok.
			_ = got
		default:
			if got != -1 {
				t.Errorf("kind %s should be skipped (-1), got %d", it.Kind, got)
			}
		}
	}
}
