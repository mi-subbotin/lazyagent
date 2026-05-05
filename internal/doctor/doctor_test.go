package doctor

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func writeFakeCLI(t *testing.T, dir, name, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("subprocess fake-CLI tests assume a POSIX shell")
	}
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake CLI %s: %v", name, err)
	}
}

func setIsolatedHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// On some platforms UserHomeDir prefers other vars first.
	t.Setenv("USERPROFILE", dir)
	return dir
}

func TestDetect_PrefersOriginWithMostItems(t *testing.T) {
	dir := t.TempDir()
	writeFakeCLI(t, dir, "claude", "echo claude")
	writeFakeCLI(t, dir, "gemini", "echo gemini")
	t.Setenv("PATH", dir)

	items := []model.Item{
		{Origin: model.OriginCodex}, {Origin: model.OriginCodex},
		{Origin: model.OriginCodex}, {Origin: model.OriginCodex},
		{Origin: model.OriginClaude},
	}
	got := Detect(items)
	if len(got) == 0 {
		t.Fatalf("expected at least one detected CLI")
	}
	// codex isn't on PATH; first available should still be a real
	// detection. With claude (5 items origin Claude lower than Codex's 4)
	// vs gemini (0), claude should win.
	if got[0].Name != "claude" {
		t.Fatalf("expected claude first, got %s (full: %+v)", got[0].Name, got)
	}
}

func TestBuildPrompt_RendersItemsYAML(t *testing.T) {
	items := []model.Item{
		{Kind: model.KindSkill, Name: "foo", Origin: model.OriginClaude, Scope: model.ScopeGlobal, Description: "hello"},
		{Kind: model.KindSession, Name: "skip-me"},
		{Kind: model.KindMemory, Name: "skip-me-2"},
	}
	out, err := BuildPrompt(items)
	if err != nil {
		t.Fatalf("BuildPrompt: %v", err)
	}
	for _, sub := range []string{"name: foo", "kind: Skills", "origin: Claude", "scope: Global"} {
		if !strings.Contains(out, sub) {
			t.Errorf("expected substring %q in prompt; got:\n%s", sub, out)
		}
	}
	if strings.Contains(out, "skip-me") {
		t.Errorf("Sessions/Memory should be skipped; got:\n%s", out)
	}
}

func TestRun_ParsesJSON(t *testing.T) {
	home := setIsolatedHome(t)
	_ = home
	dir := t.TempDir()
	// Fake CLI ignores its args and emits a clean JSON payload.
	writeFakeCLI(t, dir, "claude", `printf '%s\n' '{"duplicates":[{"names":["a","b"],"reason":"x"}],"unused":[],"other":[]}'`)
	t.Setenv("PATH", dir+":/bin:/usr/bin")

	cli := CLI{Name: "claude", Origin: model.OriginClaude, Path: filepath.Join(dir, "claude")}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, rec, err := Run(ctx, []model.Item{{Kind: model.KindSkill, Name: "foo"}}, cli)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rec.Duplicates) != 1 || rec.Duplicates[0].Names[0] != "a" {
		t.Fatalf("unexpected recs: %+v", rec)
	}
	if rec.CLI != "claude" {
		t.Errorf("CLI not stamped, got %q", rec.CLI)
	}

	// File should exist in the isolated home.
	saved := filepath.Join(home, ".lazyagent", "doctor-"+id+".json")
	if _, err := os.Stat(saved); err != nil {
		t.Fatalf("expected saved file at %s: %v", saved, err)
	}
}

func TestRun_FallsBackToFirstJSONBlock(t *testing.T) {
	setIsolatedHome(t)
	dir := t.TempDir()
	writeFakeCLI(t, dir, "claude", `printf '%s\n' 'Sure, here is the analysis: {"duplicates":[],"unused":[],"other":[{"title":"t","body":"b"}]} let me know'`)
	t.Setenv("PATH", dir+":/bin:/usr/bin")

	cli := CLI{Name: "claude", Origin: model.OriginClaude, Path: filepath.Join(dir, "claude")}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, rec, err := Run(ctx, []model.Item{}, cli)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rec.Other) != 1 || rec.Other[0].Title != "t" {
		t.Fatalf("expected fallback parse to succeed, got %+v", rec)
	}
}

func TestLatest_NoFile_ReturnsErrNoRecommendations(t *testing.T) {
	setIsolatedHome(t)
	if _, _, err := Latest(); err != ErrNoRecommendations {
		t.Fatalf("expected ErrNoRecommendations, got %v", err)
	}
}

func TestLatest_PicksMostRecent(t *testing.T) {
	home := setIsolatedHome(t)
	dir := filepath.Join(home, ".lazyagent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "doctor-100.json"), []byte(`{"cli":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "doctor-200.json"), []byte(`{"cli":"new"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	id, rec, err := Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if id != "200" || rec.CLI != "new" {
		t.Fatalf("expected newest doctor-200, got id=%s rec=%+v", id, rec)
	}
}
