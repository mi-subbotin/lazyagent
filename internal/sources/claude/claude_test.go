package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// TestScanSkillsFollowsSymlinks locks down the regression that hid
// shared-store projections from the per-tool listing: os.ReadDir
// returns DirEntry whose IsDir() reports false for a symlink-to-dir,
// so without dirOrLinkToDir the skill silently disappeared after `s
// share`.
func TestScanSkillsFollowsSymlinks(t *testing.T) {
	home := t.TempDir()
	storeDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LAZYAGENT_STORE", storeDir)

	// Plant a real skill under a path that mimics the canonical store
	// layout so the Shared flag check has something to resolve to.
	canonical := filepath.Join(storeDir, "skills", "shared-skill")
	if err := os.MkdirAll(canonical, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("---\nname: shared-skill\ndescription: from store\n---\nbody\n")
	if err := os.WriteFile(filepath.Join(canonical, "SKILL.md"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	// Symlink ~/.claude/skills/shared-skill -> canonical, the layout
	// `s share` produces.
	claudeRoot := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claudeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(canonical, filepath.Join(claudeRoot, "shared-skill")); err != nil {
		t.Fatal(err)
	}

	// Plant a regular non-shared skill alongside so we know we're not
	// over-correcting and accidentally hiding real dirs.
	plain := filepath.Join(claudeRoot, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "SKILL.md"), []byte("---\nname: plain\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Source{}.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var sharedFound, plainFound bool
	for _, it := range got {
		if it.Kind != model.KindSkill {
			continue
		}
		switch it.Name {
		case "shared-skill":
			sharedFound = true
			if !it.Shared {
				t.Errorf("shared-skill: Shared=false, want true (path=%s)", it.Path)
			}
		case "plain":
			plainFound = true
			if it.Shared {
				t.Errorf("plain: Shared=true, want false (path=%s)", it.Path)
			}
		}
	}
	if !sharedFound {
		t.Fatal("symlink-backed shared-skill missing from List() output")
	}
	if !plainFound {
		t.Fatal("plain skill missing from List() output")
	}
}
