// Package backup writes pre-mutation snapshots of items destroyed by
// the actions package into ~/.lazyagent/backups/<id>/. Each snapshot is
// a self-describing directory with a meta.json manifest plus the
// original bytes (or for StorageEntry items, the parsed entry value
// serialized as JSON). Snapshots are listed newest-first, restorable in
// whole or per-item, and prune-capped via BackupConfig.KeepLast (50 by
// default). PRI-92 is the foundation; PRI-93 layers a TUI overlay on
// top, PRI-94 adds a CLI surface.
package backup

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/config"
	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
)

// ErrNotFound is returned when a snapshot id (or item index inside a
// snapshot) cannot be resolved.
var ErrNotFound = errors.New("snapshot not found")

// Snapshot is one entry on disk under ~/.lazyagent/backups.
type Snapshot struct {
	ID      string         `json:"id"`
	Op      string         `json:"op"`
	Created time.Time      `json:"created"`
	Items   []SnapshotItem `json:"items"`
}

// SnapshotItem records the on-disk state of a single item that was
// captured before a destructive action ran.
type SnapshotItem struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Origin    string `json:"origin"`
	Scope     string `json:"scope"`
	Path      string `json:"path"`
	ConfigKey string `json:"config_key,omitempty"`
	Mode      string `json:"mode"` // "file" | "dir" | "entry"
	BodyPath  string `json:"body_path,omitempty"`
	EntryFile string `json:"entry_file,omitempty"`
	Format    string `json:"format,omitempty"` // "json" | "toml" — entry mode only
}

// Create copies the on-disk state of every item in items into a new
// snapshot directory and returns the snapshot id (the directory's base
// name). op is a short tag — "delete", "place-overwrite",
// "resync-canonical", "resync-tool" — surfaced later by the TUI
// overlay. Two items referencing the same on-disk artifact are stored
// independently so Restore can address them by index.
//
// The function is named Create rather than Snapshot to avoid colliding
// with the Snapshot struct above; callers spell it backup.Create.
func Create(op string, items []model.Item) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	snap := Snapshot{ID: id, Op: op, Created: time.Now(), Items: make([]SnapshotItem, 0, len(items))}
	for i, it := range items {
		entry, err := captureItem(dir, i, it)
		if err != nil {
			// Best-effort cleanup so a partial snapshot isn't kept on
			// disk when capture fails halfway.
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("snapshot item %d (%s/%s): %w", i, it.Kind, it.Name, err)
		}
		snap.Items = append(snap.Items, entry)
	}

	if err := writeMeta(dir, snap); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return id, nil
}

// Restore writes one snapshotted item back to its original location,
// creating parent directories as needed. Returns ErrNotFound when (id,
// itemIdx) doesn't resolve.
func Restore(id string, itemIdx int) error {
	snap, dir, err := loadSnapshot(id)
	if err != nil {
		return err
	}
	if itemIdx < 0 || itemIdx >= len(snap.Items) {
		return fmt.Errorf("%w: item %d in %s", ErrNotFound, itemIdx, id)
	}
	return restoreItem(dir, snap.Items[itemIdx])
}

// RestoreAll restores every item in the snapshot. Stops at the first
// error and returns it; partial restore is preferable to none.
func RestoreAll(id string) error {
	snap, dir, err := loadSnapshot(id)
	if err != nil {
		return err
	}
	for _, it := range snap.Items {
		if err := restoreItem(dir, it); err != nil {
			return err
		}
	}
	return nil
}

// List returns every snapshot under the backup root, newest first.
// Snapshots that fail to parse are skipped silently — a corrupt
// manifest shouldn't hide healthy ones.
func List() ([]Snapshot, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Snapshot, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		snap, _, err := loadSnapshot(e.Name())
		if err != nil {
			continue
		}
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool {
		// Newest first by creation time; fall back to id for the rare
		// same-nanosecond case (the hex suffix breaks ties on disk).
		if out[i].Created.Equal(out[j].Created) {
			return out[i].ID > out[j].ID
		}
		return out[i].Created.After(out[j].Created)
	})
	return out, nil
}

// Delete removes a single snapshot directory. Idempotent against
// missing ids — already-gone is success.
func Delete(id string) error {
	root, err := Root()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, id)
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.RemoveAll(dir)
}

// Prune retains the keepLast most recent snapshots; older ones are
// removed. keepLast <= 0 means "no pruning".
func Prune(keepLast int) error {
	if keepLast <= 0 {
		return nil
	}
	snaps, err := List()
	if err != nil {
		return err
	}
	if len(snaps) <= keepLast {
		return nil
	}
	for _, s := range snaps[keepLast:] {
		if err := Delete(s.ID); err != nil {
			return err
		}
	}
	return nil
}

// Root returns the backup root, derived from $HOME so tests that
// override HOME automatically isolate. Override directly via
// LAZYAGENT_BACKUPS for callers that don't want to touch HOME.
func Root() (string, error) {
	if v := os.Getenv("LAZYAGENT_BACKUPS"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lazyagent", "backups"), nil
}

// LoadKeepLast reads ~/.lazyagent/config.toml and returns the
// configured Backup.KeepLast. Missing or unreadable config falls back
// to the baked-in default. Provided so callers (actions package) don't
// need to import internal/config just to wire the prune step.
func LoadKeepLast() int {
	def := config.Default().Backup.KeepLast
	path, err := config.DefaultPath()
	if err != nil {
		return def
	}
	cfg, _, err := config.Load(path)
	if err != nil || cfg == nil {
		return def
	}
	return cfg.Backup.KeepLast
}

// --- internals -----------------------------------------------------

func newID() (string, error) {
	var raw [3]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(raw[:])), nil
}

func writeMeta(dir string, snap Snapshot) error {
	blob, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), append(blob, '\n'), 0o644)
}

func loadSnapshot(id string) (Snapshot, string, error) {
	root, err := Root()
	if err != nil {
		return Snapshot{}, "", err
	}
	dir := filepath.Join(root, id)
	blob, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Snapshot{}, "", fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return Snapshot{}, "", err
	}
	var snap Snapshot
	if err := json.Unmarshal(blob, &snap); err != nil {
		return Snapshot{}, "", fmt.Errorf("parse %s/meta.json: %w", id, err)
	}
	return snap, dir, nil
}

// captureItem writes the bytes/value backing it into the snapshot dir
// at <idx>/<...> and returns a SnapshotItem describing how to find
// them later. Mode is derived from it.Storage; missing files in file/
// dir mode are tolerated (snapshot is empty for that item) so a stale
// Item from the TUI tree doesn't blow up the whole snapshot batch.
func captureItem(dir string, idx int, it model.Item) (SnapshotItem, error) {
	item := SnapshotItem{
		Kind:      it.Kind.String(),
		Name:      it.Name,
		Origin:    it.Origin.String(),
		Scope:     it.Scope.String(),
		Path:      it.Path,
		ConfigKey: it.ConfigKey,
	}
	subDir := filepath.Join(dir, strconv.Itoa(idx))
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		return SnapshotItem{}, err
	}

	switch it.Storage {
	case model.StorageEntry:
		val, format, err := parse.ReadEntry(it.Path, it.ConfigKey)
		if err != nil {
			return SnapshotItem{}, fmt.Errorf("read entry %s :: %s: %w", it.Path, it.ConfigKey, err)
		}
		blob, err := json.MarshalIndent(val, "", "  ")
		if err != nil {
			return SnapshotItem{}, err
		}
		entryFile := filepath.Join(subDir, "entry.json")
		if err := os.WriteFile(entryFile, append(blob, '\n'), 0o644); err != nil {
			return SnapshotItem{}, err
		}
		item.Mode = "entry"
		item.EntryFile = filepath.Join(strconv.Itoa(idx), "entry.json")
		item.Format = formatTag(format)
		return item, nil

	case model.StorageDir:
		src := filepath.Dir(it.Path)
		// Record the directory path (not the body file inside) so
		// Restore can RemoveAll + copy back without recomputing the
		// parent. Falling back to it.Path keeps the entry well-formed
		// when the source is already absent — Restore will error on
		// the missing body, which is the correct loud failure mode.
		item.Path = src
		base := filepath.Base(src)
		dst := filepath.Join(subDir, base)
		if err := copyTree(src, dst); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return SnapshotItem{}, err
			}
		}
		item.Mode = "dir"
		item.BodyPath = filepath.Join(strconv.Itoa(idx), base)
		return item, nil

	default: // StorageFile
		base := filepath.Base(it.Path)
		dst := filepath.Join(subDir, base)
		if err := copyFile(it.Path, dst); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return SnapshotItem{}, err
			}
		}
		item.Mode = "file"
		item.BodyPath = filepath.Join(strconv.Itoa(idx), base)
		return item, nil
	}
}

func restoreItem(dir string, it SnapshotItem) error {
	switch it.Mode {
	case "file":
		src := filepath.Join(dir, it.BodyPath)
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(it.Path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(it.Path, data, 0o644)

	case "dir":
		src := filepath.Join(dir, it.BodyPath)
		if _, err := os.Stat(src); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(it.Path), 0o755); err != nil {
			return err
		}
		if _, err := os.Lstat(it.Path); err == nil {
			if err := os.RemoveAll(it.Path); err != nil {
				return err
			}
		}
		return copyTree(src, it.Path)

	case "entry":
		src := filepath.Join(dir, it.EntryFile)
		blob, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		var val any
		if err := json.Unmarshal(blob, &val); err != nil {
			return fmt.Errorf("decode %s: %w", src, err)
		}
		return writeEntry(it.Path, it.ConfigKey, val)
	}
	return fmt.Errorf("unknown snapshot item mode %q", it.Mode)
}

// writeEntry sets keyPath = value inside the config file at path,
// creating intermediate maps along the way. Replicates the
// setNestedEntry behaviour used by actions.placeEntry — parse.Set
// silently no-ops when a multi-segment keyPath traverses missing
// intermediate maps, and Restore needs to handle the case where the
// original config file no longer exists at all.
func writeEntry(path, keyPath string, value any) error {
	data, format, err := parse.Read(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		data = map[string]any{}
		format = parse.FormatFromExt(path)
	}
	setNestedEntry(data, keyPath, value)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return parse.Write(path, data, format)
}

// setNestedEntry mirrors actions.setNestedEntry: assign value at
// keyPath, creating intermediate maps. Kept local rather than exported
// from parse to avoid churning that package's API for the backup
// foundation.
func setNestedEntry(m map[string]any, keyPath string, value any) {
	parts := parse.SplitKey(keyPath)
	if len(parts) == 0 {
		return
	}
	cur := m
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = value
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
}

func formatTag(f parse.ConfigFormat) string {
	if f == parse.FormatTOML {
		return "toml"
	}
	return "json"
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dst)
	}
	if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}

