package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
)

// Candidate is a single skill/agent/prompt found inside a fetched
// tarball that the user can install. The TUI overlay renders a
// checklist of these so the user picks what to keep before we copy
// anything into ~/.claude / ~/.codex / etc.
type Candidate struct {
	Kind        model.Kind // Skill / Agent / Prompt
	Name        string     // skill folder name, agent file basename, etc.
	Description string     // from frontmatter; empty when unparsed
	// SourceRel is the path inside the extracted cache directory.
	// For a Skill it points at the SKILL.md (StorageDir uses the
	// parent); for Agent / Prompt it's the .md file itself.
	SourceRel string
	// Storage matches model.Storage so callers can hand the candidate
	// straight to actions.Place without a second classification pass.
	Storage model.Storage
	// ParseError mirrors model.Item.ParseError — non-empty means the
	// frontmatter is malformed but the file exists. The TUI shows it
	// next to the checkbox so the user knows what they'd be copying.
	ParseError string
}

// Inspect walks a fetched cache directory and returns install
// candidates. Detection follows the path conventions agreed in the
// PRI-3 plan:
//
//   - skills/*/SKILL.md          -> KindSkill (StorageDir at parent)
//   - agents/*.md                -> KindAgent
//   - commands/*.md              -> KindPrompt
//
// When spec.Kind narrows to a subtree or single file, the walker
// scopes itself accordingly. The optional `lazyagent.yaml` override
// from the issue is deferred — community repos already follow these
// conventions, so MVP works without it.
func Inspect(cacheDir string, spec *Spec) ([]Candidate, error) {
	if spec.Kind == SpecKindFile {
		return inspectSingleFile(cacheDir, spec)
	}
	root := cacheDir
	if spec.Kind == SpecKindSubtree && spec.Path != "" {
		root = filepath.Join(cacheDir, filepath.FromSlash(spec.Path))
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("subtree %q: %w", spec.Path, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("subtree %q is not a directory", spec.Path)
		}
	}

	var out []Candidate
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip the marker file's parent shouldn't recurse into the
			// staging cache marker dirs (none in practice, but cheap).
			return nil
		}
		// markerFile is internal bookkeeping, not a real candidate.
		if d.Name() == markerFile {
			return nil
		}
		rel, err := filepath.Rel(cacheDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if c, ok := classify(cacheDir, rel); ok {
			out = append(out, c)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// inspectSingleFile handles `/blob/<ref>/path/to/SKILL.md` URLs.
// The user pointed at exactly one file, so we trust the suffix to
// decide kind.
func inspectSingleFile(cacheDir string, spec *Spec) ([]Candidate, error) {
	full := filepath.Join(cacheDir, filepath.FromSlash(spec.Path))
	if _, err := os.Stat(full); err != nil {
		return nil, fmt.Errorf("file %q: %w", spec.Path, err)
	}
	rel := filepath.ToSlash(spec.Path)
	if c, ok := classify(cacheDir, rel); ok {
		return []Candidate{c}, nil
	}
	// Fall back to "agent" — a bare .md file with frontmatter is the
	// shape Codex/Claude both accept.
	if strings.HasSuffix(strings.ToLower(rel), ".md") {
		c := buildCandidate(cacheDir, rel, model.KindAgent, model.StorageFile, baseName(rel))
		return []Candidate{c}, nil
	}
	return nil, errors.New("unrecognized file shape; expected SKILL.md or *.md")
}

// classify maps a path inside the extracted tarball to a candidate.
// Returns ok=false for files we don't know how to install.
func classify(cacheDir, rel string) (Candidate, bool) {
	parts := strings.Split(rel, "/")
	switch {
	case len(parts) >= 3 && parts[len(parts)-3] == "skills" &&
		strings.EqualFold(parts[len(parts)-1], "SKILL.md"):
		name := parts[len(parts)-2]
		return buildCandidate(cacheDir, rel, model.KindSkill, model.StorageDir, name), true

	case len(parts) >= 2 && parts[len(parts)-2] == "agents" &&
		strings.HasSuffix(strings.ToLower(parts[len(parts)-1]), ".md"):
		return buildCandidate(cacheDir, rel, model.KindAgent, model.StorageFile, baseName(rel)), true

	case len(parts) >= 2 && parts[len(parts)-2] == "commands" &&
		strings.HasSuffix(strings.ToLower(parts[len(parts)-1]), ".md"):
		return buildCandidate(cacheDir, rel, model.KindPrompt, model.StorageFile, baseName(rel)), true
	}
	return Candidate{}, false
}

// buildCandidate reads the file's frontmatter so the picker overlay
// can show description + a parse-error banner. Read failures collapse
// into a ParseError so the user sees something meaningful.
func buildCandidate(cacheDir, rel string, kind model.Kind, storage model.Storage, name string) Candidate {
	full := filepath.Join(cacheDir, filepath.FromSlash(rel))
	c := Candidate{
		Kind:      kind,
		Name:      name,
		SourceRel: rel,
		Storage:   storage,
	}
	data, err := os.ReadFile(full)
	if err != nil {
		c.ParseError = fmt.Sprintf("read: %v", err)
		return c
	}
	fm := parse.Parse(string(data))
	if n := strings.TrimSpace(fm.Fields["name"]); n != "" {
		c.Name = n
	}
	c.Description = strings.TrimSpace(fm.Fields["description"])
	if perr, _ := parse.DiagnoseFrontmatter(fm, []string{"name"}); perr != "" {
		c.ParseError = perr
	}
	return c
}

func baseName(rel string) string {
	b := filepath.Base(rel)
	return strings.TrimSuffix(b, filepath.Ext(b))
}
