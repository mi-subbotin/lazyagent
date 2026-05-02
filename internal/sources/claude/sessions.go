package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
	"github.com/mi-subbotin/lazyagent/internal/pricing"
)

// scanSessions enumerates Claude session transcripts under
// ~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl. The first .jsonl
// line carries metadata (sessionId, type, content) and subsequent lines
// hold the user/assistant turns; we sniff the leading turns just enough
// to extract the project cwd and the first user prompt — enough for a
// scannable list row without slurping multi-megabyte transcripts.
//
// Top-level files are user-driven sessions. Newer Claude releases also
// drop subagent transcripts at
// `<encoded>/<sessionId>/subagents/agent-*.jsonl` — those carry
// `isSidechain: true` on every message and represent Task-tool spawns
// rather than resumable chats. We descend one level into `subagents/`
// directories and tag the resulting items with Agent=true so the TUI
// can hide them by default (PRI-70).
func scanSessions(claudeHome, projectDir string) []model.Item {
	projectsDir := filepath.Join(claudeHome, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}
	var out []model.Item
	for _, projDir := range entries {
		if !projDir.IsDir() {
			continue
		}
		encoded := projDir.Name()
		encodedRoot := filepath.Join(projectsDir, encoded)
		out = scanSessionsIn(encodedRoot, encoded, projectDir, out)
	}
	// Newest first so the list reads top-down by recency.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Meta["lastUpdated"] > out[j].Meta["lastUpdated"]
	})
	return out
}

// scanSessionsIn walks an encoded-cwd directory, picking up top-level
// .jsonl files (user sessions) and descending into any `subagents/`
// subdirectories of per-session folders (Task-tool spawns). All other
// directory contents are ignored.
func scanSessionsIn(root, encoded, projectDir string, out []model.Item) []model.Item {
	files, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, f := range files {
		if f.IsDir() {
			// Per-session folder — only the `subagents` subtree is of
			// interest. Anything else (Claude has used several layouts
			// over time) is silently skipped.
			subagentsDir := filepath.Join(root, f.Name(), "subagents")
			info, err := os.Stat(subagentsDir)
			if err != nil || !info.IsDir() {
				continue
			}
			subFiles, err := os.ReadDir(subagentsDir)
			if err != nil {
				continue
			}
			for _, sf := range subFiles {
				if sf.IsDir() || !strings.HasSuffix(sf.Name(), ".jsonl") {
					continue
				}
				full := filepath.Join(subagentsDir, sf.Name())
				if it, ok := readClaudeSession(full, encoded, projectDir); ok {
					out = append(out, it)
				}
			}
			continue
		}
		if !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		full := filepath.Join(root, f.Name())
		if it, ok := readClaudeSession(full, encoded, projectDir); ok {
			out = append(out, it)
		}
	}
	return out
}

func readClaudeSession(path, encodedDir, projectDir string) (model.Item, bool) {
	f, err := os.Open(path)
	if err != nil {
		return model.Item{}, false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return model.Item{}, false
	}
	sessionID := strings.TrimSuffix(filepath.Base(path), ".jsonl")

	// Path-based agent detection: subagent jsonls live at
	// <encoded>/<sessionId>/subagents/agent-*.jsonl. Cheap and runs
	// before any line parsing.
	agent := isAgentPath(path)

	var (
		firstUserMsg string
		cwd          string
		seenLines    int
	)
	scanner := bufio.NewScanner(f)
	// Tool outputs occasionally exceed the default 64K limit; raise it
	// rather than silently dropping records.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		// Stop once we have what we need or after a generous prefix —
		// session files are append-only, the first user message and cwd
		// always appear near the start. We always read at least the
		// first line so the isSidechain content fallback runs.
		if firstUserMsg != "" && cwd != "" && seenLines > 0 {
			break
		}
		if seenLines > 200 {
			break
		}
		seenLines++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Type        string          `json:"type"`
			Cwd         string          `json:"cwd"`
			Message     json.RawMessage `json:"message"`
			IsSidechain *bool           `json:"isSidechain"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if cwd == "" && rec.Cwd != "" {
			cwd = rec.Cwd
		}
		if !agent && rec.IsSidechain != nil && *rec.IsSidechain {
			// Content fallback for layouts where subagent transcripts
			// land at the top level; the path check has already run.
			agent = true
		}
		if firstUserMsg == "" && rec.Type == "user" && len(rec.Message) > 0 {
			firstUserMsg = extractClaudeMessageText(rec.Message)
		}
	}

	if firstUserMsg == "" && cwd == "" {
		return model.Item{}, false
	}

	project := claudeProjectLabel(cwd, encodedDir)
	preview := parse.SessionPreview(firstUserMsg, 80)
	if preview == "" {
		preview = "(no user prompt)"
	}

	private := parse.IsPrivateSessionCwd(cwd)
	scope := model.ScopeGlobal
	if !private && parse.SessionIsLocal(cwd, projectDir) {
		scope = model.ScopeLocal
	}

	desc := fmt.Sprintf("%s · %s", project, parse.SessionFriendlyTime(info.ModTime()))
	meta := map[string]string{
		"sessionId":   sessionID,
		"cwd":         cwd,
		"project":     project,
		"lastUpdated": info.ModTime().UTC().Format(time.RFC3339),
	}
	// PRI-31: read per-session usage, compute cost via embedded rates,
	// stash both on Meta so the renderer + footer aggregator can pick
	// them up without re-walking the .jsonl. ReadClaudeUsage caches by
	// (path, mtime) so this stays cheap on repeated launches.
	if usage, err := parse.ReadClaudeUsage(path); err == nil && usage.Messages > 0 {
		meta["usage_model"] = usage.Model
		meta["usage_input"] = strconv.FormatInt(usage.InputTokens, 10)
		meta["usage_output"] = strconv.FormatInt(usage.OutputTokens, 10)
		meta["usage_cache_create"] = strconv.FormatInt(usage.CacheCreateTokens, 10)
		meta["usage_cache_read"] = strconv.FormatInt(usage.CacheReadTokens, 10)
		if cost, ok := pricing.Cost(usage); ok {
			meta["cost_usd"] = strconv.FormatFloat(cost, 'f', 4, 64)
			desc = fmt.Sprintf("%s · $%.2f", desc, cost)
		} else {
			meta["cost_unpriced"] = "1"
			desc = fmt.Sprintf("%s · %s tok (unpriced)", desc, parse.FormatTokens(usage.Total()))
		}
	}

	return model.Item{
		Origin:      model.OriginClaude,
		Kind:        model.KindSession,
		Scope:       scope,
		Private:     private,
		Agent:       agent,
		Name:        preview,
		Path:        path,
		Description: desc,
		Body:        parse.SessionBody(firstUserMsg, project, sessionID, info.ModTime()),
		Storage:     model.StorageFile,
		ConfigKey:   sessionID,
		Meta:        meta,
	}, true
}

// isAgentPath reports whether `path` lives under a Claude
// `<sessionId>/subagents/` directory — the canonical layout for
// Task-tool spawn transcripts. Cheap path-only check; no I/O. The
// content-side fallback in readClaudeSession (`isSidechain: true` in
// the first message) handles older / alternative layouts.
func isAgentPath(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/subagents/")
}

// extractClaudeMessageText handles both shapes the Claude jsonl uses
// for message.content: a bare string ("hello") or an array of blocks
// ([{type:"text", text:"hello"}, ...]).
func extractClaudeMessageText(raw json.RawMessage) string {
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return ""
	}
	if len(msg.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(msg.Content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(msg.Content, &blocks); err == nil {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return b.Text
			}
		}
	}
	return ""
}

// claudeProjectLabel picks a human-readable project name. The directory
// name is a lossy encoding (every non-alphanumeric → "-"), so we prefer
// the cwd recorded inside the jsonl when available and fall back to the
// directory basename otherwise.
func claudeProjectLabel(cwd, encoded string) string {
	if cwd != "" {
		return filepath.Base(cwd)
	}
	return encoded
}
