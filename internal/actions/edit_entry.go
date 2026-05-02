// External-editor support for StorageEntry items (PRI-76).
//
// `e` on a plain file opens $EDITOR on it. For StorageEntry items
// (MCP, Hook, codex profile) the same key used to open the *whole*
// surrounding config — settings.json, .claude.json, config.toml —
// burying the user under hundreds of lines of unrelated config
// (numStartups, tipsHistory, …) just to tweak one MCP's args.
//
// PrepareEntryEdit / CommitEntryEdit fix that. Together they:
//
//  1. Read the entry value at ConfigKey via parse.ReadEntry.
//  2. Pretty-print it as JSON into a temp file with a .json extension
//     so $EDITOR enables JSON syntax highlighting.
//  3. After $EDITOR exits, parse the temp file's bytes back to JSON,
//     hand them to parse.WriteEntry, and clean up the temp file.
//
// Format note: even when the underlying config is TOML (codex), the
// temp buffer is JSON. parse.WriteEntry handles the TOML round-trip
// — comments are lost in that path, same as inline editing today.

package actions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
)

// PrepareEntryEdit writes the entry's current JSON representation to
// a temp file and returns the path. Caller is expected to spawn
// $EDITOR on tempPath, then call CommitEntryEdit when the editor
// exits. cleanup removes the temp file regardless of whether the
// edit succeeded; the caller defers it.
func PrepareEntryEdit(it model.Item) (tempPath string, cleanup func(), err error) {
	if it.Storage != model.StorageEntry {
		return "", func() {}, fmt.Errorf("not a StorageEntry item")
	}
	if it.Path == "" || it.ConfigKey == "" {
		return "", func() {}, fmt.Errorf("entry missing Path or ConfigKey")
	}
	val, _, err := parse.ReadEntry(it.Path, it.ConfigKey)
	if err != nil {
		return "", func() {}, fmt.Errorf("read entry: %w", err)
	}
	pretty, err := json.MarshalIndent(val, "", "  ")
	if err != nil {
		return "", func() {}, fmt.Errorf("marshal entry: %w", err)
	}
	// .json extension so $EDITOR enables syntax highlighting; the
	// `lazyagent-` prefix makes stray temp files easy to identify.
	f, err := os.CreateTemp("", "lazyagent-edit-*.json")
	if err != nil {
		return "", func() {}, err
	}
	tempPath = f.Name()
	cleanup = func() { _ = os.Remove(tempPath) }
	if _, err := f.Write(pretty); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, err
	}
	if _, err := f.Write([]byte("\n")); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return tempPath, cleanup, nil
}

// CommitEntryEdit reads tempPath, parses it as JSON, and writes the
// result back to it via parse.WriteEntry. JSON parse errors surface
// as a typed error so the TUI can show "edit aborted: invalid JSON"
// without overwriting the on-disk entry.
func CommitEntryEdit(it model.Item, tempPath string) error {
	data, err := os.ReadFile(tempPath)
	if err != nil {
		return fmt.Errorf("read temp file: %w", err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("invalid JSON in %s: %w", filepath.Base(tempPath), err)
	}
	return parse.WriteEntry(it.Path, it.ConfigKey, v)
}
