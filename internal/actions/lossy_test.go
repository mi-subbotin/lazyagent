package actions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
)

// PRI-72: drift detector reports false when the on-disk codex profile
// entry is the same byte-shape we would regenerate from canonical.
func TestLossyProjectionDrift_CodexProfile_NoDrift(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	canonicalDir := filepath.Join(lib, "agents", "reviewer")
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: reviewer\nmodel: claude-opus-4-7\n---\nReview PRs carefully.\n"
	if err := os.WriteFile(filepath.Join(canonicalDir, "agent.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeCodexProfile([]byte(body), "reviewer", model.ScopeGlobal, ""); err != nil {
		t.Fatalf("writeCodexProfile: %v", err)
	}

	it := model.Item{
		Origin:    model.OriginCodex,
		Kind:      model.KindAgent,
		Scope:     model.ScopeGlobal,
		Name:      "reviewer",
		Path:      filepath.Join(home, ".codex", "config.toml"),
		ConfigKey: "profiles/reviewer",
		Storage:   model.StorageEntry,
	}
	if LossyProjectionDrift(it, canonicalDir, "") {
		t.Errorf("freshly-projected entry should not drift")
	}
}

// PRI-72: hand-edited codex profile entry must surface as drift so
// the TUI shows the `~` marker and R picks the user up.
func TestLossyProjectionDrift_CodexProfile_EditedEntry(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	canonicalDir := filepath.Join(lib, "agents", "reviewer")
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: reviewer\n---\nReview PRs carefully.\n"
	if err := os.WriteFile(filepath.Join(canonicalDir, "agent.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeCodexProfile([]byte(body), "reviewer", model.ScopeGlobal, ""); err != nil {
		t.Fatalf("writeCodexProfile: %v", err)
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := parse.WriteEntry(configPath, "profiles/reviewer", map[string]any{
		"instructions": "Be sloppy.",
	}); err != nil {
		t.Fatalf("hand-edit profile: %v", err)
	}

	it := model.Item{
		Origin:    model.OriginCodex,
		Kind:      model.KindAgent,
		Scope:     model.ScopeGlobal,
		Name:      "reviewer",
		Path:      configPath,
		ConfigKey: "profiles/reviewer",
		Storage:   model.StorageEntry,
	}
	if !LossyProjectionDrift(it, canonicalDir, "") {
		t.Errorf("edited profile entry should drift")
	}
}

// PRI-72: an extra field added to the codex profile entry counts as
// drift — Resync canonical-wins removes it on regenerate.
func TestLossyProjectionDrift_CodexProfile_ExtraField(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	canonicalDir := filepath.Join(lib, "agents", "reviewer")
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: reviewer\n---\nReview PRs.\n"
	if err := os.WriteFile(filepath.Join(canonicalDir, "agent.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeCodexProfile([]byte(body), "reviewer", model.ScopeGlobal, ""); err != nil {
		t.Fatalf("writeCodexProfile: %v", err)
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := parse.WriteEntry(configPath, "profiles/reviewer", map[string]any{
		"instructions":    "Review PRs.",
		"approval_policy": "auto",
	}); err != nil {
		t.Fatalf("hand-edit profile: %v", err)
	}

	it := model.Item{
		Origin:    model.OriginCodex,
		Kind:      model.KindAgent,
		Scope:     model.ScopeGlobal,
		Name:      "reviewer",
		Path:      configPath,
		ConfigKey: "profiles/reviewer",
		Storage:   model.StorageEntry,
	}
	if !LossyProjectionDrift(it, canonicalDir, "") {
		t.Errorf("entry with extra field should drift")
	}
}

// PRI-72: gemini TOML byte equality after a fresh write is no-drift.
func TestLossyProjectionDrift_GeminiTOML_NoDrift(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	canonicalDir := filepath.Join(lib, "prompts", "summarise")
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: summarise\ndescription: \"summarise diff\"\n---\nSummarise the staged diff.\nKeep it short.\n"
	if err := os.WriteFile(filepath.Join(canonicalDir, "prompt.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeGeminiCommandFile([]byte(body), "summarise", model.ScopeGlobal, ""); err != nil {
		t.Fatalf("writeGeminiCommandFile: %v", err)
	}

	tomlPath := filepath.Join(home, ".gemini", "commands", "summarise.toml")
	it := model.Item{
		Origin:  model.OriginGemini,
		Kind:    model.KindPrompt,
		Scope:   model.ScopeGlobal,
		Name:    "summarise",
		Path:    tomlPath,
		Storage: model.StorageFile,
	}
	if LossyProjectionDrift(it, canonicalDir, "") {
		t.Errorf("freshly-projected gemini toml should not drift")
	}
}

// PRI-72: editing the gemini TOML body surfaces as drift.
func TestLossyProjectionDrift_GeminiTOML_EditedBody(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	canonicalDir := filepath.Join(lib, "prompts", "summarise")
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: summarise\n---\nSummarise the staged diff.\n"
	if err := os.WriteFile(filepath.Join(canonicalDir, "prompt.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeGeminiCommandFile([]byte(body), "summarise", model.ScopeGlobal, ""); err != nil {
		t.Fatalf("writeGeminiCommandFile: %v", err)
	}

	tomlPath := filepath.Join(home, ".gemini", "commands", "summarise.toml")
	edited, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(edited), "Summarise", "REWRITE BY USER:", 1)
	if err := os.WriteFile(tomlPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}

	it := model.Item{
		Origin:  model.OriginGemini,
		Kind:    model.KindPrompt,
		Scope:   model.ScopeGlobal,
		Name:    "summarise",
		Path:    tomlPath,
		Storage: model.StorageFile,
	}
	if !LossyProjectionDrift(it, canonicalDir, "") {
		t.Errorf("edited gemini toml should drift")
	}
}

// PRI-72: Resync canonical-wins regenerates a drifted lossy projection
// and clears the drift flag on the next read.
func TestResyncCanonicalWins_RegeneratesLossyDrift(t *testing.T) {
	home := t.TempDir()
	lib := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_LIBRARY", lib)

	// Stage canonical agent.md + a manifest so canonicalForItem finds
	// it via name lookup, mirroring the post-Place library shape.
	canonicalDir := filepath.Join(lib, "agents", "reviewer")
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: reviewer\nmodel: claude-opus-4-7\n---\nReview PRs.\n"
	canonicalBody := filepath.Join(canonicalDir, "agent.md")
	if err := os.WriteFile(canonicalBody, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonicalDir, "manifest.toml"),
		[]byte("name = \"reviewer\"\nkind = \"Agents\"\nprojected_to = [\"Codex\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Project to codex, then tamper with the entry.
	if err := writeCodexProfile([]byte(body), "reviewer", model.ScopeGlobal, ""); err != nil {
		t.Fatalf("writeCodexProfile: %v", err)
	}
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := parse.WriteEntry(configPath, "profiles/reviewer", map[string]any{
		"instructions": "tampered",
	}); err != nil {
		t.Fatal(err)
	}

	shared := model.Item{
		Origin:  model.OriginShared,
		Kind:    model.KindAgent,
		Scope:   model.ScopeGlobal,
		Name:    "reviewer",
		Path:    canonicalBody,
		Storage: model.StorageFile,
		Shared:  true,
	}
	if err := Resync(shared, ResyncCanonicalWins); err != nil {
		t.Fatalf("Resync: %v", err)
	}

	check := model.Item{
		Origin:    model.OriginCodex,
		Kind:      model.KindAgent,
		Scope:     model.ScopeGlobal,
		Name:      "reviewer",
		Path:      configPath,
		ConfigKey: "profiles/reviewer",
		Storage:   model.StorageEntry,
	}
	if LossyProjectionDrift(check, canonicalDir, "") {
		t.Errorf("Resync should have cleared lossy drift")
	}
}
