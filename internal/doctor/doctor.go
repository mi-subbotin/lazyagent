// Package doctor runs an external LLM CLI (claude / codex / gemini)
// against the user's lazyagent items to surface duplicates, unused
// entries, and other cleanup hints. v1 is read-only: results are
// persisted to ~/.lazyagent/doctor-<unix-ts>.json for the user to
// review manually.
package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/templates"
)

var (
	ErrCLINotFound       = errors.New("no LLM CLI in PATH")
	ErrNoRecommendations = errors.New("no doctor recommendations on disk")
)

func loadPromptTemplate() (string, error) {
	b, err := templates.Read("doctor.md")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CLI describes one LLM CLI binary located on PATH.
type CLI struct {
	Name   string
	Origin model.Origin
	Path   string
}

// Recommendations is the parsed JSON the LLM returns plus metadata.
type Recommendations struct {
	Created    time.Time            `json:"created"`
	CLI        string               `json:"cli"`
	Duplicates []DupSuggestion      `json:"duplicates"`
	Unused     []UnusedSuggestion   `json:"unused"`
	Other      []FreeFormSuggestion `json:"other"`
}

type DupSuggestion struct {
	Names  []string `json:"names"`
	Reason string   `json:"reason"`
}

type UnusedSuggestion struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

type FreeFormSuggestion struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// candidates lists the supported CLIs in default preference order.
var candidates = []struct {
	name   string
	origin model.Origin
}{
	{"claude", model.OriginClaude},
	{"codex", model.OriginCodex},
	{"gemini", model.OriginGemini},
}

// Detect returns LLM CLIs available on PATH, sorted so that the CLI
// matching the origin with the most items in `items` comes first.
func Detect(items []model.Item) []CLI {
	counts := map[model.Origin]int{}
	for _, it := range items {
		counts[it.Origin]++
	}
	found := make([]CLI, 0, len(candidates))
	for _, c := range candidates {
		path, err := exec.LookPath(c.name)
		if err != nil {
			continue
		}
		found = append(found, CLI{Name: c.name, Origin: c.origin, Path: path})
	}
	sort.SliceStable(found, func(i, j int) bool {
		return counts[found[i].Origin] > counts[found[j].Origin]
	})
	return found
}

// BuildPrompt renders the embedded prompt template with the items
// rendered as YAML blocks. Sessions and Memory are skipped.
func BuildPrompt(items []model.Item) (string, error) {
	src, err := loadPromptTemplate()
	if err != nil {
		return "", err
	}
	tmpl, err := template.New("doctor").Parse(src)
	if err != nil {
		return "", err
	}
	yaml := renderItemsYAML(items)
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{"ItemsYAML": yaml}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func renderItemsYAML(items []model.Item) string {
	var b strings.Builder
	for _, it := range items {
		if it.Kind == model.KindSession || it.Kind == model.KindMemory {
			continue
		}
		fmt.Fprintf(&b, "- kind: %s\n", it.Kind.String())
		fmt.Fprintf(&b, "  name: %s\n", it.Name)
		fmt.Fprintf(&b, "  origin: %s\n", it.Origin.String())
		fmt.Fprintf(&b, "  scope: %s\n", it.Scope.String())
		fmt.Fprintf(&b, "  description: %s\n", strconv.Quote(it.Description))
		fmt.Fprintf(&b, "  body_excerpt: %s\n", strconv.Quote(excerpt(it.Body, 200)))
	}
	return b.String()
}

func excerpt(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Run executes the CLI subprocess with the rendered prompt, parses
// the JSON response, saves it to ~/.lazyagent/doctor-<id>.json, and
// returns the id alongside the parsed recommendations.
func Run(ctx context.Context, items []model.Item, cli CLI) (string, Recommendations, error) {
	prompt, err := BuildPrompt(items)
	if err != nil {
		return "", Recommendations{}, fmt.Errorf("build prompt: %w", err)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}

	args := cliArgs(cli.Name, prompt)
	cmd := exec.CommandContext(ctx, cli.Path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", Recommendations{}, fmt.Errorf("%s: %w (stderr: %s)", cli.Name, err, truncate(stderr.String(), 1024))
	}

	rec, err := parseRecommendations(stdout.Bytes())
	if err != nil {
		return "", Recommendations{}, err
	}
	rec.Created = time.Now().UTC()
	rec.CLI = cli.Name

	id := strconv.FormatInt(rec.Created.Unix(), 10)
	if err := saveRecommendations(id, rec); err != nil {
		return "", Recommendations{}, err
	}
	return id, rec, nil
}

// cliArgs returns the argv tail for each supported CLI. Best-known
// invocations as of 2026-05: claude/gemini take a `-p` flag, codex
// uses the `exec` subcommand. All take the prompt as a single
// positional arg and write free-form text to stdout.
func cliArgs(name, prompt string) []string {
	switch name {
	case "codex":
		return []string{"exec", prompt}
	default:
		return []string{"-p", prompt}
	}
}

func parseRecommendations(stdout []byte) (Recommendations, error) {
	var rec Recommendations
	if err := json.Unmarshal(stdout, &rec); err == nil {
		return rec, nil
	}
	if start, end := firstJSONObject(stdout); start >= 0 {
		if err := json.Unmarshal(stdout[start:end+1], &rec); err == nil {
			return rec, nil
		}
	}
	return Recommendations{}, fmt.Errorf("could not parse JSON from CLI output: %s", truncate(string(stdout), 4096))
}

func firstJSONObject(b []byte) (int, int) {
	start := bytes.IndexByte(b, '{')
	end := bytes.LastIndexByte(b, '}')
	if start < 0 || end <= start {
		return -1, -1
	}
	return start, end
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func saveRecommendations(id string, rec Recommendations) error {
	dir, err := lazyagentHome()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "doctor-"+id+".json")
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func lazyagentHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lazyagent"), nil
}

// Latest returns the most recent saved Recommendations file. The id
// is the unix-timestamp portion of the filename.
func Latest() (string, Recommendations, error) {
	dir, err := lazyagentHome()
	if err != nil {
		return "", Recommendations{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", Recommendations{}, ErrNoRecommendations
		}
		return "", Recommendations{}, err
	}
	var bestID string
	var bestTS int64 = -1
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "doctor-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		idStr := strings.TrimSuffix(strings.TrimPrefix(name, "doctor-"), ".json")
		ts, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		if ts > bestTS {
			bestTS = ts
			bestID = idStr
		}
	}
	if bestTS < 0 {
		return "", Recommendations{}, ErrNoRecommendations
	}
	data, err := os.ReadFile(filepath.Join(dir, "doctor-"+bestID+".json"))
	if err != nil {
		return "", Recommendations{}, err
	}
	var rec Recommendations
	if err := json.Unmarshal(data, &rec); err != nil {
		return "", Recommendations{}, err
	}
	return bestID, rec, nil
}
