package gemini

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// TestScanSessionsBasic plants two Gemini chat JSONs in the same
// project hash dir, asserts they're returned newest first, the per-
// project numeric index matches `gemini --resume <idx>` semantics, and
// the preview comes from the first user message.
func TestScanSessionsBasic(t *testing.T) {
	home := t.TempDir()
	hash := "abc123def456"
	chatsDir := filepath.Join(home, ".gemini", "tmp", hash, "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	newer := `{
  "sessionId": "AAAA",
  "projectHash": "` + hash + `",
  "startTime": "2026-01-15T10:00:00.000Z",
  "lastUpdated": "2026-01-15T10:30:00.000Z",
  "messages": [
    {"id":"m1","timestamp":"2026-01-15T10:00:00.000Z","type":"user","content":"Refactor the UserService class"}
  ]
}`
	older := `{
  "sessionId": "BBBB",
  "projectHash": "` + hash + `",
  "startTime": "2026-01-14T09:00:00.000Z",
  "lastUpdated": "2026-01-14T09:30:00.000Z",
  "messages": [
    {"id":"m1","timestamp":"2026-01-14T09:00:00.000Z","type":"user","content":"Add a unit test for parser"}
  ]
}`
	if err := os.WriteFile(filepath.Join(chatsDir, "session-2026-01-15-AAAA.json"), []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chatsDir, "session-2026-01-14-BBBB.json"), []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}

	items := scanSessions(filepath.Join(home, ".gemini"))
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].ConfigKey != "AAAA" {
		t.Errorf("items[0] = %q, expected newer session AAAA first", items[0].ConfigKey)
	}
	if items[0].Meta["index"] != "1" {
		t.Errorf("newest items[0].index=%q, want 1", items[0].Meta["index"])
	}
	if items[1].Meta["index"] != "2" {
		t.Errorf("older items[1].index=%q, want 2", items[1].Meta["index"])
	}
	if !strings.Contains(items[0].Name, "Refactor the UserService") {
		t.Errorf("items[0].Name=%q missing preview", items[0].Name)
	}
	if items[0].Origin != model.OriginGemini || items[0].Kind != model.KindSession {
		t.Errorf("origin/kind mismatch: %v/%v", items[0].Origin, items[0].Kind)
	}
	if items[0].Meta["projectHash"] != hash {
		t.Errorf("projectHash=%q, want %q", items[0].Meta["projectHash"], hash)
	}
}

// TestScanSessionsMalformedSkipped — a non-JSON file in chats/ must
// not crash the scanner; it's silently skipped like the rest of the
// adapter.
func TestScanSessionsMalformedSkipped(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "deadbeef", "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chatsDir, "session-broken.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if items := scanSessions(filepath.Join(home, ".gemini")); len(items) != 0 {
		t.Fatalf("expected empty, got %d", len(items))
	}
}
