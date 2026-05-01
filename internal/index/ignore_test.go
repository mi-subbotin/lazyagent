package index

import (
	"path/filepath"
	"reflect"
	"testing"

	ignore "github.com/sabhiram/go-gitignore"
)

func TestIgnoreNilNeverMatches(t *testing.T) {
	var ig *Ignore
	if ig.Match("/anything") {
		t.Error("nil *Ignore must not match anything")
	}
}

func TestIgnoreMissingFileIsNoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ig, err := LoadIgnore()
	if err != nil {
		t.Fatalf("LoadIgnore on missing file: %v", err)
	}
	if ig != nil {
		t.Errorf("expected nil ignore for missing file, got %#v", ig)
	}
	if ig.Match("/anything") {
		t.Error("nil *Ignore must not match")
	}
}

func TestLoadIgnoreFileExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, "ignore"),
		"# comment\n~/work/\n$HOME/Company/\n/abs/path/\n")
	ig, err := LoadIgnoreFile(filepath.Join(home, "ignore"))
	if err != nil {
		t.Fatalf("LoadIgnoreFile: %v", err)
	}
	if ig == nil {
		t.Fatal("expected non-nil Ignore for present file")
	}
	cases := []struct {
		path string
		want bool
	}{
		{filepath.Join(home, "work", "alpha"), true},
		{filepath.Join(home, "Company", "beta"), true},
		{"/abs/path/gamma", true},
		{filepath.Join(home, "personal", "delta"), false},
	}
	for _, tc := range cases {
		if got := ig.Match(tc.path); got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestAppendPatternRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := AppendPattern("~/work/"); err != nil {
		t.Fatalf("AppendPattern: %v", err)
	}
	if _, err := AppendPattern("Company/"); err != nil {
		t.Fatalf("AppendPattern: %v", err)
	}
	got, err := ListPatterns()
	if err != nil {
		t.Fatalf("ListPatterns: %v", err)
	}
	want := []string{"~/work/", "Company/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListPatterns = %v, want %v", got, want)
	}
}

func TestAppendPatternRefusesEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := AppendPattern("   "); err == nil {
		t.Error("expected error for whitespace pattern")
	}
}

func TestDiscoverHonoursIgnore(t *testing.T) {
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "alpha", ".claude"))
	mustMkdirAll(t, filepath.Join(root, "work", "beta", ".claude"))
	mustMkdirAll(t, filepath.Join(root, "company", "gamma", ".claude"))

	ig := &Ignore{matcher: ignore.CompileIgnoreLines(
		filepath.Join(root, "work")+"/",
		filepath.Join(root, "company")+"/",
	)}
	got, err := Discover(Options{Roots: []string{root}, Ignore: ig})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d projects, want 1: %+v", len(got), got)
	}
	if filepath.Base(got[0].Path) != "alpha" {
		t.Errorf("survived project = %q, want alpha", got[0].Path)
	}
}

func TestExpandHomeMissingHomeUntouched(t *testing.T) {
	cases := []struct{ in, home, want string }{
		{"~/foo", "", "~/foo"},
		{"# comment", "/h", "# comment"},
		{"", "/h", ""},
		{"~/foo", "/h", "/h/foo"},
		{"$HOME/foo", "/h", "/h/foo"},
		{"/abs", "/h", "/abs"},
	}
	for _, tc := range cases {
		if got := expandHome(tc.in, tc.home); got != tc.want {
			t.Errorf("expandHome(%q, %q) = %q, want %q", tc.in, tc.home, got, tc.want)
		}
	}
}

