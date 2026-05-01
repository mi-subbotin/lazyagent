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

// PRI-70: subagent (Task-tool spawn) transcripts live at
// `<encoded>/<sessionId>/subagents/agent-*.jsonl`. They must surface
// as KindSession items but with Agent=true so the TUI can hide them
// by default. The parent session at the top level stays Agent=false.
func TestScanSessionsTagsSubagentTranscripts(t *testing.T) {
	home := t.TempDir()
	encoded := filepath.Join(home, ".claude", "projects", "-Users-testfake-Projects-myapp")
	if err := os.MkdirAll(encoded, 0o755); err != nil {
		t.Fatal(err)
	}
	parentID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	parentBody := `{"type":"user","cwd":"/Users/testfake/Projects/myapp","message":{"role":"user","content":"plan the refactor"},"sessionId":"` + parentID + `","isSidechain":false}` + "\n"
	if err := os.WriteFile(filepath.Join(encoded, parentID+".jsonl"), []byte(parentBody), 0o644); err != nil {
		t.Fatal(err)
	}

	subDir := filepath.Join(encoded, parentID, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentBody := `{"type":"user","cwd":"/Users/testfake/Projects/myapp","message":{"role":"user","content":"You are a code reviewer..."},"sessionId":"` + parentID + `","isSidechain":true,"agentId":"abcd123"}` + "\n"
	if err := os.WriteFile(filepath.Join(subDir, "agent-abcd123.jsonl"), []byte(agentBody), 0o644); err != nil {
		t.Fatal(err)
	}

	items := scanSessions(filepath.Join(home, ".claude"), "")
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 (parent + subagent)", len(items))
	}
	var parentAgent, subAgent *model.Item
	for i := range items {
		if strings.Contains(items[i].Path, "/subagents/") {
			subAgent = &items[i]
		} else {
			parentAgent = &items[i]
		}
	}
	if parentAgent == nil || subAgent == nil {
		t.Fatalf("missing parent or subagent: %+v", items)
	}
	if parentAgent.Agent {
		t.Errorf("top-level session should not be tagged Agent")
	}
	if !subAgent.Agent {
		t.Errorf("subagent transcript should be tagged Agent=true; got %+v", subAgent)
	}
}

// Defence-in-depth: a top-level transcript whose first record carries
// `isSidechain: true` (e.g. format drift in a future Claude release)
// must still be flagged Agent — the path check fails but content
// detection picks it up.
func TestScanSessionsTagsAgentByContent(t *testing.T) {
	home := t.TempDir()
	encoded := filepath.Join(home, ".claude", "projects", "-Users-testfake-x")
	if err := os.MkdirAll(encoded, 0o755); err != nil {
		t.Fatal(err)
	}
	sid := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	body := `{"type":"user","cwd":"/Users/testfake/x","message":{"role":"user","content":"agent ping"},"sessionId":"` + sid + `","isSidechain":true}` + "\n"
	if err := os.WriteFile(filepath.Join(encoded, sid+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	items := scanSessions(filepath.Join(home, ".claude"), "")
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if !items[0].Agent {
		t.Errorf("expected Agent=true via isSidechain content fallback; got %+v", items[0])
	}
}
