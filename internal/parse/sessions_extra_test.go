package parse

import (
	"strings"
	"testing"
	"time"
)

func TestSessionPreview(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello world", 100, "hello world"},
		{"  multi\nline\rtext  ", 100, "multi line text"},
		{"abcdefghij", 5, "abcde…"},
		{"ünïcödé long string", 6, "ünïcöd…"},
	}
	for _, tc := range cases {
		if got := SessionPreview(tc.in, tc.n); got != tc.want {
			t.Errorf("SessionPreview(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestSessionFriendlyTime(t *testing.T) {
	now := time.Now()
	cases := []struct {
		t       time.Time
		wantSub string
	}{
		{now, "just now"},
		{now.Add(-5 * time.Minute), "m ago"},
		{now.Add(-3 * time.Hour), "h ago"},
		{now.Add(-2 * 24 * time.Hour), "d ago"},
		{now.Add(-90 * 24 * time.Hour), "-"}, // calendar date contains dashes
	}
	for _, tc := range cases {
		got := SessionFriendlyTime(tc.t)
		if !strings.Contains(got, tc.wantSub) {
			t.Errorf("SessionFriendlyTime(%v) = %q; want substring %q", tc.t, got, tc.wantSub)
		}
	}
}

func TestSessionBody(t *testing.T) {
	out := SessionBody("hi there", "/tmp/proj", "abc-123", time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC))
	for _, want := range []string{"abc-123", "/tmp/proj", "hi there", "Press `R`"} {
		if !strings.Contains(out, want) {
			t.Errorf("SessionBody missing %q in:\n%s", want, out)
		}
	}
	out2 := SessionBody("", "", "", time.Time{})
	if !strings.Contains(out2, "Press `R`") {
		t.Errorf("SessionBody minimal output should still include resume hint:\n%s", out2)
	}
}
