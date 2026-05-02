package actions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestPrepareEntryEditWritesJSONFragment(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".claude.json")
	body := `{"mcpServers":{"linear":{"command":"npx","args":["@linear/mcp"],"env":{},"type":"stdio"}},"unrelated":"keep"}`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindMCP,
		Path: cfg, Storage: model.StorageEntry,
		ConfigKey: "mcpServers/linear",
	}
	tempPath, cleanup, err := PrepareEntryEdit(it)
	if err != nil {
		t.Fatalf("PrepareEntryEdit: %v", err)
	}
	defer cleanup()
	if !strings.HasSuffix(tempPath, ".json") {
		t.Errorf("expected .json suffix for syntax highlighting, got %q", tempPath)
	}
	data, err := os.ReadFile(tempPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "@linear/mcp") {
		t.Errorf("temp file missing entry body: %s", s)
	}
	if strings.Contains(s, "unrelated") {
		t.Errorf("temp file leaked unrelated keys: %s", s)
	}
	// Cleanup must remove the temp file.
	cleanup()
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Errorf("cleanup didn't remove temp: %v", err)
	}
}

func TestCommitEntryEditWritesBack(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".claude.json")
	body := `{"mcpServers":{"linear":{"command":"npx","args":["@linear/mcp"],"env":{},"type":"stdio"}},"unrelated":"keep"}`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindMCP,
		Path: cfg, Storage: model.StorageEntry,
		ConfigKey: "mcpServers/linear",
	}
	tempPath, cleanup, err := PrepareEntryEdit(it)
	if err != nil {
		t.Fatalf("PrepareEntryEdit: %v", err)
	}
	defer cleanup()
	// Simulate the user editing the args.
	edited := `{
  "args": ["@linear/mcp", "--debug"],
  "command": "npx",
  "env": {},
  "type": "stdio"
}`
	if err := os.WriteFile(tempPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CommitEntryEdit(it, tempPath); err != nil {
		t.Fatalf("CommitEntryEdit: %v", err)
	}
	got, _ := os.ReadFile(cfg)
	s := string(got)
	if !strings.Contains(s, "--debug") {
		t.Errorf("config missing edit:\n%s", s)
	}
	if !strings.Contains(s, "unrelated") {
		t.Errorf("unrelated keys clobbered:\n%s", s)
	}
}

func TestCommitEntryEditRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".claude.json")
	body := `{"mcpServers":{"x":{"command":"npx","type":"stdio"}}}`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindMCP,
		Path: cfg, Storage: model.StorageEntry,
		ConfigKey: "mcpServers/x",
	}
	temp := filepath.Join(dir, "temp.json")
	if err := os.WriteFile(temp, []byte("{ not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CommitEntryEdit(it, temp); err == nil {
		t.Error("expected JSON parse error")
	}
	// On-disk config must be untouched.
	got, _ := os.ReadFile(cfg)
	if string(got) != body {
		t.Errorf("config was modified despite parse error:\n%s", string(got))
	}
}

func TestPrepareEntryEditRejectsNonEntry(t *testing.T) {
	it := model.Item{Storage: model.StorageFile, Path: "/tmp/x"}
	if _, _, err := PrepareEntryEdit(it); err == nil {
		t.Error("expected error for non-StorageEntry item")
	}
}

func TestPrepareEntryEditMissingFile(t *testing.T) {
	it := model.Item{
		Storage: model.StorageEntry, Path: "/no/such/file.json", ConfigKey: "foo/bar",
	}
	if _, _, err := PrepareEntryEdit(it); err == nil {
		t.Error("expected read error for missing config file")
	}
}
