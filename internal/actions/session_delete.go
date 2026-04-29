package actions

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// DeleteSession removes the on-disk record for a session. The behavior
// per origin matches what the upstream CLI considers a "delete":
//
//   - Claude — remove the .jsonl file outright. claude -r will no
//     longer find this session.
//   - Gemini — remove the .json file. The CLI's `--list-sessions`
//     index numbers shift downward; we surface that by reloading
//     after delete.
//   - Codex — soft-delete in state_5.sqlite by setting archived=1.
//     Codex's own UI hides archived threads; the user can un-archive
//     via codex CLI if they change their mind. We never DROP rows so
//     the rollout_path file (the actual transcript) stays intact.
func DeleteSession(it model.Item) error {
	if it.Kind != model.KindSession {
		return ErrUnsupported
	}
	switch it.Origin {
	case model.OriginClaude, model.OriginGemini:
		if it.Path == "" {
			return fmt.Errorf("session has no on-disk path")
		}
		return os.Remove(it.Path)
	case model.OriginCodex:
		return archiveCodexSession(it)
	}
	return ErrUnsupported
}

// archiveCodexSession sets archived=1 for the thread in state_5.sqlite,
// matching what Codex's own delete gesture does. Shells out to sqlite3
// to dodge the modernc.org/sqlite cgo-free Go driver (~30 MB) — we
// already require sqlite3 to read sessions in the first place.
func archiveCodexSession(it model.Item) error {
	id := it.ConfigKey
	if id == "" {
		return fmt.Errorf("codex session: missing thread id")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	db := filepath.Join(home, ".codex", "state_5.sqlite")
	// Parameterise to keep id-injection out of the SQL string. sqlite3
	// CLI doesn't take bind params, so we pass via the .parameter set
	// dot-command which IS escape-safe.
	script := fmt.Sprintf(".parameter set :id '%s'\nUPDATE threads SET archived=1, archived_at=strftime('%%s','now') WHERE id=:id;\n", sqlEscapeSingle(id))
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3 archive: %w (%s)", err, string(out))
	}
	return nil
}

// sqlEscapeSingle escapes the only character that breaks a
// single-quoted SQLite string literal: a literal single quote, doubled.
// We never inject untrusted text — it's the session id we just read
// out of the same DB — but defending against malformed ids costs
// nothing.
func sqlEscapeSingle(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '\'')
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}

