package parse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildClaudeTranscriptBasic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	body := strings.Join([]string{
		`{"type":"queue-operation","operation":"enqueue"}`,
		`{"type":"user","timestamp":"2026-04-29T19:00:00.000Z","cwd":"/p","message":{"role":"user","content":"How do I add a flag?"}}`,
		`{"type":"assistant","timestamp":"2026-04-29T19:00:30.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Use cobra or wire flag.Parse manually."},{"type":"tool_use","name":"Read","input":{}}]}}`,
		`{"type":"user","timestamp":"2026-04-29T19:01:00.000Z","message":{"role":"user","content":[{"type":"text","text":"Got it, thanks."}]}}`,
		`{"type":"system","content":"context-window-warning"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got := BuildClaudeTranscript(path)
	for _, want := range []string{
		"### User",
		"### Assistant",
		"How do I add a flag?",
		"Use cobra or wire flag.Parse manually.",
		"_[tool_use: Read]_",
		"Got it, thanks.",
		"2026-04-29 19:00",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q\nfull output:\n%s", want, got)
		}
	}
	// queue-operation / system lines must not show up.
	if strings.Contains(got, "queue-operation") || strings.Contains(got, "context-window-warning") {
		t.Errorf("transcript leaked non-message lines:\n%s", got)
	}
}

func TestBuildClaudeTranscriptTruncates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "long.jsonl")
	var b strings.Builder
	for i := 0; i < transcriptMessageCap+50; i++ {
		b.WriteString(`{"type":"user","message":{"role":"user","content":"msg ` + strings.Repeat("x", 5) + `"}}`)
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := BuildClaudeTranscript(path)
	if !strings.Contains(got, "Showing the last") {
		t.Errorf("truncation banner missing:\n%s", got[:200])
	}
	// Should contain capped count but not full count.
	count := strings.Count(got, "### User")
	if count != transcriptMessageCap {
		t.Errorf("expected %d User headers after truncation, got %d", transcriptMessageCap, count)
	}
}

func TestBuildGeminiTranscriptBasic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	body := `{
  "sessionId": "S1",
  "messages": [
    {"id":"m1","timestamp":"2026-04-29T19:00:00.000Z","type":"user","content":"first"},
    {"id":"m2","timestamp":"2026-04-29T19:00:30.000Z","type":"model","content":"reply"},
    {"id":"m3","timestamp":"2026-04-29T19:01:00.000Z","type":"user","content":"thanks"}
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := BuildGeminiTranscript(path)
	for _, want := range []string{"### User", "### Model", "first", "reply", "thanks"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in transcript:\n%s", want, got)
		}
	}
}

func TestBuildClaudeTranscriptMissingFile(t *testing.T) {
	got := BuildClaudeTranscript("/no/such/file")
	if !strings.Contains(got, "transcript unavailable") {
		t.Errorf("expected fallback message, got: %s", got)
	}
}

func TestBuildClaudeTranscriptEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	got := BuildClaudeTranscript(path)
	if !strings.Contains(got, "no user/assistant messages") {
		t.Errorf("expected empty-transcript note, got: %s", got)
	}
}
