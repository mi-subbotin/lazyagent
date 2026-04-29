package gemini

import (
	"crypto/sha256"
	"encoding/hex"
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
)

// cwdHash returns the per-project directory name Gemini CLI uses
// under ~/.gemini/tmp/. Confirmed empirically — Gemini hashes the
// absolute cwd via plain SHA-256 (no salt, no separator) and
// hex-encodes the digest.
func cwdHash(cwd string) string {
	h := sha256.Sum256([]byte(cwd))
	return hex.EncodeToString(h[:])
}

// privateCwdHashSet precomputes hashes for cwd values that always
// land in the Private bucket. Gemini's transcript JSON doesn't
// preserve cwd, only its hash, so we can't classify by path content
// the way we do for Claude — we can only check exact-match against
// well-known directories. Subdirs of these (e.g. /tmp/scratch) leak
// into Global; that's acceptable since a precomputed prefix-match
// over an unbounded subdir space isn't possible without listing the
// whole filesystem.
func privateCwdHashSet() map[string]struct{} {
	roots := []string{"/tmp", "/private/tmp", "/var/tmp"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		for _, sub := range []string{".claude", ".codex", ".gemini", ".lazyagent"} {
			roots = append(roots, filepath.Join(home, sub))
		}
	}
	out := make(map[string]struct{}, len(roots))
	for _, r := range roots {
		out[cwdHash(r)] = struct{}{}
	}
	return out
}

// geminiSessionFile mirrors the on-disk JSON shape produced by the
// Gemini CLI for ~/.gemini/tmp/<projectHash>/chats/session-*.json.
// Fields we don't consume are omitted to keep the decode tolerant.
type geminiSessionFile struct {
	SessionID   string `json:"sessionId"`
	ProjectHash string `json:"projectHash"`
	StartTime   string `json:"startTime"`
	LastUpdated string `json:"lastUpdated"`
	Messages    []struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	} `json:"messages"`
}

// scanSessions enumerates Gemini chat transcripts under
// ~/.gemini/tmp/<projectHash>/chats/. Gemini's resume CLI uses a
// per-project numeric index ("--resume 1" = newest in this project),
// so adapter sorts within each projectHash and stamps Meta["index"]
// before merging the per-project lists into one global stream.
func scanSessions(geminiHome, projectDir string) []model.Item {
	tmpDir := filepath.Join(geminiHome, "tmp")
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil
	}
	localHash := ""
	if projectDir != "" {
		localHash = cwdHash(projectDir)
	}
	privateHashes := privateCwdHashSet()
	var out []model.Item
	for _, hashDir := range entries {
		if !hashDir.IsDir() {
			continue
		}
		chatsDir := filepath.Join(tmpDir, hashDir.Name(), "chats")
		files, err := os.ReadDir(chatsDir)
		if err != nil {
			continue
		}
		isLocal := localHash != "" && hashDir.Name() == localHash
		_, isPrivate := privateHashes[hashDir.Name()]
		var perProject []model.Item
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			full := filepath.Join(chatsDir, f.Name())
			it, ok := readGeminiSession(full, hashDir.Name(), isLocal, isPrivate)
			if !ok {
				continue
			}
			perProject = append(perProject, it)
		}
		// Newest first → index 1 is the freshest; matches `gemini --resume 1`.
		sort.SliceStable(perProject, func(i, j int) bool {
			return perProject[i].Meta["lastUpdated"] > perProject[j].Meta["lastUpdated"]
		})
		for i := range perProject {
			perProject[i].Meta["index"] = strconv.Itoa(i + 1)
		}
		out = append(out, perProject...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Meta["lastUpdated"] > out[j].Meta["lastUpdated"]
	})
	return out
}

func readGeminiSession(path, projectHash string, isLocal, isPrivate bool) (model.Item, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.Item{}, false
	}
	var s geminiSessionFile
	if err := json.Unmarshal(data, &s); err != nil {
		return model.Item{}, false
	}
	if s.SessionID == "" && len(s.Messages) == 0 {
		return model.Item{}, false
	}
	var firstUser string
	for _, m := range s.Messages {
		if m.Type == "user" {
			firstUser = m.Content
			break
		}
	}

	// Gemini doesn't record cwd in the transcript, only a hash of it.
	// Show a short hash prefix so the user can still tell projects apart
	// in the list. PRI-5 follow-up: optionally pre-compute hashes for
	// known project dirs from ~/.lazyagent/config.toml so this becomes a
	// real path.
	project := projectHash
	if len(project) > 8 {
		project = project[:8]
	}

	last := s.LastUpdated
	if last == "" {
		last = s.StartTime
	}
	mod := time.Time{}
	if t, err := time.Parse(time.RFC3339Nano, last); err == nil {
		mod = t
	} else if info, err := os.Stat(path); err == nil {
		mod = info.ModTime()
	}

	preview := parse.SessionPreview(firstUser, 80)
	if preview == "" {
		preview = "(no user prompt)"
	}

	scope := model.ScopeGlobal
	if !isPrivate && isLocal {
		scope = model.ScopeLocal
	}

	return model.Item{
		Origin:      model.OriginGemini,
		Kind:        model.KindSession,
		Scope:       scope,
		Private:     isPrivate,
		Name:        preview,
		Path:        path,
		Description: fmt.Sprintf("%s · %s", project, parse.SessionFriendlyTime(mod)),
		Body:        parse.SessionBody(firstUser, project, s.SessionID, mod),
		Storage:     model.StorageFile,
		ConfigKey:   s.SessionID,
		Meta: map[string]string{
			"sessionId":   s.SessionID,
			"projectHash": projectHash,
			"project":     project,
			"lastUpdated": mod.UTC().Format(time.RFC3339),
		},
	}, true
}
