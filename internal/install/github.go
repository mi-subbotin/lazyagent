// Package install handles fetching skills/agents/prompts from
// public GitHub repositories. The 3.A slice covers URL parsing,
// ref-to-sha resolution and tarball download/extract into the cache;
// inspection (frontmatter classification) and the manifest live in
// 3.B (internal/install/inspect.go, manifest.go).
package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// SpecKind says what was resolved from a user-supplied URL — used
// later by inspect to decide what to walk: the whole repo, a single
// subtree, or one file.
type SpecKind int

const (
	SpecKindRepo    SpecKind = iota // whole repo at HEAD or pinned ref
	SpecKindSubtree                 // /tree/<ref>/<path> — narrow to subdir
	SpecKindFile                    // /blob/<ref>/<path>/<file> — single file
	SpecKindGist                    // gist.github.com/<id>
)

// Spec is the parsed representation of an install URL.
type Spec struct {
	Kind  SpecKind
	Host  string // "github.com" or "gist.github.com"
	Owner string // empty for gists
	Repo  string // gist id when Kind==SpecKindGist
	Ref   string // empty means default branch
	Path  string // subtree dir (Subtree) or full file path (File)
}

// ParseURL accepts the canonical github.com / gist URL shapes the
// roadmap promises. Schemes (https://) are optional; trailing slashes
// and `.git` suffixes are tolerated.
func ParseURL(s string) (*Spec, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty URL")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	host := strings.ToLower(u.Host)
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		return nil, fmt.Errorf("URL has no path: %s", s)
	}

	switch host {
	case "github.com", "www.github.com":
		if len(parts) < 2 {
			return nil, fmt.Errorf("github URL needs owner/repo: %s", s)
		}
		spec := &Spec{
			Kind:  SpecKindRepo,
			Host:  "github.com",
			Owner: parts[0],
			Repo:  strings.TrimSuffix(parts[1], ".git"),
		}
		if len(parts) == 2 {
			return spec, nil
		}
		switch parts[2] {
		case "tree":
			if len(parts) < 4 {
				return nil, fmt.Errorf("tree URL needs ref: %s", s)
			}
			spec.Ref = parts[3]
			if len(parts) > 4 {
				spec.Path = strings.Join(parts[4:], "/")
				spec.Kind = SpecKindSubtree
			}
			return spec, nil
		case "blob":
			if len(parts) < 5 {
				return nil, fmt.Errorf("blob URL needs ref and path: %s", s)
			}
			spec.Ref = parts[3]
			spec.Path = strings.Join(parts[4:], "/")
			spec.Kind = SpecKindFile
			return spec, nil
		default:
			return nil, fmt.Errorf("unsupported github URL shape: %s", s)
		}
	case "gist.github.com":
		var id string
		switch len(parts) {
		case 1:
			id = parts[0]
		case 2:
			id = parts[1]
		default:
			return nil, fmt.Errorf("unsupported gist URL: %s", s)
		}
		if id == "" {
			return nil, fmt.Errorf("gist URL has no id: %s", s)
		}
		return &Spec{Kind: SpecKindGist, Host: "gist.github.com", Repo: id}, nil
	default:
		return nil, fmt.Errorf("unsupported host %q (only github.com and gist.github.com)", host)
	}
}

// Client downloads and caches GitHub tarballs. Tests inject BaseURL +
// HTTP to point at httptest; production code uses NewClient.
type Client struct {
	HTTP     *http.Client
	BaseURL  string // override for tests; defaults to https://api.github.com
	Token    string // GH_TOKEN/GITHUB_TOKEN — for private repos and rate limit
	CacheDir string // typically ~/.lazyagent/cache
}

// NewClient builds a Client with sane defaults: 30s timeout, env-based
// auth, the public api.github.com base URL.
func NewClient(cacheDir string) *Client {
	tok := os.Getenv("GH_TOKEN")
	if tok == "" {
		tok = os.Getenv("GITHUB_TOKEN")
	}
	return &Client{
		HTTP:     &http.Client{Timeout: 30 * time.Second},
		BaseURL:  "https://api.github.com",
		Token:    tok,
		CacheDir: cacheDir,
	}
}

// Resolve returns the full commit sha for spec.Ref. An empty ref
// means the repo's default branch (looked up via /repos/{owner}/{repo}).
func (c *Client) Resolve(ctx context.Context, spec *Spec) (string, error) {
	if spec.Kind == SpecKindGist {
		if spec.Ref != "" {
			return spec.Ref, nil
		}
		var info struct {
			History []struct {
				Version string `json:"version"`
			} `json:"history"`
		}
		if err := c.getJSON(ctx, fmt.Sprintf("/gists/%s", spec.Repo), &info); err != nil {
			return "", err
		}
		if len(info.History) == 0 {
			return "", fmt.Errorf("gist %s has no revisions", spec.Repo)
		}
		return info.History[0].Version, nil
	}

	ref := spec.Ref
	if ref == "" {
		var info struct {
			DefaultBranch string `json:"default_branch"`
		}
		if err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/%s", spec.Owner, spec.Repo), &info); err != nil {
			return "", err
		}
		ref = info.DefaultBranch
		if ref == "" {
			ref = "main"
		}
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := c.getJSON(ctx, fmt.Sprintf("/repos/%s/%s/commits/%s", spec.Owner, spec.Repo, ref), &commit); err != nil {
		return "", err
	}
	if commit.SHA == "" {
		return "", fmt.Errorf("empty sha for %s/%s@%s", spec.Owner, spec.Repo, ref)
	}
	return commit.SHA, nil
}

// CachePath returns the on-disk location for a pinned download.
// Layout: <CacheDir>/<host>/<owner>/<repo>@<sha> (gists drop owner).
func (c *Client) CachePath(host, owner, repo, sha string) string {
	if owner == "" {
		return filepath.Join(c.CacheDir, host, fmt.Sprintf("%s@%s", repo, sha))
	}
	return filepath.Join(c.CacheDir, host, owner, fmt.Sprintf("%s@%s", repo, sha))
}

// markerFile is written after a successful extract so a half-finished
// staging dir never looks like a complete cache hit.
const markerFile = ".lazyagent-ok"

// Fetch downloads and extracts the tarball for spec at sha into the
// cache. If the cache already contains a complete extract (marker
// present) it returns immediately. The returned path is the directory
// that contains the repo's top-level files; the GitHub tarball wrapper
// dir is unwrapped.
func (c *Client) Fetch(ctx context.Context, spec *Spec, sha string) (string, error) {
	if spec.Kind == SpecKindGist {
		// Gists need a different transport (raw file URLs, not a
		// tarball). 3.B will plug in the gist fetcher; for now fail
		// loud so callers don't silently lose them.
		return "", errors.New("gist install not yet implemented (3.B)")
	}
	dest := c.CachePath(spec.Host, spec.Owner, spec.Repo, sha)
	if _, err := os.Stat(filepath.Join(dest, markerFile)); err == nil {
		return dest, nil
	}

	endpoint := fmt.Sprintf("/repos/%s/%s/tarball/%s", spec.Owner, spec.Repo, sha)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+endpoint, nil)
	if err != nil {
		return "", err
	}
	c.setAuth(req)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("download %s: %s: %s", endpoint, resp.Status, strings.TrimSpace(string(body)))
	}

	staging := dest + ".tmp"
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", err
	}
	if err := extractTarball(resp.Body, staging); err != nil {
		_ = os.RemoveAll(staging)
		return "", err
	}
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(staging, dest); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dest, markerFile), nil, 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

func (c *Client) setAuth(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "lazyagent")
}

func (c *Client) getJSON(ctx context.Context, p string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+p, nil)
	if err != nil {
		return err
	}
	c.setAuth(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s %s: %s: %s", req.Method, p, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

// extractTarball reads a gzipped tarball and writes its contents into
// dst, stripping the single top-level wrapper directory the GitHub
// tarball API prepends (`<owner>-<repo>-<sha7>/...`). Symlinks and
// hard links are skipped; absolute or path-traversal entries are
// rejected.
func extractTarball(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		clean := stripTopDir(hdr.Name)
		if clean == "" {
			continue
		}
		if !safePath(clean) {
			return fmt.Errorf("unsafe path in tarball: %q", hdr.Name)
		}
		target := filepath.Join(dst, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			continue
		}
	}
}

func stripTopDir(name string) string {
	name = strings.TrimLeft(name, "/")
	if i := strings.Index(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return ""
}

func safePath(p string) bool {
	if filepath.IsAbs(p) {
		return false
	}
	clean := path.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return false
	}
	return true
}
