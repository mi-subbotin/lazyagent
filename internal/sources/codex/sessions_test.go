package codex

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// requireSqlite3 skips the test when the system sqlite3 binary isn't
// on PATH — the codex adapter shells out to it for SQLite reads, so
// without sqlite3 there's nothing meaningful to assert.
func requireSqlite3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skipf("sqlite3 not in PATH: %v", err)
	}
}

// seedDB creates a state_5.sqlite under the given codex home,
// matching the columns the adapter SELECTs. Returns nothing — fail
// fast on any sqlite3 error.
func seedDB(t *testing.T, codexHome string, rows []codexRow) {
	t.Helper()
	dbPath := filepath.Join(codexHome, "state_5.sqlite")
	schema := `CREATE TABLE threads(
		id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL,
		source TEXT NOT NULL DEFAULT '', model_provider TEXT NOT NULL DEFAULT '',
		cwd TEXT NOT NULL,
		title TEXT NOT NULL DEFAULT '', sandbox_policy TEXT NOT NULL DEFAULT '',
		approval_mode TEXT NOT NULL DEFAULT '', tokens_used INTEGER NOT NULL DEFAULT 0,
		has_user_event INTEGER NOT NULL DEFAULT 0,
		archived INTEGER NOT NULL DEFAULT 0,
		first_user_message TEXT NOT NULL DEFAULT ''
	);`
	cmd := exec.Command("sqlite3", dbPath)
	cmd.Stdin = strings.NewReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create schema: %v (%s)", err, out)
	}
	for _, r := range rows {
		stmt := "INSERT INTO threads(id, cwd, title, first_user_message, updated_at, archived) VALUES('" +
			r.ID + "', '" + r.Cwd + "', '" + r.Title + "', '" + r.FirstUser +
			"', " + itoa(r.UpdatedAt) + ", " + itoa(int64(r.Archived)) + ");"
		c := exec.Command("sqlite3", dbPath)
		c.Stdin = strings.NewReader(stmt)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("seed row %s: %v (%s)", r.ID, err, out)
		}
	}
}

type codexRow struct {
	ID        string
	Cwd       string
	Title     string
	FirstUser string
	UpdatedAt int64
	Archived  int
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestScanCodexSessionsBasic(t *testing.T) {
	requireSqlite3(t)
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	if err := exec.Command("mkdir", "-p", codexHome).Run(); err != nil {
		t.Fatal(err)
	}
	projectDir := "/Users/testfake/Projects/myapp"
	seedDB(t, codexHome, []codexRow{
		{ID: "t1", Cwd: projectDir, Title: "local thread", FirstUser: "Build the parser", UpdatedAt: 1700000200},
		{ID: "t2", Cwd: "/Users/testfake/Projects/other", FirstUser: "Refactor models", UpdatedAt: 1700000100},
		{ID: "t3", Cwd: "/private/tmp/scratch", FirstUser: "throwaway", UpdatedAt: 1700000050},
		{ID: "t4", Cwd: projectDir, FirstUser: "archived one", UpdatedAt: 1700000010, Archived: 1},
	})

	items := scanSessions(codexHome, projectDir)
	if len(items) != 3 {
		t.Fatalf("want 3 active sessions, got %d", len(items))
	}
	got := map[string]string{}
	for _, it := range items {
		bucket := "global"
		if it.Private {
			bucket = "private"
		} else if it.Scope == model.ScopeLocal {
			bucket = "local"
		}
		got[it.ConfigKey] = bucket
	}
	want := map[string]string{"t1": "local", "t2": "global", "t3": "private"}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("session %s = %q, want %q", id, got[id], w)
		}
	}
	// Ordered newest first.
	if items[0].ConfigKey != "t1" {
		t.Errorf("expected t1 first (highest updated_at), got %s", items[0].ConfigKey)
	}
	// Preview comes from first_user_message.
	if !strings.Contains(items[0].Name, "Build the parser") {
		t.Errorf("preview missing in items[0].Name=%q", items[0].Name)
	}
}

func TestScanCodexSessionsMissingDB(t *testing.T) {
	requireSqlite3(t)
	if items := scanSessions(t.TempDir(), ""); len(items) != 0 {
		t.Errorf("expected empty, got %d", len(items))
	}
}
