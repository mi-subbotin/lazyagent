package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
)

// scanSessions reads the threads table from ~/.codex/state_5.sqlite
// via the system sqlite3 CLI (-json output) so we don't pull in the
// 30 MB modernc.org/sqlite cgo-free SQL driver. macOS ships sqlite3,
// the Codex install also requires it transitively, and our brew
// formula deploys to macOS only — Linux/Windows variants will need to
// either bundle sqlite3 or take the Go driver later.
//
// Read-only access. We don't archive / delete here; that's the d
// hotkey's job in app.go.
func scanSessions(codexHome, projectDir string) []model.Item {
	dbPath := filepath.Join(codexHome, "state_5.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		return nil
	}
	// LIMIT keeps the round trip cheap on hugely populated DBs;
	// realistically threads tops out in the low thousands. archived=0
	// hides the user's already-deleted threads.
	q := `SELECT id, cwd, title, first_user_message, updated_at
		FROM threads WHERE archived=0
		ORDER BY updated_at DESC LIMIT 2000`
	cmd := exec.Command("sqlite3", "-json", dbPath, q)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	if len(out) == 0 {
		// sqlite3 emits empty stdout (not "[]") when the result set is
		// empty. Treat as "no sessions".
		return nil
	}
	var rows []struct {
		ID         string `json:"id"`
		Cwd        string `json:"cwd"`
		Title      string `json:"title"`
		FirstUser  string `json:"first_user_message"`
		UpdatedAt  int64  `json:"updated_at"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil
	}
	items := make([]model.Item, 0, len(rows))
	for _, r := range rows {
		items = append(items, codexSessionItem(r.ID, r.Cwd, r.Title, r.FirstUser, r.UpdatedAt, projectDir))
	}
	return items
}

func codexSessionItem(id, cwd, title, firstUser string, updatedAt int64, projectDir string) model.Item {
	preview := parse.SessionPreview(firstUser, 80)
	if preview == "" {
		preview = parse.SessionPreview(title, 80)
	}
	if preview == "" {
		preview = "(no user prompt)"
	}

	mod := time.Unix(updatedAt, 0)
	private := parse.IsPrivateSessionCwd(cwd)
	scope := model.ScopeGlobal
	if !private && parse.SessionIsLocal(cwd, projectDir) {
		scope = model.ScopeLocal
	}

	project := filepath.Base(cwd)
	if cwd == "" {
		project = "(unknown)"
	}

	return model.Item{
		Origin:      model.OriginCodex,
		Kind:        model.KindSession,
		Scope:       scope,
		Private:     private,
		Name:        preview,
		Path:        fmt.Sprintf("codex://thread/%s", id),
		Description: fmt.Sprintf("%s · %s", project, parse.SessionFriendlyTime(mod)),
		Body:        parse.SessionBody(firstUser, project, id, mod),
		// SQLite-backed; Storage / Path are placeholders (no on-disk
		// file the editor / share machinery could open). The TUI
		// short-circuits Codex sessions for actions it can't perform.
		Storage:   model.StorageEntry,
		ConfigKey: id,
		Meta: map[string]string{
			"sessionId":   id,
			"cwd":         cwd,
			"project":     project,
			"title":       title,
			"lastUpdated": mod.UTC().Format(time.RFC3339),
		},
	}
}
