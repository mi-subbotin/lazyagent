package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/BurntSushi/toml"
)

// Manifest is the on-disk record of everything installed via
// `lazyagent install`. It lives at <ConfigDir>/installed.toml
// (typically ~/.lazyagent/installed.toml). The issue spec mentions a
// YAML file; we use TOML because BurntSushi/toml is already in the
// dependency tree (config + manifest share the same parser) and
// pulling gopkg.in/yaml.v3 just for this one file isn't worth it.
//
// Update and Uninstall consult Manifest to learn what to delete and
// which sha to diff against. cache gc walks Manifest.Installs to
// decide which extracted tarballs to keep.
type Manifest struct {
	Installs []Install `toml:"installs"`
}

// Install describes one installed item. TargetPath is the absolute
// destination — the SKILL.md inside ~/.claude/skills/<name>/, the
// .md file at ~/.codex/agents/<name>.md, etc. We store the path the
// same way model.Item.Path does it so Find/Remove can match against
// what the source adapters report at runtime.
type Install struct {
	Name         string    `toml:"name"`
	Kind         string    `toml:"kind"`           // "skill" / "agent" / "prompt"
	OriginURL    string    `toml:"origin_url"`     // canonical install URL (with ref/path)
	SHA          string    `toml:"sha"`            // pinned commit sha at install time
	InstalledAt  time.Time `toml:"installed_at"`   // RFC 3339, set by Add
	TargetOrigin string    `toml:"target_origin"`  // "claude" / "codex" / "gemini" / "shared"
	TargetScope  string    `toml:"target_scope"`   // "global" / "local"
	TargetPath   string    `toml:"target_path"`    // absolute, on the user's disk
	SourceRel    string    `toml:"source_rel"`     // relpath inside the cache tarball
}

// DefaultPath returns ~/.lazyagent/installed.toml — the location the
// CLI dispatcher uses unless a flag overrides it.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lazyagent", "installed.toml"), nil
}

// Load parses the manifest at path. A missing file is *not* an error
// — first install creates it. Decode errors are forwarded so the
// CLI can show a precise complaint.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Manifest{}, nil
		}
		return nil, err
	}
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}

// Save writes the manifest atomically (temp + rename) so a crash
// can never leave a half-written installed.toml.
func (m *Manifest) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Sort by target path for stable diffs when the file is checked
	// in by paranoid users.
	sort.Slice(m.Installs, func(i, j int) bool {
		return m.Installs[i].TargetPath < m.Installs[j].TargetPath
	})
	tmp, err := os.CreateTemp(filepath.Dir(path), ".installed.toml.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	enc := toml.NewEncoder(tmp)
	if err := enc.Encode(m); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// Add appends or replaces an entry by TargetPath. InstalledAt is set
// here so callers don't have to remember.
func (m *Manifest) Add(in Install) {
	if in.InstalledAt.IsZero() {
		in.InstalledAt = time.Now().UTC()
	}
	for i, existing := range m.Installs {
		if existing.TargetPath == in.TargetPath {
			m.Installs[i] = in
			return
		}
	}
	m.Installs = append(m.Installs, in)
}

// Remove drops an entry by TargetPath. Returns true if anything was
// removed (so callers can complain about an unknown name).
func (m *Manifest) Remove(targetPath string) bool {
	for i, existing := range m.Installs {
		if existing.TargetPath == targetPath {
			m.Installs = append(m.Installs[:i], m.Installs[i+1:]...)
			return true
		}
	}
	return false
}

// FindByName returns all installs matching the given name. Multiple
// installs of the same logical name can coexist when a user installs
// the same skill into both Claude and Codex, so callers must be
// ready for >1 result.
func (m *Manifest) FindByName(name string) []Install {
	var out []Install
	for _, in := range m.Installs {
		if in.Name == name {
			out = append(out, in)
		}
	}
	return out
}

// Sha returns every distinct sha referenced by the manifest. Used by
// `lazyagent cache gc` to decide which extracted tarballs are still
// load-bearing.
func (m *Manifest) Shas() map[string]struct{} {
	out := map[string]struct{}{}
	for _, in := range m.Installs {
		if in.SHA != "" {
			out[in.SHA] = struct{}{}
		}
	}
	return out
}
