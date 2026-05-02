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
	"github.com/mi-subbotin/lazyagent/internal/pricing"
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
// ~/.gemini/tmp/<bucket>/chats/. Gemini's resume CLI uses a per-project
// numeric index ("--resume 1" = newest in this project), so adapter
// sorts within each bucket and stamps Meta["index"] before merging the
// per-project lists into one global stream.
//
// Bucket naming changed across CLI versions: older Gemini releases used
// sha256(cwd), newer ones (≥0.40) use the cwd basename plus a sibling
// `.project_root` text file holding the absolute cwd. Both shapes can
// coexist in the same tmp dir on a long-lived install. We treat the
// dir name as opaque and rely on the JSON's own `projectHash` field
// for the real hash, plus `.project_root` (when present) for the real
// cwd.
func scanSessions(geminiHome, projectDir string) []model.Item {
	tmpDir := filepath.Join(geminiHome, "tmp")
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil
	}
	privateHashes := privateCwdHashSet()
	var out []model.Item
	for _, dirEnt := range entries {
		if !dirEnt.IsDir() {
			continue
		}
		dirName := dirEnt.Name()
		bucketDir := filepath.Join(tmpDir, dirName)
		chatsDir := filepath.Join(bucketDir, "chats")
		files, err := os.ReadDir(chatsDir)
		if err != nil {
			continue
		}
		cwdFromMarker := readProjectRoot(bucketDir)
		var perProject []model.Item
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			full := filepath.Join(chatsDir, f.Name())
			it, ok := readGeminiSession(full, dirName, cwdFromMarker, projectDir, privateHashes)
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

// readProjectRoot returns the cwd recorded in `<bucketDir>/.project_root`
// (newer Gemini releases) or "" when the marker is absent. The file is
// a single line of plain text; we trim trailing newlines so the value
// can be compared with filepath strings directly.
func readProjectRoot(bucketDir string) string {
	data, err := os.ReadFile(filepath.Join(bucketDir, ".project_root"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readGeminiSession(path, dirName, cwdFromMarker, projectDir string, privateHashes map[string]struct{}) (model.Item, bool) {
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

	// The canonical projectHash always lives in the JSON body and is
	// sha256(cwd). Older Gemini releases happened to also use it as the
	// parent directory name; newer ones don't, so we always trust the
	// JSON over the directory name. Fall back to the dir name only when
	// the file omits the field (very old shapes).
	projectHash := s.ProjectHash
	if projectHash == "" {
		projectHash = dirName
	}

	// Local classification: prefer the marker file when present (exact
	// path match), fall back to sha256(projectDir) match for buckets
	// that predate `.project_root`.
	isLocal := false
	if projectDir != "" {
		switch {
		case cwdFromMarker != "" && cwdFromMarker == projectDir:
			isLocal = true
		case projectHash == cwdHash(projectDir):
			isLocal = true
		}
	}

	// Private classification can be triggered by either the recovered
	// path (newer layout) or the precomputed hash set (older layout).
	isPrivate := false
	if cwdFromMarker != "" {
		isPrivate = parse.IsPrivateSessionCwd(cwdFromMarker)
	}
	if !isPrivate {
		_, isPrivate = privateHashes[projectHash]
	}

	// Project label for the detail panel: real basename when we know
	// the cwd, hash prefix otherwise.
	project := projectHash
	if cwdFromMarker != "" {
		project = filepath.Base(cwdFromMarker)
	} else if len(project) > 8 {
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

	meta := map[string]string{
		"sessionId":   s.SessionID,
		"projectHash": projectHash,
		"project":     project,
		"lastUpdated": mod.UTC().Format(time.RFC3339),
	}
	if cwdFromMarker != "" {
		meta["cwd"] = cwdFromMarker
	}

	desc := fmt.Sprintf("%s · %s", project, parse.SessionFriendlyTime(mod))

	// PRI-63: per-message usage grouped by model. Sessions can switch
	// models mid-flight, so we sum cost across the per-model entries
	// rather than pricing one aggregate against a single rate.
	if usages, err := parse.ReadGeminiUsage(path); err == nil && len(usages) > 0 {
		// Pick the most-recent model for the displayed `usage_model`.
		latest := usages[len(usages)-1]
		meta["usage_model"] = latest.Model
		var inSum, outSum, cacheSum int64
		for _, u := range usages {
			inSum += u.InputTokens
			outSum += u.OutputTokens
			cacheSum += u.CacheReadTokens
		}
		meta["usage_input"] = strconv.FormatInt(inSum, 10)
		meta["usage_output"] = strconv.FormatInt(outSum, 10)
		meta["usage_cache_read"] = strconv.FormatInt(cacheSum, 10)
		cost, allPriced := pricing.SumCost(usages)
		if allPriced && cost > 0 {
			meta["cost_usd"] = strconv.FormatFloat(cost, 'f', 4, 64)
			desc = fmt.Sprintf("%s · $%.2f", desc, cost)
		} else if cost > 0 {
			meta["cost_usd"] = strconv.FormatFloat(cost, 'f', 4, 64)
			meta["cost_partial"] = "1"
			desc = fmt.Sprintf("%s · $%.2f+", desc, cost)
		} else if total := pricing.SumTokens(usages); total > 0 {
			meta["cost_unpriced"] = "1"
			desc = fmt.Sprintf("%s · %s tok (unpriced)", desc, parse.FormatTokens(total))
		}
	}

	return model.Item{
		Origin:      model.OriginGemini,
		Kind:        model.KindSession,
		Scope:       scope,
		Private:     isPrivate,
		Name:        preview,
		Path:        path,
		Description: desc,
		Body:        parse.SessionBody(firstUser, project, s.SessionID, mod),
		Storage:     model.StorageFile,
		ConfigKey:   s.SessionID,
		Meta:        meta,
	}, true
}
