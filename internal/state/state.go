// Package state owns the persistent UI preferences file at
// ~/.lazyagent/state.json. Unlike the canonical store
// (internal/store), state.json holds opaque, version-able JSON the
// TUI uses to remember what the user toggled last session — privacy
// hide for now, more later (last-update-check timestamp, recently
// resumed sessions, etc).
//
// The file is rewritten atomically (tmp + rename) on every save and
// best-effort everywhere else: a missing or corrupt file collapses
// to defaults so the TUI can always start, and write errors surface
// as a toast rather than crashing.
package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// State is the on-disk shape of state.json. Only flat scalar /
// boolean fields for now; additions should keep that property so
// future versions can add fields without touching old files.
type State struct {
	HidePrivateSessions bool `json:"hide_private_sessions,omitempty"`
}

// Path returns the absolute path to state.json. We co-locate it with
// the canonical store under ~/.lazyagent/ so users only have one
// directory to back up.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lazyagent", "state.json"), nil
}

// Load reads state.json. A missing file returns zero-value State and
// no error — the caller treats that as "first run, defaults". A
// corrupt file logs nothing here (the TUI surfaces a toast) and
// likewise returns defaults so the user isn't locked out.
func Load() (State, error) {
	path, err := Path()
	if err != nil {
		return State{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{}, nil
		}
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, err
	}
	return s, nil
}

// Save writes state atomically. Parent dir is created with 0o755 if
// missing. Tmp file lives in the same dir as the final path so the
// rename stays on a single filesystem.
func Save(s State) error {
	path, err := Path()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
