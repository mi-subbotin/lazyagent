package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefault_PopulatedFields(t *testing.T) {
	d := Default()
	if d.UI.Theme == "" {
		t.Fatal("Default().UI.Theme empty")
	}
	if len(d.Search.Roots) == 0 {
		t.Fatal("Default().Search.Roots empty")
	}
	if d.Logging.Level == "" {
		t.Fatal("Default().Logging.Level empty")
	}
	if !d.Tools.Claude || !d.Tools.Codex || !d.Tools.Gemini {
		t.Fatal("Default().Tools should enable Claude/Codex/Gemini")
	}
	if d.Tools.Shared {
		t.Fatal("Default().Tools.Shared should start false until user opts in")
	}
}

func TestLoad_MissingFile_ReturnsDefaults(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.toml")
	cfg, report, err := Load(missing)
	if err != nil {
		t.Fatalf("Load missing returned error: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load missing returned nil config")
	}
	if report.Path != "" {
		t.Errorf("report.Path = %q, want empty for missing file", report.Path)
	}
	if cfg.UI.Theme != Default().UI.Theme {
		t.Errorf("UI.Theme = %q, want default", cfg.UI.Theme)
	}
}

func TestLoad_PartialFile_LayersOverDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
[ui]
theme = "catppuccin"

[tools]
codex = false
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Theme != "catppuccin" {
		t.Errorf("UI.Theme = %q, want catppuccin (overridden)", cfg.UI.Theme)
	}
	if cfg.Tools.Codex {
		t.Error("Tools.Codex = true, want false (overridden)")
	}
	if cfg.UI.DefaultMode != Default().UI.DefaultMode {
		t.Errorf("UI.DefaultMode = %q, want default (untouched)", cfg.UI.DefaultMode)
	}
	if !cfg.Tools.Claude {
		t.Error("Tools.Claude = false, want true (default, untouched)")
	}
}

func TestLoad_InvalidEnum_ResetsAndReports(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
[ui]
theme = "not-a-theme"
default_mode = "wat"

[logging]
level = "yelling"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, report, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Theme != Default().UI.Theme {
		t.Errorf("UI.Theme = %q, want default after invalid", cfg.UI.Theme)
	}
	if cfg.UI.DefaultMode != Default().UI.DefaultMode {
		t.Errorf("UI.DefaultMode = %q, want default after invalid", cfg.UI.DefaultMode)
	}
	if cfg.Logging.Level != Default().Logging.Level {
		t.Errorf("Logging.Level = %q, want default after invalid", cfg.Logging.Level)
	}
	if len(report.ValidationErrors) < 3 {
		t.Errorf("ValidationErrors count = %d, want >= 3; got %v", len(report.ValidationErrors), report.ValidationErrors)
	}
}

func TestLoad_UnknownKeys_Reported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
[ui]
theme = "tokyonight"

[ufos]
visit_count = 3
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, report, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.UnknownKeys) == 0 {
		t.Fatalf("UnknownKeys empty, want at least one (ufos.*)")
	}
	found := false
	for _, k := range report.UnknownKeys {
		if strings.HasPrefix(k, "ufos") {
			found = true
		}
	}
	if !found {
		t.Errorf("UnknownKeys = %v, none starts with 'ufos'", report.UnknownKeys)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.toml")
	in := Default()
	in.UI.Theme = "nord"
	in.Tools.Gemini = false
	in.Install.GcAfterDays = 14
	if err := Save(path, in); err != nil {
		t.Fatal(err)
	}
	out, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.UI.Theme != "nord" {
		t.Errorf("round-trip UI.Theme = %q, want nord", out.UI.Theme)
	}
	if out.Tools.Gemini {
		t.Error("round-trip Tools.Gemini = true, want false")
	}
	if out.Install.GcAfterDays != 14 {
		t.Errorf("round-trip Install.GcAfterDays = %d, want 14", out.Install.GcAfterDays)
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir available")
	}
	cases := []struct {
		in, want string
	}{
		{"~", home},
		{"$HOME", home},
		{"~/foo", filepath.Join(home, "foo")},
		{"$HOME/bar/baz", filepath.Join(home, "bar", "baz")},
		{"/abs/path", "/abs/path"},
		{"relative/path", "relative/path"},
		{"", ""},
	}
	for _, c := range cases {
		if got := ExpandPath(c.in); got != c.want {
			t.Errorf("ExpandPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
