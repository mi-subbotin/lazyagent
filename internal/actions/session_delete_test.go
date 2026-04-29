package actions

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// TestDeleteSessionClaudeRemovesFile — Claude jsonl files are removed
// outright. claude -r will no longer find this session afterwards.
func TestDeleteSessionClaudeRemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abc.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user"}` + "\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin: model.OriginClaude,
		Kind:   model.KindSession,
		Path:   path,
	}
	if err := DeleteSession(it); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be removed, stat returned %v", err)
	}
}

func TestDeleteSessionGeminiRemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin: model.OriginGemini,
		Kind:   model.KindSession,
		Path:   path,
	}
	if err := DeleteSession(it); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected gone, got %v", err)
	}
}

// TestDeleteSessionRejectsNonSession — non-Session items must go
// through the standard Delete path; DeleteSession refuses them so a
// caller bug doesn't accidentally rm an MCP entry.
func TestDeleteSessionRejectsNonSession(t *testing.T) {
	it := model.Item{Origin: model.OriginClaude, Kind: model.KindSkill}
	if err := DeleteSession(it); !errors.Is(err, ErrUnsupported) {
		t.Errorf("expected ErrUnsupported, got %v", err)
	}
}

// Codex archive is exercised end-to-end in the codex sources tests
// (which set up a real sqlite DB). Here we just verify the no-id
// short-circuit so a malformed Item doesn't shell out blindly.
func TestArchiveCodexSessionNoID(t *testing.T) {
	it := model.Item{Origin: model.OriginCodex, Kind: model.KindSession}
	if err := DeleteSession(it); err == nil {
		t.Errorf("expected error on missing thread id")
	}
}
