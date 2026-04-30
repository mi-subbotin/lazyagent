package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseURL(t *testing.T) {
	tests := []struct {
		in   string
		want Spec
	}{
		{
			in:   "github.com/foo/bar",
			want: Spec{Kind: SpecKindRepo, Host: "github.com", Owner: "foo", Repo: "bar"},
		},
		{
			in:   "https://github.com/foo/bar",
			want: Spec{Kind: SpecKindRepo, Host: "github.com", Owner: "foo", Repo: "bar"},
		},
		{
			in:   "https://github.com/foo/bar.git",
			want: Spec{Kind: SpecKindRepo, Host: "github.com", Owner: "foo", Repo: "bar"},
		},
		{
			in:   "https://github.com/foo/bar/tree/main",
			want: Spec{Kind: SpecKindRepo, Host: "github.com", Owner: "foo", Repo: "bar", Ref: "main"},
		},
		{
			in:   "https://github.com/foo/bar/tree/main/skills/cool",
			want: Spec{Kind: SpecKindSubtree, Host: "github.com", Owner: "foo", Repo: "bar", Ref: "main", Path: "skills/cool"},
		},
		{
			in:   "https://github.com/foo/bar/blob/v1.2.3/skills/cool/SKILL.md",
			want: Spec{Kind: SpecKindFile, Host: "github.com", Owner: "foo", Repo: "bar", Ref: "v1.2.3", Path: "skills/cool/SKILL.md"},
		},
		{
			in:   "https://gist.github.com/abcdef0123",
			want: Spec{Kind: SpecKindGist, Host: "gist.github.com", Repo: "abcdef0123"},
		},
		{
			in:   "https://gist.github.com/octocat/abcdef0123",
			want: Spec{Kind: SpecKindGist, Host: "gist.github.com", Repo: "abcdef0123"},
		},
	}
	for _, tt := range tests {
		got, err := ParseURL(tt.in)
		if err != nil {
			t.Errorf("ParseURL(%q) unexpected error: %v", tt.in, err)
			continue
		}
		if *got != tt.want {
			t.Errorf("ParseURL(%q):\n got  %+v\nwant %+v", tt.in, *got, tt.want)
		}
	}
}

func TestParseURL_Errors(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"https://gitlab.com/foo/bar",
		"github.com/onlyowner",
		"https://github.com/foo/bar/raw/main",        // unsupported shape
		"https://github.com/foo/bar/tree",            // missing ref
		"https://github.com/foo/bar/blob/main",       // missing path
	}
	for _, in := range cases {
		if spec, err := ParseURL(in); err == nil {
			t.Errorf("ParseURL(%q) = %+v, want error", in, spec)
		}
	}
}

func TestClient_ResolveAndFetch(t *testing.T) {
	const sha = "abc123def456abc123def456abc123def456abcd"
	tarball := buildTestTarball(t, "foo-bar-abc123d", map[string]string{
		"README.md":            "hello",
		"skills/cool/SKILL.md": "---\nname: cool\n---\nbody\n",
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/foo/bar/commits/main", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": sha})
	})
	mux.HandleFunc("/repos/foo/bar/tarball/"+sha, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-gzip")
		_, _ = w.Write(tarball)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cache := t.TempDir()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, CacheDir: cache}

	spec, err := ParseURL("github.com/foo/bar/tree/main")
	if err != nil {
		t.Fatal(err)
	}
	gotSHA, err := c.Resolve(context.Background(), spec)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if gotSHA != sha {
		t.Fatalf("sha = %q, want %q", gotSHA, sha)
	}

	dir, err := c.Fetch(context.Background(), spec, gotSHA)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := filepath.Join(cache, "github.com", "foo", "bar@"+sha)
	if dir != want {
		t.Errorf("Fetch dir = %q, want %q", dir, want)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "README.md")); err != nil || string(b) != "hello" {
		t.Errorf("README.md missing or wrong: %q, err=%v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "cool", "SKILL.md")); err != nil {
		t.Errorf("nested SKILL.md missing: %v", err)
	}

	// Second Fetch with the same sha must hit the cache and not re-extract.
	// Verify by deleting README.md — a re-extract would put it back.
	if err := os.Remove(filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(context.Background(), spec, gotSHA); err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); !os.IsNotExist(err) {
		t.Errorf("cache miss: README.md was re-extracted (err=%v)", err)
	}
}

func TestClient_Resolve_DefaultBranch(t *testing.T) {
	const sha = "1111111111111111111111111111111111111111"
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/foo/bar", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"default_branch": "trunk"})
	})
	mux.HandleFunc("/repos/foo/bar/commits/trunk", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": sha})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, CacheDir: t.TempDir()}
	spec, err := ParseURL("github.com/foo/bar")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Resolve(context.Background(), spec)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != sha {
		t.Errorf("sha = %q, want %q", got, sha)
	}
}

func TestClient_Resolve_Forwards404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, CacheDir: t.TempDir()}
	spec := &Spec{Kind: SpecKindRepo, Host: "github.com", Owner: "ghost", Repo: "missing", Ref: "main"}
	if _, err := c.Resolve(context.Background(), spec); err == nil {
		t.Error("Resolve on 404: want error, got nil")
	}
}

func TestClient_Fetch_GistNotImplemented(t *testing.T) {
	c := &Client{HTTP: http.DefaultClient, BaseURL: "http://invalid", CacheDir: t.TempDir()}
	spec := &Spec{Kind: SpecKindGist, Host: "gist.github.com", Repo: "abc"}
	if _, err := c.Fetch(context.Background(), spec, "abc"); err == nil {
		t.Error("Fetch(gist) want not-implemented error")
	}
}

func TestExtractTarball_RejectsUnsafePath(t *testing.T) {
	body := buildTestTarball(t, "wrap-prefix", map[string]string{"../escape": "boom"})
	err := extractTarball(bytes.NewReader(body), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Errorf("err = %v, want 'unsafe path'", err)
	}
}

func TestExtractTarball_StripsWrapperDir(t *testing.T) {
	body := buildTestTarball(t, "owner-repo-abc1234", map[string]string{
		"a/b/c.txt": "content",
	})
	dst := t.TempDir()
	if err := extractTarball(bytes.NewReader(body), dst); err != nil {
		t.Fatal(err)
	}
	// Expect dst/a/b/c.txt — wrapper "owner-repo-abc1234" must be gone.
	if b, err := os.ReadFile(filepath.Join(dst, "a", "b", "c.txt")); err != nil || string(b) != "content" {
		t.Errorf("file = %q err=%v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "owner-repo-abc1234")); err == nil {
		t.Errorf("wrapper dir leaked into dst")
	}
}

func buildTestTarball(t *testing.T, prefix string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		full := prefix + "/" + name
		if err := tw.WriteHeader(&tar.Header{
			Name:     full,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
