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

	items := scanSessions(filepath.Join(home, ".gemini"), "")
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

// TestScanSessionsLocalAndPrivateBuckets verifies SHA-256(cwd) match
// against the projectDir → Local, and SHA-256("/private/tmp") → Private.
func TestScanSessionsLocalAndPrivateBuckets(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "Projects", "myapp")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	mk := func(hashCwd, sid string) {
		hash := cwdHash(hashCwd)
		chatsDir := filepath.Join(home, ".gemini", "tmp", hash, "chats")
		if err := os.MkdirAll(chatsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"sessionId":"` + sid + `","projectHash":"` + hash + `","lastUpdated":"2026-01-15T10:00:00Z","messages":[{"id":"m1","timestamp":"2026-01-15T10:00:00Z","type":"user","content":"hi"}]}`
		if err := os.WriteFile(filepath.Join(chatsDir, "session-"+sid+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk(projectDir, "AAAA")
	mk(filepath.Join(home, "Projects", "other"), "BBBB")
	mk("/private/tmp", "CCCC")

	items := scanSessions(filepath.Join(home, ".gemini"), projectDir)
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	got := map[string]string{}
	for _, it := range items {
		bucket := "global"
		if it.Private {
			bucket = "private"
		} else if it.Scope == model.ScopeLocal {
			bucket = "local"
		}
		got[it.ConfigKey] = bucket
	}
	want := map[string]string{"AAAA": "local", "BBBB": "global", "CCCC": "private"}
	for sid, w := range want {
		if got[sid] != w {
			t.Errorf("session %s = %q, want %q", sid, got[sid], w)
		}
	}
}

// TestScanSessionsBasenameLayout exercises the post-0.40 Gemini layout
// where the per-project bucket is named by cwd basename (not sha256)
// and a sibling `.project_root` file records the absolute cwd. The
// scanner must classify the bucket as Local when the marker matches
// projectDir, stamp Meta["cwd"] for the resume planner, and fall back
// to the JSON's own `projectHash` for hash-keyed callers.
func TestScanSessionsBasenameLayout(t *testing.T) {
	home := t.TempDir()
	// projectDir is used only as a string — for `.project_root` content
	// and for the Local-bucket comparison. We deliberately don't mkdir
	// it under t.TempDir() because on Linux that lives under /tmp,
	// which parse.IsPrivateSessionCwd correctly classifies as Private,
	// flipping Scope back to Global and breaking the test on CI.
	projectDir := "/Users/test/Projects/myapp"
	bucket := filepath.Join(home, ".gemini", "tmp", "myapp")
	chatsDir := filepath.Join(bucket, "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucket, ".project_root"), []byte(projectDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realHash := cwdHash(projectDir)
	body := `{"sessionId":"AAAA","projectHash":"` + realHash + `","lastUpdated":"2026-01-15T10:00:00Z","messages":[{"id":"m1","timestamp":"2026-01-15T10:00:00Z","type":"user","content":"hi"}]}`
	if err := os.WriteFile(filepath.Join(chatsDir, "session-AAAA.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	items := scanSessions(filepath.Join(home, ".gemini"), projectDir)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if it.Scope != model.ScopeLocal {
		t.Errorf("scope=%v, want Local (basename layout with matching .project_root)", it.Scope)
	}
	if it.Meta["cwd"] != projectDir {
		t.Errorf("Meta[cwd]=%q, want %q", it.Meta["cwd"], projectDir)
	}
	if it.Meta["projectHash"] != realHash {
		t.Errorf("Meta[projectHash]=%q, want sha256(cwd)=%q (must come from JSON, not dir name)", it.Meta["projectHash"], realHash)
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
	if items := scanSessions(filepath.Join(home, ".gemini"), ""); len(items) != 0 {
		t.Fatalf("expected empty, got %d", len(items))
	}
}
