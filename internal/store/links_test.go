package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCloudSyncedPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cases := []struct {
		path string
		want bool
	}{
		{filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "x"), true},
		{filepath.Join(home, "Library", "CloudStorage", "Dropbox", "x"), true},
		{filepath.Join(home, "Dropbox", "stuff"), true},
		{filepath.Join(home, "Dropbox (Personal)", "stuff"), true},
		{filepath.Join(home, "OneDrive", "stuff"), true},
		{filepath.Join(home, "OneDrive - Acme", "stuff"), true},
		{filepath.Join(home, "Google Drive", "stuff"), true},
		{filepath.Join(home, ".lazyagent", "store"), false},
		{filepath.Join(home, "Projects", "Dropbox-app"), false}, // unrelated nested name
		{"/tmp/lazyagent", false},
	}
	for _, tc := range cases {
		got := CloudSyncedPath(tc.path)
		if got != tc.want {
			t.Errorf("CloudSyncedPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestEnsureLinkSymlink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.md")
	if err := os.WriteFile(src, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	tgt := filepath.Join(dir, "out", "tgt.md")

	if err := EnsureLink(src, tgt, LinkSymlink); err != nil {
		t.Fatalf("EnsureLink: %v", err)
	}
	if got, err := os.Readlink(tgt); err != nil || got != src {
		t.Fatalf("symlink mismatch: got=%q err=%v", got, err)
	}
	// Idempotent re-run.
	if err := EnsureLink(src, tgt, LinkSymlink); err != nil {
		t.Fatalf("EnsureLink idempotent: %v", err)
	}
	// Conflict: regular file at target.
	other := filepath.Join(dir, "other.md")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLink(src, other, LinkSymlink); err == nil {
		t.Fatal("expected ErrLinkConflict on regular file target")
	}
}

func TestEnsureLinkCopyAndRemove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.md")
	if err := os.WriteFile(src, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	tgt := filepath.Join(dir, "out", "tgt.md")
	if err := EnsureLink(src, tgt, LinkCopy); err != nil {
		t.Fatalf("EnsureLink copy: %v", err)
	}
	data, err := os.ReadFile(tgt)
	if err != nil || string(data) != "body" {
		t.Fatalf("copy contents wrong: %q err=%v", data, err)
	}
	// Idempotent re-copy.
	if err := EnsureLink(src, tgt, LinkCopy); err != nil {
		t.Fatalf("EnsureLink copy idempotent: %v", err)
	}
	// Remove cleanly.
	if err := RemoveLink(src, tgt); err != nil {
		t.Fatalf("RemoveLink: %v", err)
	}
	if _, err := os.Stat(tgt); !os.IsNotExist(err) {
		t.Fatalf("target should be gone, err=%v", err)
	}
}
