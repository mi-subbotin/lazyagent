package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadLastSeen_ClaudeJSONL_FindsName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sess := filepath.Join(home, ".claude", "projects", "-tmp-proj", "abc.jsonl")
	body := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"my-skill"}]}}` + "\n"
	writeFile(t, sess, body)
	wantTime := time.Now().Add(-2 * time.Hour).Round(time.Second)
	if err := os.Chtimes(sess, wantTime, wantTime); err != nil {
		t.Fatal(err)
	}

	items := []model.Item{{Kind: model.KindSkill, Name: "my-skill"}}
	if err := LoadLastSeen(items); err != nil {
		t.Fatalf("LoadLastSeen: %v", err)
	}
	if items[0].LastSeen.IsZero() {
		t.Fatal("LastSeen still zero; expected mtime stamp")
	}
	if !items[0].LastSeen.Equal(wantTime) {
		t.Errorf("LastSeen = %v; want %v", items[0].LastSeen, wantTime)
	}
}

func TestLoadLastSeen_GeminiChats_FindsName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chat := filepath.Join(home, ".gemini", "tmp", "bucket1", "chats", "session-1.json")
	body := `{"messages":[{"type":"user","content":"my-agent","invoked":"my-agent"}]}`
	writeFile(t, chat, body)
	wantTime := time.Now().Add(-3 * time.Hour).Round(time.Second)
	if err := os.Chtimes(chat, wantTime, wantTime); err != nil {
		t.Fatal(err)
	}

	items := []model.Item{{Kind: model.KindAgent, Name: "my-agent"}}
	if err := LoadLastSeen(items); err != nil {
		t.Fatalf("LoadLastSeen: %v", err)
	}
	if !items[0].LastSeen.Equal(wantTime) {
		t.Errorf("LastSeen = %v; want %v", items[0].LastSeen, wantTime)
	}
}

func TestLoadLastSeen_CacheHit_SkipsScan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sess := filepath.Join(home, ".claude", "projects", "-tmp", "x.jsonl")
	writeFile(t, sess, `{"name":"unrelated"}`)
	info, err := os.Stat(sess)
	if err != nil {
		t.Fatal(err)
	}
	mt := info.ModTime()

	cachedTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	cache := cacheFile{
		SessionFiles: []fileEntry{{
			Path:            sess,
			ModTime:         mt,
			LastSeenPerName: map[string]time.Time{"my-skill": cachedTime},
		}},
		UpdatedAt: time.Now(),
	}
	cachePath := filepath.Join(home, ".lazyagent", "usage.json")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(cache)
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	items := []model.Item{{Kind: model.KindSkill, Name: "my-skill"}}
	if err := LoadLastSeen(items); err != nil {
		t.Fatalf("LoadLastSeen: %v", err)
	}
	if !items[0].LastSeen.Equal(cachedTime) {
		t.Errorf("LastSeen = %v; want cached %v (cache should have been used without a fresh scan)", items[0].LastSeen, cachedTime)
	}
}

func TestLoadLastSeen_NameNotFound_LeavesZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sess := filepath.Join(home, ".claude", "projects", "-tmp", "x.jsonl")
	writeFile(t, sess, `{"name":"something-else"}`)

	items := []model.Item{{Kind: model.KindSkill, Name: "ghost"}}
	if err := LoadLastSeen(items); err != nil {
		t.Fatalf("LoadLastSeen: %v", err)
	}
	if !items[0].LastSeen.IsZero() {
		t.Errorf("LastSeen = %v; want zero (name not present in any session log)", items[0].LastSeen)
	}
}

func TestLoadLastSeen_SkipsSessionsAndMemoryKinds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sess := filepath.Join(home, ".claude", "projects", "-tmp", "x.jsonl")
	writeFile(t, sess, `{"name":"never-mind","title":"my-session"}`)

	items := []model.Item{
		{Kind: model.KindSession, Name: "my-session"},
		{Kind: model.KindMemory, Name: "never-mind"},
	}
	if err := LoadLastSeen(items); err != nil {
		t.Fatalf("LoadLastSeen: %v", err)
	}
	for i, it := range items {
		if !it.LastSeen.IsZero() {
			t.Errorf("items[%d] LastSeen = %v; want zero (kind %v should be skipped)", i, it.LastSeen, it.Kind)
		}
	}
}

func TestLoadLastSeen_CodexSkippedGracefully(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Stage a fake codex SQLite file and a rollout — neither should be
	// scanned in this PR. The call must still succeed and not panic.
	if err := os.MkdirAll(filepath.Join(home, ".codex", "sessions", "2026", "05", "01"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "state_5.sqlite"), []byte("not a real db"), 0o644); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(home, ".codex", "sessions", "2026", "05", "01", "rollout-x.jsonl")
	writeFile(t, rollout, `{"type":"event_msg","payload":{"name":"my-skill"}}`)

	items := []model.Item{{Origin: model.OriginCodex, Kind: model.KindSkill, Name: "my-skill"}}
	if err := LoadLastSeen(items); err != nil {
		t.Fatalf("LoadLastSeen: %v", err)
	}
	// Codex is skipped pending parser — LastSeen stays zero.
	if !items[0].LastSeen.IsZero() {
		t.Errorf("LastSeen = %v; want zero (codex scan not yet supported)", items[0].LastSeen)
	}
}
