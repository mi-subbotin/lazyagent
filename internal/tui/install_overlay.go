package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mi-subbotin/lazyagent/internal/install"
	"github.com/mi-subbotin/lazyagent/internal/model"
)

// installPhase tracks where the user is in the install overlay's
// step-through wizard. Each phase has its own key map and view; the
// transitions only ever go forward except for esc, which always
// closes the overlay.
type installPhase int

const (
	phaseInstallURL     installPhase = iota // typing the github URL
	phaseInstallFetch                       // download running, waiting on async msg
	phaseInstallPick                        // candidate checklist
	phaseInstallTarget                      // origin/scope chooser
	phaseInstallConfirm                     // per-item r/k/s for conflicts
	phaseInstallDone                        // result summary, esc to close
)

// installConflict tracks one preflight collision: a candidate whose
// resolved destination already exists. The user picks replace/keep/skip
// before we touch the disk.
type installConflict struct {
	cand   install.Candidate
	target string // pre-resolved destination
	choice byte   // 'r' / 'k' / 's' / 0 = undecided
}

// installTargetOption is one row in the target chooser. We pre-build
// the list so combinations the install package would refuse (codex
// agents, gemini prompts) are visibly disabled instead of silently
// filtered — same UX pattern crossPicker uses.
type installTargetOption struct {
	origin   string
	scope    string
	disabled bool
	reason   string
}

func (o installTargetOption) label() string {
	s := fmt.Sprintf("%s (%s)", o.origin, o.scope)
	if o.disabled {
		s += " — " + o.reason
	}
	return s
}

// installOverlay is the modal state for `i`. The overlay carries
// everything the wizard needs across phases — typed URL, resolved
// spec/sha, fetched candidates with selection state, target choice
// and any pre-flight conflict decisions.
type installOverlay struct {
	phase installPhase

	url string

	spec     *install.Spec
	sha      string
	cacheDir string

	candidates []install.Candidate
	selected   []bool
	cursor     int

	targetOpts   []installTargetOption
	targetCursor int

	conflicts      []installConflict
	conflictCursor int

	summary []string
	err     string
}

func newInstallOverlay() *installOverlay {
	return &installOverlay{phase: phaseInstallURL}
}

// installFetchedMsg lands when the async fetch goroutine returns.
// Either err is set or {spec, sha, cacheDir, candidates} are filled.
type installFetchedMsg struct {
	spec       *install.Spec
	sha        string
	cacheDir   string
	candidates []install.Candidate
	err        error
}

// installAppliedMsg lands when Apply (and any uninstall/manifest
// bookkeeping) finishes for one wizard run.
type installAppliedMsg struct {
	summary []string
	err     error
}

// runInstallFetch is the tea.Cmd that does the network hit. Bubbletea
// runs this off the main goroutine so the UI stays responsive — the
// view shows "downloading…" until the message arrives.
func runInstallFetch(rawURL string) tea.Cmd {
	return func() tea.Msg {
		spec, err := install.ParseURL(rawURL)
		if err != nil {
			return installFetchedMsg{err: fmt.Errorf("url: %w", err)}
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return installFetchedMsg{err: err}
		}
		cacheDir := filepath.Join(home, ".lazyagent", "cache")
		client := install.NewClient(cacheDir)

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		sha, err := client.Resolve(ctx, spec)
		if err != nil {
			return installFetchedMsg{err: fmt.Errorf("resolve: %w", err)}
		}
		repoDir, err := client.Fetch(ctx, spec, sha)
		if err != nil {
			return installFetchedMsg{err: fmt.Errorf("fetch: %w", err)}
		}
		cands, err := install.Inspect(repoDir, spec)
		if err != nil {
			return installFetchedMsg{err: fmt.Errorf("inspect: %w", err)}
		}
		if len(cands) == 0 {
			return installFetchedMsg{err: errors.New("no installable items found")}
		}
		return installFetchedMsg{spec: spec, sha: sha, cacheDir: repoDir, candidates: cands}
	}
}

// runInstallApply does the per-candidate Apply pass and writes the
// manifest. Conflicts already have their choice recorded — replace
// uses Overwrite=true, keep uses Overwrite=false (which will error
// and we re-skip), skip filters them out before this runs.
func runInstallApply(ov *installOverlay, originURL string) tea.Cmd {
	return func() tea.Msg {
		manifestPath, err := install.DefaultPath()
		if err != nil {
			return installAppliedMsg{err: err}
		}
		manifest, err := install.Load(manifestPath)
		if err != nil {
			return installAppliedMsg{err: err}
		}
		conflictByPath := map[string]byte{}
		for _, c := range ov.conflicts {
			conflictByPath[c.target] = c.choice
		}

		var summary []string
		target := installTargetFromOpt(ov.targetOpts[ov.targetCursor])
		for i, c := range ov.candidates {
			if !ov.selected[i] {
				continue
			}
			dst, err := install.ResolvePath(c, target)
			if err != nil {
				summary = append(summary, fmt.Sprintf("skip %s: %v", c.Name, err))
				continue
			}
			overwrite := false
			if choice, ok := conflictByPath[dst]; ok {
				switch choice {
				case 's':
					summary = append(summary, fmt.Sprintf("skip %s (kept existing)", c.Name))
					continue
				case 'r':
					overwrite = true
				}
			}
			entry, err := install.Apply(ov.cacheDir, c, target, originURL, ov.sha, install.ApplyOptions{Overwrite: overwrite})
			if err != nil {
				summary = append(summary, fmt.Sprintf("skip %s: %v", c.Name, err))
				continue
			}
			manifest.Add(entry)
			summary = append(summary, fmt.Sprintf("installed %s -> %s", entry.Name, entry.TargetPath))
		}
		if err := manifest.Save(manifestPath); err != nil {
			return installAppliedMsg{summary: summary, err: fmt.Errorf("save manifest: %w", err)}
		}
		return installAppliedMsg{summary: summary}
	}
}

func installTargetFromOpt(opt installTargetOption) install.Target {
	cwd, _ := os.Getwd()
	return install.Target{Origin: opt.origin, Scope: opt.scope, ProjectDir: cwd}
}

// buildInstallTargets pre-computes the row-by-row option list so
// combinations install.ResolvePath would reject are visibly greyed
// out with a one-word reason.
func buildInstallTargets(cand install.Candidate, hasProject bool) []installTargetOption {
	var opts []installTargetOption
	for _, origin := range []string{"claude", "codex", "gemini", "shared"} {
		for _, scope := range []string{"global", "local"} {
			if origin == "shared" && scope == "local" {
				continue
			}
			opt := installTargetOption{origin: origin, scope: scope}
			if scope == "local" && !hasProject {
				opt.disabled = true
				opt.reason = "no project dir"
			} else if _, err := install.ResolvePath(cand, install.Target{
				Origin: origin, Scope: scope, ProjectDir: "/tmp/probe",
			}); err != nil {
				opt.disabled = true
				opt.reason = trimReason(err.Error())
			}
			opts = append(opts, opt)
		}
	}
	return opts
}

func trimReason(s string) string {
	// ResolvePath errors are descriptive; keep them short for the row label.
	if i := strings.Index(s, " — "); i >= 0 {
		return s[:i]
	}
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

// preflightConflicts probes each selected candidate's destination and
// returns those that already exist. The picker overlay opens for those
// before any bytes are copied so the user decides per-item.
func preflightConflicts(ov *installOverlay, target install.Target) []installConflict {
	var out []installConflict
	for i, c := range ov.candidates {
		if !ov.selected[i] {
			continue
		}
		dst, err := install.ResolvePath(c, target)
		if err != nil {
			continue
		}
		if dirOrFileExists(dst, c.Storage) {
			out = append(out, installConflict{cand: c, target: dst})
		}
	}
	return out
}

func dirOrFileExists(p string, storage model.Storage) bool {
	if storage == model.StorageDir {
		_, err := os.Stat(filepath.Dir(p))
		return err == nil
	}
	_, err := os.Stat(p)
	return err == nil
}

// updateInstall is the modal Update for the install overlay. It is
// the analogue of updateCreate / updateSharePicker — keystrokes route
// here while m.installing != nil.
func (m Model) updateInstall(msg tea.KeyMsg) (Model, tea.Cmd) {
	ov := m.installing
	switch ov.phase {
	case phaseInstallURL:
		switch msg.String() {
		case "esc":
			m.installing = nil
			return m, nil
		case "enter":
			ov.url = strings.TrimSpace(ov.url)
			if ov.url == "" {
				ov.err = "URL is required"
				return m, nil
			}
			ov.err = ""
			ov.phase = phaseInstallFetch
			return m, runInstallFetch(ov.url)
		case "backspace":
			if ov.url != "" {
				ov.url = ov.url[:len(ov.url)-1]
			}
			return m, nil
		default:
			if r := msg.Runes; len(r) > 0 {
				ov.url += string(r)
			}
			return m, nil
		}

	case phaseInstallFetch:
		// Anything other than esc is ignored while the fetch is in
		// flight — wait for installFetchedMsg.
		if msg.String() == "esc" {
			m.installing = nil
		}
		return m, nil

	case phaseInstallPick:
		switch msg.String() {
		case "esc":
			m.installing = nil
			return m, nil
		case "up", "k":
			if ov.cursor > 0 {
				ov.cursor--
			}
			return m, nil
		case "down", "j":
			if ov.cursor < len(ov.candidates)-1 {
				ov.cursor++
			}
			return m, nil
		case " ":
			ov.selected[ov.cursor] = !ov.selected[ov.cursor]
			return m, nil
		case "a":
			// toggle all
			anyOff := false
			for _, s := range ov.selected {
				if !s {
					anyOff = true
					break
				}
			}
			for i := range ov.selected {
				ov.selected[i] = anyOff
			}
			return m, nil
		case "enter":
			anyChosen := false
			for _, s := range ov.selected {
				if s {
					anyChosen = true
					break
				}
			}
			if !anyChosen {
				ov.err = "select at least one item"
				return m, nil
			}
			ov.err = ""
			// Build target options based on the first selected
			// candidate; the rest of the selection follows the same
			// origin/kind constraints in practice (skills+agents+prompts
			// each pick valid origins independently). For mixed kinds
			// we'd need per-item targeting — left for a follow-up.
			firstSel := -1
			for i, s := range ov.selected {
				if s {
					firstSel = i
					break
				}
			}
			ov.targetOpts = buildInstallTargets(ov.candidates[firstSel], m.projectDir != "")
			ov.targetCursor = firstEnabled(ov.targetOpts)
			ov.phase = phaseInstallTarget
			return m, nil
		}
		return m, nil

	case phaseInstallTarget:
		switch msg.String() {
		case "esc":
			ov.phase = phaseInstallPick
			return m, nil
		case "up", "k":
			ov.targetCursor = prevEnabled(ov.targetOpts, ov.targetCursor)
			return m, nil
		case "down", "j":
			ov.targetCursor = nextEnabled(ov.targetOpts, ov.targetCursor)
			return m, nil
		case "enter":
			opt := ov.targetOpts[ov.targetCursor]
			if opt.disabled {
				return m, nil
			}
			target := installTargetFromOpt(opt)
			conflicts := preflightConflicts(ov, target)
			if len(conflicts) == 0 {
				ov.phase = phaseInstallFetch // re-use as "applying" spinner phase
				return m, runInstallApply(ov, ov.url)
			}
			ov.conflicts = conflicts
			ov.conflictCursor = 0
			ov.phase = phaseInstallConfirm
			return m, nil
		}
		return m, nil

	case phaseInstallConfirm:
		switch strings.ToLower(msg.String()) {
		case "esc":
			ov.phase = phaseInstallTarget
			ov.conflicts = nil
			return m, nil
		case "r", "k", "s":
			ov.conflicts[ov.conflictCursor].choice = msg.String()[0]
			if ov.conflictCursor < len(ov.conflicts)-1 {
				ov.conflictCursor++
				return m, nil
			}
			// All conflicts resolved — apply.
			ov.phase = phaseInstallFetch
			return m, runInstallApply(ov, ov.url)
		}
		return m, nil

	case phaseInstallDone:
		// Any key closes.
		m.installing = nil
		// Reload sources so newly-installed items appear in the tree
		// without a manual `r`.
		m.loading = true
		return m, m.loadCmd()
	}
	return m, nil
}

func firstEnabled(opts []installTargetOption) int {
	for i, o := range opts {
		if !o.disabled {
			return i
		}
	}
	return 0
}

func nextEnabled(opts []installTargetOption, cur int) int {
	for i := cur + 1; i < len(opts); i++ {
		if !opts[i].disabled {
			return i
		}
	}
	return cur
}

func prevEnabled(opts []installTargetOption, cur int) int {
	for i := cur - 1; i >= 0; i-- {
		if !opts[i].disabled {
			return i
		}
	}
	return cur
}

// installOverlayText renders the wizard for the current phase. View
// embeds this inside the standard centered overlay frame.
func installOverlayText(ov *installOverlay) string {
	var b strings.Builder
	switch ov.phase {
	case phaseInstallURL:
		fmt.Fprintln(&b, titleStyle.Render("Install from GitHub"))
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Paste a github.com / gist URL or owner/repo shorthand, then enter.")
		fmt.Fprintln(&b, "Examples:")
		fmt.Fprintln(&b, dimStyle.Render("  mattpocock/skills"))
		fmt.Fprintln(&b, dimStyle.Render("  github.com/anthropics/skills"))
		fmt.Fprintln(&b, dimStyle.Render("  github.com/foo/bar/tree/main/skills/cool"))
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "URL: "+ov.url+"_")
		if ov.err != "" {
			fmt.Fprintln(&b)
			fmt.Fprintln(&b, invalidStyle.Render(ov.err))
		}
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, dimStyle.Render("enter — fetch | esc — cancel"))
	case phaseInstallFetch:
		fmt.Fprintln(&b, titleStyle.Render("Install from GitHub"))
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Working on "+ov.url+" …")
		fmt.Fprintln(&b, dimStyle.Render("(this may take a few seconds)"))
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, dimStyle.Render("esc — cancel"))
	case phaseInstallPick:
		fmt.Fprintln(&b, titleStyle.Render("Install from GitHub"))
		fmt.Fprintln(&b, dimStyle.Render(fmt.Sprintf("ref %s — %d candidate(s)", ov.sha[:short(len(ov.sha), 8)], len(ov.candidates))))
		fmt.Fprintln(&b)
		for i, c := range ov.candidates {
			marker := "[ ]"
			if ov.selected[i] {
				marker = "[x]"
			}
			line := fmt.Sprintf("%s  %-7s  %-24s  %s", marker, c.Kind, c.Name, c.Description)
			if c.ParseError != "" {
				line += " " + invalidStyle.Render("(invalid)")
			}
			if i == ov.cursor {
				line = selectedStyle.Render(line)
			}
			fmt.Fprintln(&b, line)
		}
		if ov.err != "" {
			fmt.Fprintln(&b)
			fmt.Fprintln(&b, invalidStyle.Render(ov.err))
		}
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, dimStyle.Render("space — toggle | a — toggle all | enter — pick target | esc — cancel"))
	case phaseInstallTarget:
		fmt.Fprintln(&b, titleStyle.Render("Install — pick target"))
		fmt.Fprintln(&b)
		for i, opt := range ov.targetOpts {
			line := "  " + opt.label()
			switch {
			case opt.disabled:
				line = dimStyle.Render(line)
			case i == ov.targetCursor:
				line = selectedStyle.Render(line)
			}
			fmt.Fprintln(&b, line)
		}
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, dimStyle.Render("enter — confirm | esc — back"))
	case phaseInstallConfirm:
		fmt.Fprintln(&b, titleStyle.Render("Install — resolve conflicts"))
		c := ov.conflicts[ov.conflictCursor]
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "%s already exists at:\n  %s\n\n", c.cand.Name, c.target)
		fmt.Fprintf(&b, "Conflict %d / %d. What now?\n", ov.conflictCursor+1, len(ov.conflicts))
		fmt.Fprintln(&b, dimStyle.Render("  r — replace  k — keep existing  s — skip"))
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, dimStyle.Render("esc — back"))
	case phaseInstallDone:
		fmt.Fprintln(&b, titleStyle.Render("Install — done"))
		fmt.Fprintln(&b)
		if ov.err != "" {
			fmt.Fprintln(&b, invalidStyle.Render("error: "+ov.err))
			fmt.Fprintln(&b)
		}
		for _, line := range ov.summary {
			fmt.Fprintln(&b, line)
		}
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, dimStyle.Render("any key — close"))
	}
	return lipgloss.NewStyle().Render(b.String())
}

func short(n, max int) int {
	if n < max {
		return n
	}
	return max
}
