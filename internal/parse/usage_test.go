package parse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadClaudeUsage_SumsAssistantTurns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	body := `{"type":"user","message":{"content":"hi"}}
{"type":"assistant","message":{"model":"claude-sonnet-4-6","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":2}}}
{"type":"assistant","message":{"model":"claude-sonnet-4-6","usage":{"input_tokens":20,"output_tokens":15,"cache_creation_input_tokens":3}}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	u, err := ReadClaudeUsage(path)
	if err != nil {
		t.Fatalf("ReadClaudeUsage: %v", err)
	}
	if u.Model != "claude-sonnet-4-6" {
		t.Errorf("Model = %q, want claude-sonnet-4-6", u.Model)
	}
	if u.InputTokens != 30 {
		t.Errorf("InputTokens = %d, want 30", u.InputTokens)
	}
	if u.OutputTokens != 20 {
		t.Errorf("OutputTokens = %d, want 20", u.OutputTokens)
	}
	if u.CacheReadTokens != 2 {
		t.Errorf("CacheReadTokens = %d, want 2", u.CacheReadTokens)
	}
	if u.CacheCreateTokens != 3 {
		t.Errorf("CacheCreateTokens = %d, want 3", u.CacheCreateTokens)
	}
	if u.Messages != 2 {
		t.Errorf("Messages = %d, want 2", u.Messages)
	}
}

func TestReadClaudeUsage_SkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	body := `not-json
{"type":"assistant","message":{"model":"claude-haiku-4-5","usage":{"input_tokens":5,"output_tokens":3}}}
also not json {{
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	u, err := ReadClaudeUsage(path)
	if err != nil {
		t.Fatalf("ReadClaudeUsage: %v", err)
	}
	if u.InputTokens != 5 || u.OutputTokens != 3 {
		t.Errorf("malformed-line skipping broke totals: %+v", u)
	}
}

func TestReadClaudeUsage_MissingFile(t *testing.T) {
	if _, err := ReadClaudeUsage("/no/such/file.jsonl"); err == nil {
		t.Error("missing file should return an error")
	}
}

func TestReadClaudeUsage_CacheHit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"assistant","message":{"model":"x","usage":{"input_tokens":1}}}`+"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	first, err := ReadClaudeUsage(path)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	// Mutate the file but keep the same mtime — second call should hit
	// the cache and return the original totals.
	info, _ := os.Stat(path)
	if err := os.WriteFile(path, []byte(`{"type":"assistant","message":{"model":"x","usage":{"input_tokens":99}}}`+"\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	second, err := ReadClaudeUsage(path)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.InputTokens != second.InputTokens {
		t.Errorf("cache miss when mtime unchanged: first=%d second=%d", first.InputTokens, second.InputTokens)
	}
}
