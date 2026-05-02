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

	// ShowAgentSessions defaults to false — Task-tool subagent
	// transcripts are noise for the typical user (often 5–10× the
	// count of real chats) and stay hidden until they explicitly
	// enable the toggle. PRI-70.
	ShowAgentSessions bool `json:"show_agent_sessions,omitempty"`

	// PRI-19: weekly update check. LastUpdateCheckAt is unix seconds of
	// the last successful poll against the GitHub releases API;
	// LatestKnownVersion is the most recent tag we saw (with the leading
	// "v" stripped). UpdateBannerDismissedFor + UpdateBannerDismissedDate
	// remember a per-day suppression so a key tap silences the banner
	// for the current calendar day, but a brand-new version (or a fresh
	// day) brings it back.
	LastUpdateCheckAt            int64  `json:"last_update_check_at,omitempty"`
	LatestKnownVersion           string `json:"latest_known_version,omitempty"`
	UpdateBannerDismissedFor     string `json:"update_banner_dismissed_for,omitempty"`
	UpdateBannerDismissedDate    string `json:"update_banner_dismissed_date,omitempty"`

	// PRI-75: form-mode editor preferences. EditorListMode controls
	// how list/map fields (args, env, headers) render inside the form
	// editor. Empty / "lines" = multi-line textarea, "fields" =
	// dynamic add/remove rows. Toggled with ctrl+m inside the form;
	// the new value persists as the next-open default.
	EditorListMode string `json:"editor_list_mode,omitempty"`
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
