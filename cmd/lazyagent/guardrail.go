package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/config"
	"github.com/mi-subbotin/lazyagent/internal/guardrails"
	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
	"github.com/mi-subbotin/lazyagent/internal/sources"
	"github.com/mi-subbotin/lazyagent/internal/sources/claude"
	"github.com/mi-subbotin/lazyagent/internal/sources/codex"
	"github.com/mi-subbotin/lazyagent/internal/sources/gemini"
	"github.com/mi-subbotin/lazyagent/internal/sources/lazyagent"
)

// markerKey is the field added to every lazyagent-installed hook entry
// so uninstall can find and remove its own hooks without touching ones
// the user wrote by hand. Value: "guardrail/<name>".
const markerKey = "_lazyagent_marker"

// runGuardrail dispatches `lazyagent guardrail <list|eval|install|uninstall>`.
func runGuardrail(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: lazyagent guardrail <list|eval|install|uninstall> [flags]")
	}
	switch args[0] {
	case "list":
		return runGuardrailList(args[1:])
	case "eval":
		return runGuardrailEval(ctx, args[1:])
	case "install":
		return runGuardrailInstall(args[1:])
	case "uninstall":
		return runGuardrailUninstall(args[1:])
	default:
		return fmt.Errorf("unknown guardrail subcommand %q (try: list, eval, install, uninstall)", args[0])
	}
}

func runGuardrailList(args []string) error {
	fs := flag.NewFlagSet("guardrail list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	for _, g := range guardrails.All() {
		installed, _ := guardrailInstalled(g.Name(), "claude", "global", "")
		state := "not installed"
		if installed {
			state = "installed (claude/global)"
		}
		fmt.Printf("%s\t%s\t%s\n", g.Name(), state, g.Description())
	}
	return nil
}

// runGuardrailEval is the hook-callback path. It reads a Claude hook
// envelope from stdin, runs the named guardrail, and prints the
// matching response envelope to stdout.
func runGuardrailEval(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("guardrail eval", flag.ContinueOnError)
	name := fs.String("name", "", "guardrail name to evaluate")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return errors.New("guardrail eval: --name is required")
	}
	// Hooks are invoked on every SessionStart and stdout is parsed by
	// Claude — adapter slog warnings would otherwise leak onto the user
	// terminal. Silence the default logger here; the TUI path keeps its
	// regular file-backed logger.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
	g, ok := guardrails.Get(*name)
	if !ok {
		return fmt.Errorf("guardrail %q is not registered", *name)
	}

	cfg := config.Default()
	if path, err := config.DefaultPath(); err == nil {
		if loaded, _, lerr := config.Load(path); lerr == nil {
			cfg = loaded
		}
	}
	for _, d := range cfg.Guardrails.Disabled {
		if d == *name {
			return writeAllow(os.Stdout)
		}
	}
	g = applyConfigOverrides(g, cfg)

	// Empty-stdin tolerance: Claude isn't guaranteed to send a body for
	// every hook event, and a missing payload should not stop the user's
	// session. We fall back to an empty envelope.
	envelope := map[string]any{}
	if data, err := io.ReadAll(os.Stdin); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		_ = json.Unmarshal(data, &envelope)
	}

	cwd := stringField(envelope, "cwd")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	projectDir := detectProject(cwd)

	srcs := []sources.Source{claude.Source{}, codex.Source{}, gemini.Source{}, lazyagent.Source{}}
	var items []model.Item
	for _, s := range srcs {
		got, err := s.List(ctx, projectDir)
		if err != nil {
			continue
		}
		items = append(items, got...)
	}

	res := g.Evaluate(guardrails.EvalContext{
		Items:      items,
		ProjectDir: projectDir,
		HookEvent:  stringField(envelope, "hook_event_name"),
		RawInput:   envelope,
	})
	return writeResult(os.Stdout, res)
}

// applyConfigOverrides returns a guardrail with thresholds populated
// from cfg. Built-ins are zero-valued at registration so config wins.
func applyConfigOverrides(g guardrails.Guardrail, cfg *config.Config) guardrails.Guardrail {
	switch g.(type) {
	case guardrails.TooManySkills:
		return guardrails.TooManySkills{Threshold: cfg.Guardrails.TooManySkillsThreshold}
	case guardrails.MemoryBloat:
		return guardrails.MemoryBloat{MaxBytes: cfg.Guardrails.MemoryBloatBytes}
	}
	return g
}

func writeAllow(w io.Writer) error {
	return writeJSON(w, map[string]any{"continue": true})
}

func writeResult(w io.Writer, r guardrails.Result) error {
	switch r.Action {
	case guardrails.ActionWarn:
		return writeJSON(w, map[string]any{
			"continue":          true,
			"additionalContext": r.Message,
		})
	case guardrails.ActionBlock:
		return writeJSON(w, map[string]any{
			"continue":   false,
			"stopReason": r.Message,
		})
	}
	return writeAllow(w)
}

func writeJSON(w io.Writer, v map[string]any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// runGuardrailInstall idempotently appends a SessionStart hook entry to
// the chosen settings.json. Each entry carries our marker so uninstall
// can find it without touching the user's hand-written hooks.
func runGuardrailInstall(args []string) error {
	fs := flag.NewFlagSet("guardrail install", flag.ContinueOnError)
	name := fs.String("name", "", "guardrail name")
	tool := fs.String("tool", "claude", "target tool (only `claude` in MVP)")
	scope := fs.String("scope", "global", "target scope: global | local")
	project := fs.String("project", "", "project directory (required for --scope=local)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return errors.New("guardrail install: --name is required")
	}
	if _, ok := guardrails.Get(*name); !ok {
		return fmt.Errorf("guardrail %q is not registered", *name)
	}
	if *tool != "claude" {
		return fmt.Errorf("guardrail install: only --tool=claude is supported in MVP (got %q)", *tool)
	}
	settingsPath, err := settingsPathFor(*scope, *project)
	if err != nil {
		return err
	}

	data, format, err := parse.Read(settingsPath)
	if err != nil {
		if !errors.Is(err, iofs.ErrNotExist) {
			return err
		}
		data = map[string]any{}
		format = parse.FormatFromExt(settingsPath)
	}

	if hookEntryExists(data, *name) {
		fmt.Printf("guardrail %q already installed at %s\n", *name, settingsPath)
		return nil
	}

	hookEntry := map[string]any{
		"type":      "command",
		"command":   fmt.Sprintf("lazyagent guardrail eval --name=%s", *name),
		"timeout":   10,
		markerKey:   "guardrail/" + *name,
	}
	outer := map[string]any{
		"matcher": "",
		"hooks":   []any{hookEntry},
	}
	parse.Append(data, parse.JoinKey("hooks", "SessionStart"), outer)

	if err := parse.Write(settingsPath, data, format); err != nil {
		return err
	}
	fmt.Printf("guardrail %q installed at %s\n", *name, settingsPath)
	return nil
}

// runGuardrailUninstall walks SessionStart and removes any inner hook
// entry whose marker matches the requested name. Outer matcher groups
// that become empty are pruned to keep the file tidy.
func runGuardrailUninstall(args []string) error {
	fs := flag.NewFlagSet("guardrail uninstall", flag.ContinueOnError)
	name := fs.String("name", "", "guardrail name")
	tool := fs.String("tool", "claude", "target tool (only `claude` in MVP)")
	scope := fs.String("scope", "global", "target scope: global | local")
	project := fs.String("project", "", "project directory (required for --scope=local)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return errors.New("guardrail uninstall: --name is required")
	}
	if *tool != "claude" {
		return fmt.Errorf("guardrail uninstall: only --tool=claude is supported in MVP (got %q)", *tool)
	}
	settingsPath, err := settingsPathFor(*scope, *project)
	if err != nil {
		return err
	}

	data, format, err := parse.Read(settingsPath)
	if err != nil {
		if errors.Is(err, iofs.ErrNotExist) {
			fmt.Printf("guardrail %q is not installed (no settings.json at %s)\n", *name, settingsPath)
			return nil
		}
		return err
	}
	removed := removeHookByMarker(data, *name)
	if removed == 0 {
		fmt.Printf("guardrail %q is not installed at %s\n", *name, settingsPath)
		return nil
	}
	if err := parse.Write(settingsPath, data, format); err != nil {
		return err
	}
	fmt.Printf("guardrail %q removed from %s (%d entries)\n", *name, settingsPath, removed)
	return nil
}

func settingsPathFor(scope, project string) (string, error) {
	switch scope {
	case "global":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".claude", "settings.json"), nil
	case "local":
		if project == "" {
			return "", errors.New("--scope=local requires --project=<path>")
		}
		abs, err := filepath.Abs(project)
		if err != nil {
			return "", err
		}
		return filepath.Join(abs, ".claude", "settings.json"), nil
	}
	return "", fmt.Errorf("unknown scope %q (use global|local)", scope)
}

// hookEntryExists reports whether the marker for `name` is already
// present anywhere under hooks.SessionStart in data.
func hookEntryExists(data map[string]any, name string) bool {
	want := "guardrail/" + name
	for _, inner := range iterateSessionStartHooks(data) {
		if v, _ := inner[markerKey].(string); v == want {
			return true
		}
	}
	return false
}

// removeHookByMarker walks SessionStart and splices out every inner
// hook with the matching marker. Returns the number removed. Outer
// matcher groups left without any hooks are dropped along with empty
// SessionStart / hooks containers so re-running install starts from a
// clean state.
func removeHookByMarker(data map[string]any, name string) int {
	want := "guardrail/" + name
	hooksAny, ok := data["hooks"].(map[string]any)
	if !ok {
		return 0
	}
	sessionAny, ok := hooksAny["SessionStart"].([]any)
	if !ok {
		return 0
	}
	removed := 0
	keptOuter := sessionAny[:0]
	for _, outerAny := range sessionAny {
		outer, ok := outerAny.(map[string]any)
		if !ok {
			keptOuter = append(keptOuter, outerAny)
			continue
		}
		innerArr, _ := outer["hooks"].([]any)
		keptInner := innerArr[:0]
		for _, innerAny := range innerArr {
			inner, ok := innerAny.(map[string]any)
			if !ok {
				keptInner = append(keptInner, innerAny)
				continue
			}
			if v, _ := inner[markerKey].(string); v == want {
				removed++
				continue
			}
			keptInner = append(keptInner, innerAny)
		}
		if len(keptInner) == 0 {
			continue
		}
		outer["hooks"] = keptInner
		keptOuter = append(keptOuter, outer)
	}
	if removed == 0 {
		return 0
	}
	if len(keptOuter) == 0 {
		delete(hooksAny, "SessionStart")
	} else {
		hooksAny["SessionStart"] = keptOuter
	}
	if len(hooksAny) == 0 {
		delete(data, "hooks")
	}
	return removed
}

// iterateSessionStartHooks flattens hooks.SessionStart[*].hooks[*] for
// callers that just want to inspect every inner hook map.
func iterateSessionStartHooks(data map[string]any) []map[string]any {
	var out []map[string]any
	hooksAny, ok := data["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	sessionAny, ok := hooksAny["SessionStart"].([]any)
	if !ok {
		return nil
	}
	for _, outerAny := range sessionAny {
		outer, ok := outerAny.(map[string]any)
		if !ok {
			continue
		}
		inner, _ := outer["hooks"].([]any)
		for _, innerAny := range inner {
			if m, ok := innerAny.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

// guardrailInstalled is the read side used by `guardrail list` to
// surface install state. Quiet on missing files / parse errors — those
// just mean "not installed".
func guardrailInstalled(name, tool, scope, project string) (bool, error) {
	path, err := settingsPathFor(scope, project)
	if err != nil {
		return false, err
	}
	data, _, err := parse.Read(path)
	if err != nil {
		return false, nil
	}
	return hookEntryExists(data, name), nil
}
