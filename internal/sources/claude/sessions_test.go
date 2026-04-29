package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// TestScanSessionsBasic plants two .jsonl session files under
// ~/.claude/projects/<encoded>/, one with a string content and one with
// a content-blocks array, and verifies both decode into Items with the
// right Name preview, sessionId, project label and recency ordering.
func TestScanSessionsBasic(t *testing.T) {
	home := t.TempDir()
	projects := filepath.Join(home, ".claude", "projects", "-Users-foo-bar")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}

	// Newer session, content as string.
	newPath := filepath.Join(projects, "11111111-1111-1111-1111-111111111111.jsonl")
	newBody := strings.Join([]string{
		`{"type":"summary","sessionId":"11111111-1111-1111-1111-111111111111","summary":"old session","leafUuid":"a"}`,
		`{"type":"user","cwd":"/Users/foo/bar","message":{"role":"user","content":"Fix the failing test in widgets.go"},"sessionId":"11111111-1111-1111-1111-111111111111"}`,
		`{"type":"assistant","message":{"role":"assistant","content":"Sure, looking now."}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(newPath, []byte(newBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// Older session, content as block array.
	oldPath := filepath.Join(projects, "22222222-2222-2222-2222-222222222222.jsonl")
	oldBody := strings.Join([]string{
		`{"type":"user","cwd":"/Users/foo/bar","message":{"role":"user","content":[{"type":"text","text":"Run the migration"}]},"sessionId":"22222222-2222-2222-2222-222222222222"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(oldPath, []byte(oldBody), 0o644); err != nil {
		t.Fatal(err)
	}
	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	if err := os.Chtimes(oldPath, older, older); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, newer, newer); err != nil {
		t.Fatal(err)
	}

	items := scanSessions(filepath.Join(home, ".claude"), "")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}

	// Newest first.
	if items[0].ConfigKey != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("items[0] sessionId=%q, want newer one first", items[0].ConfigKey)
	}
	if !strings.Contains(items[0].Name, "Fix the failing test") {
		t.Errorf("items[0].Name=%q missing preview", items[0].Name)
	}
	if items[0].Meta["project"] != "bar" {
		t.Errorf("items[0].project=%q, want 'bar' from cwd basename", items[0].Meta["project"])
	}
	if items[0].Kind != model.KindSession || items[0].Origin != model.OriginClaude {
		t.Errorf("items[0] kind/origin = %v/%v", items[0].Kind, items[0].Origin)
	}
	if items[0].Storage != model.StorageFile {
		t.Errorf("items[0].Storage = %v, want StorageFile", items[0].Storage)
	}

	// Block-array content extracted.
	if !strings.Contains(items[1].Name, "Run the migration") {
		t.Errorf("items[1].Name=%q missing block-array preview", items[1].Name)
	}
}

// TestScanSessionsSkipsEmpty drops files that are present but contain
// no parseable user message and no cwd — those are stubs / metadata
// scratch and showing them would clutter the list.
func TestScanSessionsSkipsEmpty(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-empty")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	junk := filepath.Join(dir, "33333333-3333-3333-3333-333333333333.jsonl")
	if err := os.WriteFile(junk, []byte(`{"type":"queue-operation","operation":"enqueue"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if items := scanSessions(filepath.Join(home, ".claude"), ""); len(items) != 0 {
		t.Fatalf("expected empty result, got %d items", len(items))
	}
}

// TestScanSessionsMissingProjectsDir is the cold-start case — fresh
// install, ~/.claude/projects doesn't exist yet. Adapter must return
// an empty slice rather than erroring.
func TestScanSessionsMissingProjectsDir(t *testing.T) {
	home := t.TempDir()
	if items := scanSessions(filepath.Join(home, ".claude"), ""); len(items) != 0 {
		t.Fatalf("expected empty result, got %d items", len(items))
	}
}

// TestScanSessionsClassifiesBuckets covers the local/global/private
// split: a session in the current project goes Local, a session in a
// real but different project goes Global, and a session under
// /private/tmp goes Private. The cwd values written into the jsonl
// don't have to be real paths on disk — the classifier is purely
// path-based — so we use synthetic /Users/testfake/* strings to
// dodge the /var/folders prefix that t.TempDir() lands in on macOS.
func TestScanSessionsClassifiesBuckets(t *testing.T) {
	home := t.TempDir()
	fakeProjectDir := "/Users/testfake/Projects/myapp"
	otherFakeProject := "/Users/testfake/Projects/other"

	mk := func(encoded, cwd, sid string) {
		dir := filepath.Join(home, ".claude", "projects", encoded)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"type":"user","cwd":"` + cwd + `","message":{"role":"user","content":"hi"},"sessionId":"` + sid + `"}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, sid+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("enc-local", fakeProjectDir, "11111111-1111-1111-1111-111111111111")
	mk("enc-global", otherFakeProject, "22222222-2222-2222-2222-222222222222")
	mk("enc-private", "/private/tmp/scratch", "33333333-3333-3333-3333-333333333333")

	items := scanSessions(filepath.Join(home, ".claude"), fakeProjectDir)
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
	want := map[string]string{
		"11111111-1111-1111-1111-111111111111": "local",
		"22222222-2222-2222-2222-222222222222": "global",
		"33333333-3333-3333-3333-333333333333": "private",
	}
	for sid, w := range want {
		if got[sid] != w {
			t.Errorf("session %s = %q, want %q", sid, got[sid], w)
		}
	}
}
