package actions

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/store"
)

// ErrShareUnsupported is returned when the (Kind, Origin) pair has no
// canonical store layout yet — currently MCP (entry-shaped) and Codex
// agents (TOML profiles) need format conversion that's deferred to a
// later phase.
var ErrShareUnsupported = errors.New("share unsupported for this item shape")

// ErrShareLocalScope is returned when the user tries to share a
// project-local item. The shared store is global by definition; sharing
// a local file would tie the canonical version to one project.
var ErrShareLocalScope = errors.New("only global items can be shared")

// ErrShareConflicts is returned by Share / Reshare when a target path
// exists with content the canonical projection would have to replace.
// Inspect the conflicts via ShareConflicts and re-call with
// overwrite=true once the user confirms.
var ErrShareConflicts = errors.New("share conflicts with existing items")

// ShareConflict describes one target whose existing on-disk content
// would be replaced by a Share/Reshare. Kind is a human label
// ("directory" / "file" / "symlink") so the UI can show the user
// what they're about to destroy without re-statting.
type ShareConflict struct {
	Target model.Origin
	Path   string
	Kind   string
}

// ShareConflicts pre-flights a (possibly already-shared) item against
// the desired projection set: returns one entry per target that
// already exists on disk and is not a projection of the canonical
// store entry. The source path itself is excluded so first-time
// share doesn't flag its own origin as a conflict.
//
// For first-time share (canonical missing) any existing target is a
// conflict. For reshare, targets whose path resolves back to the
// canonical dir are silently skipped — those are existing projections,
// not conflicts.
func ShareConflicts(it model.Item, targets []model.Origin) []ShareConflict {
	canonical := store.CanonicalItemDir(it.Path)

	var sourcePath string
	if canonical != "" {
		sourcePath = canonical
	} else if it.Storage == model.StorageDir {
		sourcePath = filepath.Dir(it.Path)
	} else {
		sourcePath = it.Path
	}
	absSrc, _ := filepath.Abs(sourcePath)

	var conflicts []ShareConflict
	for _, t := range targets {
		target, err := projectionPath(it.Kind, it.Name, t, model.ScopeGlobal, "")
		if err != nil {
			continue
		}
		if absT, _ := filepath.Abs(target); absT == absSrc {
			continue
		}
		info, err := os.Lstat(target)
		if err != nil {
			continue
		}
		// Reshare on an already-projected target: the symlink points
		// at the canonical we own. Not a conflict.
		if canonical != "" && store.CanonicalItemDir(target) == canonical {
			continue
		}
		kind := "file"
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			kind = "symlink"
		case info.IsDir():
			kind = "directory"
		}
		conflicts = append(conflicts, ShareConflict{Target: t, Path: target, Kind: kind})
	}
	return conflicts
}

// Share moves an item's bytes into the canonical lazyagent store and
// projects the canonical copy back to each target tool via symlink (or
// copy on cloud-synced volumes). The original tool-side file is
// replaced by a projection so subsequent reads land on the canonical
// bytes.
//
// Targets must include the item's own Origin if the user wants the
// source tool to keep seeing the item — Share doesn't add it
// implicitly. Returns ErrShareUnsupported for kinds whose canonical
// shape isn't defined yet, ErrShareConflicts when a target already
// has unrelated content and overwrite is false (the caller is
// expected to confirm via ShareConflicts and retry with overwrite=true).
func Share(it model.Item, targets []model.Origin, overwrite bool) error {
	if it.Scope != model.ScopeGlobal {
		return ErrShareLocalScope
	}
	if !canShareKind(it.Kind, it.Storage) {
		return ErrShareUnsupported
	}
	for _, t := range targets {
		if !canProjectTo(it.Kind, t) {
			return fmt.Errorf("%w: kind %s cannot project to %s without conversion", ErrShareUnsupported, it.Kind, t)
		}
	}

	if err := store.Init(); err != nil {
		return fmt.Errorf("init store: %w", err)
	}

	storeDir, err := store.ItemDir(it.Kind, it.Name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(storeDir); err == nil {
		return fmt.Errorf("%w: %s already in shared store", ErrTargetExists, it.Name)
	}

	conflicts := ShareConflicts(it, targets)
	if len(conflicts) > 0 && !overwrite {
		return ErrShareConflicts
	}
	for _, c := range conflicts {
		if err := os.RemoveAll(c.Path); err != nil {
			return fmt.Errorf("clean conflict at %s: %w", c.Path, err)
		}
	}

	bodyName, ok := canonicalBodyName(it.Kind)
	if !ok {
		return ErrShareUnsupported
	}
	storeBodyPath := filepath.Join(storeDir, bodyName)

	if err := os.MkdirAll(filepath.Dir(storeDir), 0o755); err != nil {
		return err
	}

	switch it.Storage {
	case model.StorageDir:
		// Skill-shaped: relocate the whole directory. Body file inside
		// keeps its name (SKILL.md).
		srcDir := filepath.Dir(it.Path)
		if err := moveDir(srcDir, storeDir); err != nil {
			return err
		}
	case model.StorageFile:
		// Single file: store layout puts a per-item directory around
		// it so the manifest can sit alongside.
		if err := os.MkdirAll(storeDir, 0o755); err != nil {
			return err
		}
		if err := moveFile(it.Path, storeBodyPath); err != nil {
			return err
		}
	default:
		return ErrShareUnsupported
	}

	sharedTo := make([]string, 0, len(targets))
	for _, t := range targets {
		sharedTo = append(sharedTo, t.String())
	}
	manifest := store.Manifest{
		Name:     it.Name,
		Kind:     it.Kind.String(),
		SharedTo: sharedTo,
	}
	if err := store.WriteManifest(store.ManifestPath(storeDir), manifest); err != nil {
		return err
	}

	// Decide what to symlink/copy from. For directory-shaped kinds we
	// project the directory itself; for file-shaped kinds we project
	// the body file so the per-tool path looks like a normal `.md`.
	source := storeBodyPath
	if it.Storage == model.StorageDir {
		source = storeDir
	}

	for _, t := range targets {
		targetPath, err := projectionPath(it.Kind, it.Name, t, it.Scope, "")
		if err != nil {
			return fmt.Errorf("projection path for %s: %w", t, err)
		}
		mode := store.PickLinkMode(targetPath)
		if err := store.EnsureLink(source, targetPath, mode); err != nil {
			return fmt.Errorf("project to %s: %w", t, err)
		}
	}
	return nil
}

// CurrentProjections returns the tools whose per-tool path currently
// resolves to the canonical item backing it. The reshare overlay uses
// this to pre-check the active set so the user sees the actual on-disk
// state, not just whatever the manifest happened to record at first
// share.
//
// Membership is symlink-based: a path counts as a current projection
// only if it resolves into the same canonical store dir. Copy-mode
// projections (cloud-sync) won't appear here even though they hold
// the canonical content — the manifest's shared_to list is the
// authoritative record for those, and Resync reads it as a fallback
// when CurrentProjections comes up empty.
func CurrentProjections(it model.Item) []model.Origin {
	canonical := canonicalForItem(it)
	if canonical == "" {
		return nil
	}
	var out []model.Origin
	for _, t := range []model.Origin{model.OriginClaude, model.OriginCodex, model.OriginGemini} {
		p, err := projectionPath(it.Kind, it.Name, t, model.ScopeGlobal, "")
		if err != nil {
			continue
		}
		if _, err := os.Lstat(p); err != nil {
			continue
		}
		if store.CanonicalItemDir(p) == canonical {
			out = append(out, t)
		}
	}
	return out
}

// Reshare updates the projection set of an already-shared item: any
// target in newTargets that isn't currently projected gets a fresh
// EnsureLink, and any currently-projected target missing from
// newTargets gets a RemoveLink. The canonical bytes in the store are
// untouched. The manifest's shared_to list is rewritten to mirror the
// new set so the file on disk stays a faithful record.
//
// Caller is the TUI's share overlay when triggered on a shared item;
// it picks newTargets from the same checklist as the first-share flow.
// Returns ErrShareConflicts when an addition would clobber unrelated
// content; caller retries with overwrite=true after confirmation.
func Reshare(it model.Item, newTargets []model.Origin, overwrite bool) error {
	canonical := store.CanonicalItemDir(it.Path)
	if canonical == "" {
		return fmt.Errorf("%w: %s does not resolve into the shared store", ErrShareUnsupported, it.Path)
	}
	for _, t := range newTargets {
		if !canProjectTo(it.Kind, t) {
			return fmt.Errorf("%w: kind %s cannot project to %s without conversion", ErrShareUnsupported, it.Kind, t)
		}
	}

	bodyName, ok := canonicalBodyName(it.Kind)
	if !ok {
		return ErrShareUnsupported
	}
	source := canonical
	if it.Storage != model.StorageDir {
		source = filepath.Join(canonical, bodyName)
	}

	current := CurrentProjections(it)
	add := diffOrigin(newTargets, current)
	remove := diffOrigin(current, newTargets)

	conflicts := ShareConflicts(it, add)
	if len(conflicts) > 0 && !overwrite {
		return ErrShareConflicts
	}
	for _, c := range conflicts {
		if err := os.RemoveAll(c.Path); err != nil {
			return fmt.Errorf("clean conflict at %s: %w", c.Path, err)
		}
	}

	for _, t := range add {
		target, err := projectionPath(it.Kind, it.Name, t, model.ScopeGlobal, "")
		if err != nil {
			return err
		}
		mode := store.PickLinkMode(target)
		if err := store.EnsureLink(source, target, mode); err != nil {
			return fmt.Errorf("project to %s: %w", t, err)
		}
	}
	for _, t := range remove {
		target, err := projectionPath(it.Kind, it.Name, t, model.ScopeGlobal, "")
		if err != nil {
			return err
		}
		if err := store.RemoveLink(source, target); err != nil {
			return fmt.Errorf("unproject %s: %w", t, err)
		}
	}

	manifestPath := store.ManifestPath(canonical)
	m, err := store.ReadManifest(manifestPath)
	if err != nil {
		// Missing manifest is fine — this could be a rescan from a
		// half-migrated install. We just write a fresh one.
		m = store.Manifest{Name: it.Name, Kind: it.Kind.String()}
	}
	m.SharedTo = make([]string, 0, len(newTargets))
	for _, t := range newTargets {
		m.SharedTo = append(m.SharedTo, t.String())
	}
	return store.WriteManifest(manifestPath, m)
}

// diffOrigin returns elements in a not present in b — small helper so
// Reshare can compute add/remove sets without pulling in a generics
// helper just for two-element loops.
func diffOrigin(a, b []model.Origin) []model.Origin {
	bset := map[model.Origin]bool{}
	for _, x := range b {
		bset[x] = true
	}
	var out []model.Origin
	for _, x := range a {
		if !bset[x] {
			out = append(out, x)
		}
	}
	return out
}

// CanShare reports whether the item is eligible for sharing in v1.
// The TUI uses this to grey out the `s` keybinding on items whose
// shape isn't supported yet (MCP entries, Codex agent profiles, etc.).
func CanShare(it model.Item) bool {
	if it.Scope != model.ScopeGlobal {
		return false
	}
	return canShareKind(it.Kind, it.Storage)
}

// canShareKind reports whether (Kind, Storage) has a canonical store
// layout. MCP entries are deferred — they live inside per-tool config
// files and need merge logic, not a simple file move.
func canShareKind(k model.Kind, s model.Storage) bool {
	if s == model.StorageEntry {
		return false
	}
	switch k {
	case model.KindSkill, model.KindAgent, model.KindPrompt, model.KindMemory:
		return true
	}
	return false
}

// canProjectTo reports whether the canonical shape for kind can be
// projected to target without format conversion. Tools where the
// frontmatter / file format diverges (Codex profile-based agents,
// Gemini TOML prompts) are excluded in v1 — the share picker greys
// them out so the user doesn't get a half-broken projection.
func canProjectTo(k model.Kind, target model.Origin) bool {
	switch k {
	case model.KindSkill, model.KindMemory:
		return true
	case model.KindAgent:
		return target == model.OriginClaude || target == model.OriginGemini
	case model.KindPrompt:
		return target == model.OriginClaude || target == model.OriginCodex
	}
	return false
}

// CanProjectTo is the exported form so the TUI's share picker can
// decide which targets to enable.
func CanProjectTo(k model.Kind, target model.Origin) bool {
	return canProjectTo(k, target)
}

// canonicalBodyName delegates to store.CanonicalBodyName; the layout
// is owned by the store package so adapters, Share, and drift
// detection all agree on a single source of truth.
func canonicalBodyName(k model.Kind) (string, bool) {
	return store.CanonicalBodyName(k)
}

// projectionPath returns the per-tool path where the canonical item
// should be projected. Centralised so cross.go's same-shape lookups
// can stay where they are; this version handles the kinds Share
// supports and reuses skillRoot / toolRoot / memoryTargetPath under
// the hood.
func projectionPath(k model.Kind, name string, target model.Origin, scope model.Scope, projectDir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch k {
	case model.KindSkill:
		root, err := skillRoot(home, projectDir, target, scope)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, name), nil
	case model.KindAgent:
		root, err := toolRoot(home, projectDir, target, scope)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "agents", name+".md"), nil
	case model.KindPrompt:
		switch target {
		case model.OriginClaude:
			root, err := toolRoot(home, projectDir, target, scope)
			if err != nil {
				return "", err
			}
			return filepath.Join(root, "commands", name+".md"), nil
		case model.OriginCodex:
			if scope == model.ScopeLocal && projectDir == "" {
				return "", ErrNoProject
			}
			base := home
			if scope == model.ScopeLocal {
				base = projectDir
			}
			return filepath.Join(base, ".codex", "prompts", name+".md"), nil
		}
		return "", fmt.Errorf("prompt target %s needs format conversion", target)
	case model.KindMemory:
		return memoryTargetPath(target, scope, home, projectDir)
	}
	return "", fmt.Errorf("no projection path for kind %s", k)
}

// moveDir does os.Rename, falling back to copy+delete when the source
// and destination live on different filesystems (cross-device link).
func moveDir(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyDir(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// moveFile does os.Rename with the same cross-device fallback.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}
