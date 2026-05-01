package tui

import (
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestTruncRunes(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hell…"},
		{"abc", 0, ""},
		{"abc", 1, "…"},
		{"日本語テスト", 4, "日…"},
	}
	for _, tc := range cases {
		if got := truncRunes(tc.in, tc.w); got != tc.want {
			t.Errorf("truncRunes(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
		}
	}
}

func TestPadRightW(t *testing.T) {
	if got := padRightW("ab", 5); got != "ab   " {
		t.Errorf("padRightW = %q", got)
	}
	if got := padRightW("longstring", 3); got != "longstring" {
		t.Errorf("padRightW shouldn't shrink: %q", got)
	}
}

func TestJoinExactly(t *testing.T) {
	if got := joinExactly([]string{"a", "b", "c"}, 2); strings.Count(got, "\n") != 1 {
		t.Errorf("expected 2 lines, got %q", got)
	}
	if got := joinExactly([]string{"a"}, 3); got != "a\n\n" {
		t.Errorf("expected padded to 3 lines, got %q", got)
	}
}

func TestWrapLines(t *testing.T) {
	got := wrapLines([]string{"abcdefghij", ""}, 4)
	if len(got) < 3 {
		t.Errorf("expected at least 3 wrapped rows, got %v", got)
	}
}

func TestLastSeg(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a/b/c", "c"},
		{"single", "single"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := lastSeg(tc.in); got != tc.want {
			t.Errorf("lastSeg(%q) = %q", tc.in, got)
		}
	}
}

func TestItemMatches(t *testing.T) {
	it := model.Item{Name: "ReviewerAgent", Description: "Pull request reviewer"}
	cases := []struct {
		filter string
		want   bool
	}{
		{"reviewer", true},
		{"AGENT", false}, // filter must already be lowercase by caller
		{"agent", true},
		{"pull", true},
		{"absent-substring", false},
	}
	for _, tc := range cases {
		if got := itemMatches(it, tc.filter); got != tc.want {
			t.Errorf("itemMatches(%q) = %v, want %v", tc.filter, got, tc.want)
		}
	}
}

func TestHelpTextMentionsKeys(t *testing.T) {
	got := helpText()
	for _, want := range []string{"tab ", "/        filter", "?        toggle this help", "q        quit"} {
		if !strings.Contains(got, want) {
			t.Errorf("helpText missing %q", want)
		}
	}
}

func TestNextFormat(t *testing.T) {
	// Cycles through detailFormat values; without knowing the exact
	// enum size, just assert that two calls eventually return to the
	// original.
	start := detailFormat(0)
	got := start
	seen := map[detailFormat]bool{}
	for i := 0; i < 10; i++ {
		seen[got] = true
		got = nextFormat(got)
		if got == start && len(seen) > 1 {
			return
		}
	}
	t.Errorf("nextFormat did not cycle within 10 steps; visited %d states", len(seen))
}

func TestDefaultExpanded(t *testing.T) {
	m := defaultExpanded()
	for _, k := range []string{"Claude", "Codex", "Gemini", "Claude/Hooks", "Gemini/Skills"} {
		if !m[k] {
			t.Errorf("defaultExpanded missing %q", k)
		}
	}
}
