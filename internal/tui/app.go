package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/mi-subbotin/lazyagent/internal/actions"
	"github.com/mi-subbotin/lazyagent/internal/budget"
	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
	"github.com/mi-subbotin/lazyagent/internal/sources"
	"github.com/mi-subbotin/lazyagent/internal/state"
	"github.com/mi-subbotin/lazyagent/internal/store"
)

type focus int

const (
	focusTree focus = iota
	focusDetail
)

type detailFormat int

const (
	formatAuto detailFormat = iota
	formatJSON
	formatTOML
)

type pendingKind int

const (
	pendDelete pendingKind = iota
	pendFix
)

type pendingOp struct {
	kind pendingKind
	item model.Item // snapshot — survives even if list reshuffles
	// fix carries the pre-flight FixPlan for pendFix so the confirm
	// overlay can show before/after bytes and updateConfirm can apply
	// them without re-running Fix (which is non-deterministic when
	// callers mutate the file between key presses).
	fix actions.FixPlan
}

// createOverlay drives the "create new item" flow opened with `n`. The
// caller pre-fills origin/kind/scope from the cursor's position in the
// tree so the user only has to type a name. err carries the latest
// validation message and is rendered red under the input.
type createOverlay struct {
	origin model.Origin
	kind   model.Kind
	scope  model.Scope
	name   string
	err    string
}

func (p pendingOp) verb() string {
	switch p.kind {
	case pendDelete:
		return "Delete"
	case pendFix:
		return "Fix"
	}
	return "?"
}

// node is a row in the visible (post-collapse) tree.
type node struct {
	depth     int
	label     string // stable identity / expansion key (full path for groups)
	display   string // optional override for what to render (e.g. "Skills (3)")
	collapsed bool   // whether this group is currently collapsed
	isGroup   bool
	itemIdx   int // index into Model.items for leaf rows; -1 for groups
	// PRI-20: marks per-section empty placeholders ("no skills yet").
	// Dim styling and inert under activate() so the user can land on
	// it without triggering anything destructive.
	isEmpty bool
}

type Model struct {
	srcs       []sources.Source
	projectDir string

	items []model.Item
	tree  []node // visible rows
	// expansion state keyed by full group path (e.g. "Claude/Skills/Global")
	expanded map[string]bool

	cursor       int
	focus        focus
	detailScroll int
	detailFmt    detailFormat

	// Filter: when filterMode is true, keystrokes edit filterText. The
	// tree is rebuilt to only contain items whose name (or description)
	// matches filterText (case-insensitive substring); empty groups are
	// hidden. Esc cancels and clears, Enter commits and exits the editor
	// while keeping the active filter.
	filterMode bool
	filterText string

	helpOpen bool

	// detailFull is true when the user has "zoomed into" an item: the
	// detail panel takes the full inner area and the tree is hidden.
	// j/k scroll the body, t toggles JSON/TOML, esc returns to split.
	detailFull bool

	// placePicker is non-nil while the unified place overlay is open
	// (key `p`). Replaces the legacy copy/move/cross/share keys with a
	// single Origin × Scope matrix backed by ~/.lazyagent/library.
	placePicker *placePicker

	// syncing is non-nil while the bulk-sync overlay is open (key `S`).
	// Replaces the headless `lazyagent library sync` for interactive
	// users; same planner / executor underneath. PRI-64.
	syncing *syncOverlay

	// fixing is non-nil while the bulk-fix overlay is open (key `F`).
	// Mirrors `syncing` but iterates over actions.Fix instead of
	// actions.SyncAll: every item with `ParseError != ""` is enrolled,
	// each gets a precomputed FixPlan or an unfixable reason. PRI-73.
	fixing *fixOverlay

	// resyncPicker is non-nil while the drift-resolution overlay is
	// open. Single keypress (c/t/esc) decides which side wins.
	resyncPicker *resyncPicker

	// creating is non-nil while the "create new item" overlay is open.
	// Keystrokes are routed to updateCreate; the rest of the UI is
	// suspended.
	creating *createOverlay

	// editing is non-nil while the built-in textarea editor is open
	// over an item. Keystrokes route to updateEditor (or the conflict
	// branch when editing.conflict is true). The tree+detail view is
	// hidden until the user saves or cancels.
	editing *editorState

	// Pending write action awaiting [y/n] confirmation. nil means no
	// pending action; the overlay is hidden.
	pending *pendingOp

	// Toast surfaced under the status bar after a write finishes (success
	// or error). Cleared on the next tick / next user keystroke.
	toast      string
	toastUntil time.Time

	width, height int
	err           error
	loading       bool

	// glamourCache memoizes markdown-rendered bodies. Key is "path|width" so
	// the cache is invalidated automatically on terminal resize. Maps are
	// reference types, so mutations persist across the value-typed Model
	// copies bubbletea passes through Update.
	glamourCache map[string][]string

	// sessionBodyCache memoizes the raw markdown transcript (pre-glamour)
	// for KindSession items. Keyed by Item.Path. Built on first detail
	// access and dropped on reload (`r`) — long sessions are too
	// expensive to parse every render cycle. Active sessions accumulate
	// new turns on disk, so the cache will go stale until the user
	// reloads; this is documented so the trade-off is explicit.
	sessionBodyCache map[string]string

	// hidePrivateSessions hides the Private subgroup under
	// KindSession entirely when true (orchestrator / tmp / tool-internal
	// sessions disappear from the tree). Toggled by H, persisted in
	// ~/.lazyagent/state.json so the preference survives across runs.
	hidePrivateSessions bool

	// showAgentSessions includes Task-tool subagent transcripts in
	// the Sessions tree. Default false (hidden) — these spawn-chats
	// are not user-resumable and tend to outnumber real chats by an
	// order of magnitude. Toggled by G, persisted alongside
	// HidePrivateSessions. PRI-70.
	showAgentSessions bool

	// installing drives the `i` GitHub-install wizard (PRI-3.D). All
	// modal phases — URL input, fetch, candidate checklist, target
	// chooser, conflict prompts and the final summary — share state
	// in this struct.
	installing *installOverlay

	// usaging drives the `u` usage overlay (PRI-63). Read-only summary
	// of per-session cost aggregated across origins / models /
	// projects. Tab cycles the time window.
	usaging *usageOverlay

	// budgeting drives the `b` context-budget overlay (PRI-66).
	// Estimates passive token cost of installed Skills / Agents /
	// Memory / Prompts / MCP and rolls them up by Origin × Kind ×
	// Scope. Tab cycles the reference window (Claude / Codex / Gemini).
	budgeting *budgetOverlay

	// forming is the structured form-mode editor for StorageEntry
	// items (PRI-75). Set when E is pressed on an entry whose
	// schema matches; falls back to the JSON-textarea editor when
	// no schema exists or the entry's shape is non-standard. ctrl+m
	// toggles list/map presentation (lines vs fields), ctrl+s saves.
	forming *formOverlay

	// restoreOverlay drives the `Z` undo / restore overlay over
	// internal/backup snapshots (PRI-93). List view → detail view →
	// per-item restore with confirm-overwrite when the target path is
	// occupied.
	restoreOverlay *restoreOverlay

	// PRI-19: update banner. updateAvailable carries the version the
	// background goroutine fetched from GitHub when it is strictly
	// newer than the running build; updateURL is the release page;
	// updateBannerOff is set after the user dismisses it for the day.
	// installSource (brew / go-install / unknown) is plumbed in by
	// main.go so the banner can suggest the right upgrade command.
	updateAvailable string
	updateURL       string
	updateBannerOff bool
	installSource   string

	// PRI-4: "all local" mode. discoveredProjects is the full list of
	// project roots the global indexer found across the user's home
	// directory; allLocal toggles whether their items are folded into
	// the tree under each Origin's Local section. Items from foreign
	// projects carry Item.Meta["project"] = projectDir so the renderer
	// can suffix them with the project name.
	//
	// PRI-56: allLocalModeB switches the Local section from a flat list
	// (Mode A — the original) to a per-project subgroup tree (Mode B —
	// each project becomes its own collapsible node under Local). Only
	// meaningful when allLocal is true; toggled via Shift+B.
	discoveredProjects []string
	allLocal           bool
	allLocalModeB      bool
}

func New(srcs []sources.Source, projectDir string) Model {
	st, _ := state.Load()
	return Model{
		srcs:                srcs,
		projectDir:          projectDir,
		expanded:            defaultExpanded(),
		loading:             true,
		glamourCache:        map[string][]string{},
		sessionBodyCache:    map[string]string{},
		hidePrivateSessions: st.HidePrivateSessions,
		showAgentSessions:   st.ShowAgentSessions,
	}
}

// SetInstallSource records how the binary was installed ("brew",
// "go-install", "unknown"). main.go calls this once before tea.Run so
// the update banner can suggest the right upgrade command. Stored on
// Model rather than passed through New so additional flags do not turn
// the constructor into a kitchen sink.
func (m *Model) SetInstallSource(s string) { m.installSource = s }

// SetDiscoveredProjects seeds the global index lookup (PRI-4). The
// list is the absolute paths the walker found that contain at least
// one tool marker. The cwd-project — if any — is filtered out of this
// list before display so we never duplicate it. allLocal flips the
// initial mode; users toggle at runtime with `A`.
func (m *Model) SetDiscoveredProjects(projects []string, allLocal bool) {
	out := make([]string, 0, len(projects))
	for _, p := range projects {
		if p != "" && p != m.projectDir {
			out = append(out, p)
		}
	}
	m.discoveredProjects = out
	m.allLocal = allLocal
}

func defaultExpanded() map[string]bool {
	return map[string]bool{
		"Claude":         true,
		"Claude/Skills":  true,
		"Claude/Agents":  true,
		"Claude/MCP":     true,
		"Claude/Prompts": true,
		"Claude/Memory":  true,
		"Claude/Hooks":   true,
		"Codex":          true,
		"Codex/Skills":   true,
		"Codex/Agents":   true,
		"Codex/MCP":      true,
		"Codex/Prompts":  true,
		"Codex/Memory":   true,
		"Codex/Hooks":    true,
		"Gemini":         true,
		"Gemini/Skills":  true,
		"Gemini/Agents":  true,
		"Gemini/MCP":     true,
		"Gemini/Prompts": true,
		"Gemini/Memory":  true,
		"Gemini/Hooks":   true,
		"Shared":         true,
		"Shared/Skills":  true,
		"Shared/Agents":  true,
		"Shared/MCP":     true,
		"Shared/Prompts": true,
		"Shared/Memory":  true,
	}
}

type itemsLoadedMsg struct {
	items []model.Item
	err   error
}

// externalEditDoneMsg arrives after $EDITOR (spawned via tea.ExecProcess
// for the "e" key) exits. We capture the path so the glamour cache can be
// invalidated for it; the caller follows up with a reload.
type externalEditDoneMsg struct {
	path string
	err  error
}

// externalEntryEditDoneMsg arrives after $EDITOR exits for an
// entry-fragment edit (PRI-76). The TUI then parses the temp file,
// commits the change via parse.WriteEntry, and removes the temp.
// Carrying the item by value keeps the closure in `e`-handler small
// and lets the message handler do the post-edit work without a
// global reference.
type externalEntryEditDoneMsg struct {
	item     model.Item
	tempPath string
	cleanup  func()
	err      error
}

// resumeDoneMsg arrives after the upstream CLI (claude / gemini / codex)
// spawned via tea.ExecProcess for `R` on a KindSession item exits. The
// session's transcript almost always grew during the resume, so we
// drop the cached body for it and trigger a full reload.
type resumeDoneMsg struct {
	path string
	err  error
}

// UpdateAvailableMsg is sent by the background update-check goroutine
// in main.go when GitHub reports a newer release than the running
// build. Exported so cmd/lazyagent can build it without re-implementing
// the type.
type UpdateAvailableMsg struct {
	Version string
	URL     string
}

// ProjectsDiscoveredMsg is sent by the global indexer in main.go after
// a fresh walk of the configured search roots completes. The list
// replaces whatever was preloaded from the cache; if `A` is currently
// on we trigger a reload so newly-discovered projects show up.
type ProjectsDiscoveredMsg struct {
	Projects []string
}

func (m Model) Init() tea.Cmd {
	return m.loadCmd()
}

func (m Model) loadCmd() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		var all []model.Item
		for _, s := range m.srcs {
			items, err := s.List(ctx, m.projectDir)
			if err != nil {
				return itemsLoadedMsg{err: fmt.Errorf("%s: %w", s.Name(), err)}
			}
			all = append(all, items...)
		}
		// PRI-4: when allLocal is on, fan out each adapter across the
		// projects discovered by the global indexer and fold the
		// resulting Local-scope items into the same flat slice. We tag
		// them with Item.Meta["project"] so the renderer can suffix
		// them with the project directory name; non-Local items
		// returned by these calls (Global skills, sessions, etc.) are
		// already covered by the cwd pass and would only create dupes,
		// so we drop them.
		if m.allLocal {
			for _, projectDir := range m.discoveredProjects {
				for _, s := range m.srcs {
					items, err := s.List(ctx, projectDir)
					if err != nil {
						continue
					}
					for _, it := range items {
						if it.Scope != model.ScopeLocal {
							continue
						}
						if it.Meta == nil {
							it.Meta = map[string]string{}
						}
						if _, ok := it.Meta["project"]; !ok {
							it.Meta["project"] = projectDir
						}
						all = append(all, it)
					}
				}
			}
		}
		// Post-pass: classify items against the shared store. Adapters
		// already mark symlink projections via ResolvesToStore, but
		// copy-mode projections (cloud-sync volumes) are regular files
		// that don't resolve into the store — we have to find them by
		// (kind, name) lookup. Same pass detects drift for both modes.
		groups, _ := store.ListItems()
		nameIndex := map[model.Kind]map[string]string{}
		for k, entries := range groups {
			nameIndex[k] = map[string]string{}
			for _, e := range entries {
				nameIndex[k][e.Manifest.Name] = e.Dir
			}
		}
		for i := range all {
			it := &all[i]
			canonical, ok := nameIndex[it.Kind][it.Name]
			if ok && it.Origin != model.OriginShared {
				it.Shared = true
			}
			if !it.Shared || it.Origin == model.OriginShared {
				continue
			}
			// Symlink projections set Shared via path-based resolution
			// in the adapter; copy-mode projections went through the
			// name index above. Either way canonical now points at the
			// store dir we want to diff against.
			if canonical == "" {
				canonical = store.CanonicalItemDir(it.Path)
			}
			// Lossy projections (codex profile entry, gemini TOML
			// command) generate target bytes from the canonical .md
			// rather than mirroring it; byte-for-byte body compare
			// would always flag them as drifted. PRI-72 routes these
			// through a regenerate-and-compare detector instead.
			if actions.IsLossyProjection(it.Kind, it.Origin) {
				it.Drift = actions.LossyProjectionDrift(*it, canonical, m.projectDir)
				continue
			}
			it.Drift = store.IsDriftedAgainst(*it, canonical)
		}
		// Resolve cwd for every session and flag the ones whose project
		// directory was deleted. The flag drives a dim "(cwd gone)"
		// badge in the tree and a targeted resume error — without it,
		// users can't tell a resumable session from an archived one
		// until they actually press R.
		actions.EnrichSessionCwds(all)
		return itemsLoadedMsg{items: all}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Resize the editor textarea so it tracks the terminal.
		// Other overlays size themselves at render time.
		if m.editing != nil {
			m.editing.resize(m.width, m.height)
		}
		return m, nil

	case itemsLoadedMsg:
		m.loading = false
		m.err = msg.err
		m.items = msg.items
		m.rebuildTree()
		return m, nil

	case UpdateAvailableMsg:
		// PRI-19: background poll found a newer release. We re-check
		// the dismissal-for-today predicate here (not just in main.go)
		// because the user may have hit a key between fetch and arrival
		// — a tap during the fetch shouldn't surface the banner.
		st, _ := state.Load()
		now := time.Now()
		if !isUpdateBannerDismissed(st, msg.Version, now) {
			m.updateAvailable = msg.Version
			m.updateURL = msg.URL
			m.updateBannerOff = false
		}
		return m, nil

	case ProjectsDiscoveredMsg:
		// PRI-4: refreshed project index landed. Filter out the cwd
		// project so we don't double-count it. If all-local mode is
		// already on, kick a reload so the freshly-discovered roots
		// show up in the tree.
		filtered := make([]string, 0, len(msg.Projects))
		for _, p := range msg.Projects {
			if p != "" && p != m.projectDir {
				filtered = append(filtered, p)
			}
		}
		m.discoveredProjects = filtered
		if m.allLocal {
			m.loading = true
			return m, m.loadCmd()
		}
		return m, nil

	case externalEditDoneMsg:
		// $EDITOR exited. Drop any cached glamour rendering for this path
		// (across widths) and reload sources so frontmatter changes
		// propagate into the description column.
		for k := range m.glamourCache {
			if strings.HasPrefix(k, msg.path+"|") {
				delete(m.glamourCache, k)
			}
		}
		if msg.err != nil {
			m.setToast("editor: " + msg.err.Error())
		}
		m.loading = true
		return m, m.loadCmd()

	case externalEntryEditDoneMsg:
		// PRI-76: $EDITOR closed on a temp JSON fragment. Commit the
		// change back into the underlying config; on JSON parse
		// failure leave the on-disk entry untouched and surface a
		// toast so the user knows nothing was saved.
		defer msg.cleanup()
		if msg.err != nil {
			m.setToast("editor: " + msg.err.Error())
			return m, nil
		}
		if err := actions.CommitEntryEdit(msg.item, msg.tempPath); err != nil {
			m.setToast("edit aborted: " + err.Error())
			return m, nil
		}
		// Same cache-invalidation pattern as externalEditDoneMsg —
		// the underlying file changed even though we wrote it from
		// a different path.
		for k := range m.glamourCache {
			if strings.HasPrefix(k, msg.item.Path+"|") {
				delete(m.glamourCache, k)
			}
		}
		m.setToast("entry saved")
		m.loading = true
		return m, m.loadCmd()

	case installFetchedMsg:
		if m.installing == nil {
			return m, nil
		}
		if msg.err != nil {
			m.installing.err = msg.err.Error()
			m.installing.summary = nil
			m.installing.phase = phaseInstallDone
			return m, nil
		}
		m.installing.spec = msg.spec
		m.installing.sha = msg.sha
		m.installing.cacheDir = msg.cacheDir
		m.installing.candidates = msg.candidates
		m.installing.selected = make([]bool, len(msg.candidates))
		// Default-select everything to match the typical "install all"
		// case; users can `space` off the ones they don't want.
		for i := range m.installing.selected {
			m.installing.selected[i] = true
		}
		m.installing.cursor = 0
		m.installing.phase = phaseInstallPick
		return m, nil

	case installAppliedMsg:
		if m.installing == nil {
			return m, nil
		}
		m.installing.summary = msg.summary
		if msg.err != nil {
			m.installing.err = msg.err.Error()
		}
		m.installing.phase = phaseInstallDone
		return m, nil

	case resumeDoneMsg:
		// Upstream CLI exited. The session's transcript and lastUpdated
		// almost certainly changed; nuke both caches and reload.
		delete(m.sessionBodyCache, msg.path)
		for k := range m.glamourCache {
			if strings.HasPrefix(k, msg.path+"|") {
				delete(m.glamourCache, k)
			}
		}
		if msg.err != nil {
			m.setToast("resume: " + msg.err.Error())
		}
		m.loading = true
		return m, m.loadCmd()

	case tea.KeyMsg:
		// PRI-19: any key suppresses the update banner for the rest of
		// the current calendar day. The dismissal also persists, so a
		// user who hits "j" once doesn't keep seeing it on every relaunch.
		// We swallow the keypress only for the dismissal side effect —
		// the rest of the handler still processes it, so a single tap
		// both hides the banner AND moves the cursor.
		if m.updateAvailable != "" && !m.updateBannerOff {
			m.updateBannerOff = true
			if st, err := state.Load(); err == nil {
				st.UpdateBannerDismissedFor = m.updateAvailable
				st.UpdateBannerDismissedDate = time.Now().Format("2006-01-02")
				_ = state.Save(st)
			}
		}

		// Help overlay swallows everything: any key closes it.
		if m.helpOpen {
			m.helpOpen = false
			return m, nil
		}

		// Confirmation overlay: y/Y commits, n/N/esc cancels, anything
		// else is ignored to avoid accidents.
		if m.pending != nil {
			return m.updateConfirm(msg)
		}

		// Place picker: unified Origin × Scope matrix overlay (`p`).
		// Replaces the legacy copy/move/cross/share entry points.
		if m.placePicker != nil {
			return m.updatePlacePicker(msg)
		}

		// Restore overlay (`Z`): browse and restore backup snapshots
		// from internal/backup. PRI-93.
		if m.restoreOverlay != nil {
			return m.updateRestoreOverlay(msg)
		}

		// Sync-all overlay (`S`): full plan preview + apply. PRI-64.
		if m.syncing != nil {
			return m.updateSyncOverlay(msg)
		}

		// Bulk-fix overlay (`F`): list of every invalid item with its
		// computed FixPlan; y applies the fixable subset. PRI-73.
		if m.fixing != nil {
			return m.updateFixOverlay(msg)
		}

		// Usage overlay (`u`): read-only cost summary. PRI-63.
		if m.usaging != nil {
			return m.updateUsageOverlay(msg)
		}

		// Budget overlay (`b`): read-only passive-context summary. PRI-66.
		if m.budgeting != nil {
			return m.updateBudgetOverlay(msg)
		}

		// Form-mode editor (`E` on a schema'd entry). PRI-75.
		if m.forming != nil {
			return m.updateForm(msg)
		}

		// Resync picker: c/t/esc.
		if m.resyncPicker != nil {
			return m.updateResyncPicker(msg)
		}

		// Install-from-GitHub wizard: full-screen modal across several
		// phases (URL input → fetch → pick → target → conflicts → done).
		if m.installing != nil {
			next, cmd := m.updateInstall(msg)
			return next, cmd
		}

		// Create-new overlay: typed-text input, esc cancels, enter creates.
		if m.creating != nil {
			return m.updateCreate(msg)
		}

		// Built-in editor: full-screen textarea + save/cancel/conflict.
		if m.editing != nil {
			return m.updateEditor(msg)
		}

		// Filter editor mode: route keystrokes into filterText.
		if m.filterMode {
			return m.updateFilter(msg)
		}

		// Fullscreen detail: route everything to updateDetail (which
		// handles its own scroll, escape and quit semantics) so the
		// reader can't accidentally trigger destructive actions.
		if m.detailFull {
			return m.updateDetail(msg)
		}

		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			return m, tea.Quit
		case "tab", "enter":
			// Single intuitive "drill in" key. On a leaf → fullscreen
			// detail; on a group → toggle expand. Same behavior from
			// both tree mode and (via updateFilter) filter mode.
			return m.activate(), nil
		case "r":
			m.loading = true
			for k := range m.glamourCache {
				delete(m.glamourCache, k)
			}
			for k := range m.sessionBodyCache {
				delete(m.sessionBodyCache, k)
			}
			return m, m.loadCmd()
		case "A":
			// PRI-4: toggle "all local" projects mode. When on, items
			// from every discovered project root are folded into the
			// tree under their tool's Local section. We trigger a full
			// reload so the underlying source slice gets re-fanned.
			m.allLocal = !m.allLocal
			m.loading = true
			for k := range m.glamourCache {
				delete(m.glamourCache, k)
			}
			if m.allLocal {
				m.setToast(fmt.Sprintf("all-local: scanning %d projects", len(m.discoveredProjects)))
			} else {
				m.setToast("all-local: off")
			}
			return m, m.loadCmd()
		case "B":
			// PRI-56: toggle Mode B (per-project subgroups under Local).
			// Only useful when all-local mode is active; ignored otherwise
			// so users don't get a silently mutating state.
			if !m.allLocal {
				m.setToast("all-local must be on (A) for Mode B")
				return m, nil
			}
			m.allLocalModeB = !m.allLocalModeB
			m.rebuildTree()
			if m.allLocalModeB {
				m.setToast("all-local: Mode B (per-project)")
			} else {
				m.setToast("all-local: Mode A (flat)")
			}
			return m, nil
		case "/":
			m.filterMode = true
			m.focus = focusTree
			return m, nil
		case "?":
			m.helpOpen = true
			return m, nil
		case "d":
			if it, ok := m.currentItem(); ok {
				m.pending = &pendingOp{kind: pendDelete, item: it}
			}
			return m, nil
		case "f":
			// PRI-73: deterministic auto-fix for items the validators
			// flagged as `(invalid)`. We compute the plan up-front so the
			// confirm overlay can preview the rewritten bytes; the user
			// still has to say `y` before anything is written.
			it, ok := m.currentItem()
			if !ok {
				return m, nil
			}
			if it.ParseError == "" {
				m.setToast("fix: nothing to fix — item is already valid")
				return m, nil
			}
			plan, err := actions.Fix(it)
			if err != nil {
				m.setToast("fix: " + err.Error())
				return m, nil
			}
			m.pending = &pendingOp{kind: pendFix, item: it, fix: plan}
			return m, nil
		case "F":
			// PRI-73 Phase B: bulk fix overlay across every invalid item
			// in the current items slice. Same pattern as `S` (sync-all):
			// pre-flight every plan, render counts + scrollable list,
			// `y` applies the fixable subset, post-apply shows a
			// summary the user closes with esc.
			ov := newFixOverlay(m.items)
			if ov == nil {
				m.setToast("fix-all: no invalid items")
				return m, nil
			}
			m.fixing = ov
			return m, nil
		case "p":
			it, ok := m.currentItem()
			if !ok {
				return m, nil
			}
			p, err := newPlacePicker(it, m.projectDir)
			if err != nil {
				m.setToast(err.Error())
				return m, nil
			}
			m.placePicker = p
			return m, nil
		case "Z":
			// PRI-93: undo / restore overlay. Lists snapshots written
			// by internal/backup before destructive ops; per-item or
			// per-snapshot restore. Read-only until `r` / `R` inside
			// the detail view.
			m.restoreOverlay = newRestoreOverlay()
			return m, nil
		case "S":
			// Bulk sync: project every shareable global item to every
			// supported tool, importing into the library where needed.
			// PRI-64: TUI surface for the headless `library sync`.
			plan := actions.SyncAll(m.items)
			if len(plan.Ops) == 0 {
				m.setToast("sync: no items to consider")
				return m, nil
			}
			m.syncing = newSyncOverlay(plan)
			return m, nil
		case "R":
			it, ok := m.currentItem()
			if !ok {
				return m, nil
			}
			// Sessions: resume the conversation in the upstream CLI
			// (claude / gemini / codex). Other kinds: resync drifted
			// shared projections back to canonical. Same key, different
			// semantics per Kind — both are "Restart" gestures.
			if it.Kind == model.KindSession {
				ctx := actions.ResumeContext{
					ProjectDir:   m.projectDir,
					KnownHashCwd: actions.BuildHashCwdIndex(m.items),
				}
				cmd, err := actions.ResumeCommand(it, ctx)
				if err != nil {
					m.setToast(err.Error())
					return m, nil
				}
				path := it.Path
				return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
					return resumeDoneMsg{path: path, err: err}
				})
			}
			if !it.Drift {
				m.setToast("not drifted — nothing to resync")
				return m, nil
			}
			m.resyncPicker = newResyncPicker(it)
			return m, nil
		case "H":
			// Toggle visibility of the Private subgroup under Sessions
			// (orchestrator / tmp / tool-internal). Persists in
			// ~/.lazyagent/state.json so the preference survives across
			// runs. Tree rebuilds immediately so the user sees the
			// effect without a manual reload.
			m.hidePrivateSessions = !m.hidePrivateSessions
			if st, err := state.Load(); err == nil {
				st.HidePrivateSessions = m.hidePrivateSessions
				if err := state.Save(st); err != nil {
					m.setToast("save state: " + err.Error())
				}
			}
			if m.hidePrivateSessions {
				m.setToast("private sessions hidden")
			} else {
				m.setToast("private sessions visible")
			}
			m.rebuildTree()
			return m, nil
		case "G":
			// Toggle visibility of subagent (Task-tool spawn) sessions.
			// Default-off so the Sessions tree shows resumable chats
			// only; turn it on for debugging which Task-call did what.
			// Persists alongside HidePrivateSessions. PRI-70.
			m.showAgentSessions = !m.showAgentSessions
			if st, err := state.Load(); err == nil {
				st.ShowAgentSessions = m.showAgentSessions
				if err := state.Save(st); err != nil {
					m.setToast("save state: " + err.Error())
				}
			}
			if m.showAgentSessions {
				m.setToast("agent sessions visible")
			} else {
				m.setToast("agent sessions hidden")
			}
			m.rebuildTree()
			return m, nil
		case "T":
			// Open resume in a new terminal tab (iTerm2 / Apple Terminal).
			// Only meaningful on Sessions; we keep the TUI running in
			// place. For non-sessions there's nothing useful here.
			it, ok := m.currentItem()
			if !ok {
				return m, nil
			}
			if it.Kind != model.KindSession {
				m.setToast("T only works on a Session")
				return m, nil
			}
			ctx := actions.ResumeContext{
				ProjectDir:   m.projectDir,
				KnownHashCwd: actions.BuildHashCwdIndex(m.items),
			}
			cmd, err := actions.ResumeNewTabCommand(it, ctx)
			if err != nil {
				m.setToast(err.Error())
				return m, nil
			}
			if err := cmd.Run(); err != nil {
				m.setToast("new tab: " + err.Error())
				return m, nil
			}
			m.setToast("resume opened in new tab")
			return m, nil
		case "e":
			it, ok := m.currentItem()
			if !ok {
				return m, nil
			}
			// PRI-76: StorageEntry items get an entry-only temp file —
			// $EDITOR opens a JSON fragment matching the detail panel,
			// not the whole settings.json / .claude.json / config.toml.
			if it.Storage == model.StorageEntry {
				tempPath, cleanup, err := actions.PrepareEntryEdit(it)
				if err != nil {
					m.setToast("editor: " + err.Error())
					return m, nil
				}
				cmd, err := actions.EditorCommand(tempPath)
				if err != nil {
					cleanup()
					m.setToast("editor: " + err.Error())
					return m, nil
				}
				return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
					return externalEntryEditDoneMsg{
						item:     it,
						tempPath: tempPath,
						cleanup:  cleanup,
						err:      err,
					}
				})
			}
			cmd, err := actions.EditorCommand(it.Path)
			if err != nil {
				m.setToast("editor: " + err.Error())
				return m, nil
			}
			path := it.Path
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return externalEditDoneMsg{path: path, err: err}
			})
		case "n":
			ov, err := m.newCreateOverlay()
			if err != nil {
				m.setToast(err.Error())
				return m, nil
			}
			m.creating = ov
			return m, nil
		case "i":
			m.installing = newInstallOverlay()
			return m, nil
		case "u":
			m.usaging = newUsageOverlay()
			return m, nil
		case "b":
			m.budgeting = newBudgetOverlay()
			return m, nil
		case "U":
			it, ok := m.currentItem()
			if !ok {
				return m, nil
			}
			cmd, err := startUpdateForItem(it)
			if err != nil {
				m.setToast("update: " + err.Error())
				return m, nil
			}
			m.installing = newUpdateOverlay(it)
			return m, cmd
		case "E":
			it, ok := m.currentItem()
			if !ok {
				return m, nil
			}
			// StorageEntry items (Hook / MCP / codex profile) open in
			// entry mode. PRI-75: try the form-mode editor first when
			// a schema matches the entry shape; fall back to the JSON
			// textarea editor when shape is non-standard or the kind
			// has no schema.
			if it.Storage == model.StorageEntry {
				if f, ok := newFormOverlay(it); ok {
					m.forming = f
					return m, nil
				}
			}
			ed, err := newEditorState(it)
			if err != nil {
				m.setToast("editor: " + err.Error())
				return m, nil
			}
			ed.resize(m.width, m.height)
			m.editing = ed
			return m, nil
		case "esc":
			if m.filterText != "" {
				m.filterText = ""
				m.cursor = 0
				m.detailScroll = 0
				m.rebuildTree()
				return m, nil
			}
		}

		if m.focus == focusTree {
			return m.updateTree(msg)
		}
		return m.updateDetail(msg)
	}
	return m, nil
}

// newCreateOverlay derives (Origin, Kind, Scope) from the cursor's
// position in the tree and returns a populated overlay ready to accept
// a typed name. Returns an error message (suitable for a toast) when
// the cursor isn't on a recognisable Origin/Kind/Scope path — the user
// can navigate down to a Skills/Agents/Prompts scope and try again.
func (m Model) newCreateOverlay() (*createOverlay, error) {
	if m.cursor < 0 || m.cursor >= len(m.tree) {
		return nil, fmt.Errorf("nothing selected — navigate to a Skills/Agents/Prompts scope")
	}

	// Easy case: cursor on a leaf — use its item's Origin/Kind/Scope.
	if it, ok := m.currentItem(); ok {
		return &createOverlay{origin: it.Origin, kind: it.Kind, scope: it.Scope}, nil
	}

	// Group node: parse "Origin/Kind[/Scope]" out of the label. Only
	// scope-level nodes (depth 2, three segments) carry full context.
	parts := strings.Split(m.tree[m.cursor].label, "/")
	if len(parts) != 3 {
		return nil, fmt.Errorf("navigate to a Skills/Agents/Prompts scope (Global or Local) and press n")
	}
	o, ok1 := model.ParseOrigin(parts[0])
	k, ok2 := model.ParseKind(parts[1])
	s, ok3 := model.ParseScope(parts[2])
	if !ok1 || !ok2 || !ok3 {
		return nil, fmt.Errorf("can't parse %q as Origin/Kind/Scope", m.tree[m.cursor].label)
	}
	return &createOverlay{origin: o, kind: k, scope: s}, nil
}

// updateCreate handles keystrokes while the create overlay is open.
// Enter commits, esc cancels, backspace edits, anything else of length 1
// is treated as a typed character.
func (m Model) updateCreate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.creating = nil
		return m, nil
	case "enter":
		ov := m.creating
		path, err := actions.Create(ov.origin, ov.kind, ov.scope, ov.name, m.projectDir)
		if err != nil {
			ov.err = err.Error()
			return m, nil
		}
		m.creating = nil
		m.setToast("created " + path)
		// Open the freshly-created file in $EDITOR. The post-edit
		// callback already wipes the glamour cache and reloads sources,
		// so the new item appears in the tree by the time we return.
		cmd, edErr := actions.EditorCommand(path)
		if edErr != nil {
			// Editor missing — surface that in the toast and reload so
			// the new file at least shows up in the tree.
			m.setToast("created (editor unavailable: " + edErr.Error() + ")")
			m.loading = true
			return m, m.loadCmd()
		}
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
			return externalEditDoneMsg{path: path, err: err}
		})
	case "backspace":
		if len(m.creating.name) > 0 {
			m.creating.name = m.creating.name[:len(m.creating.name)-1]
			m.creating.err = ""
		}
		return m, nil
	case "ctrl+u":
		m.creating.name = ""
		m.creating.err = ""
		return m, nil
	}
	s := msg.String()
	switch {
	case s == "space":
		m.creating.name += " "
		m.creating.err = ""
	case len(s) == 1:
		m.creating.name += s
		m.creating.err = ""
	}
	return m, nil
}

// updateEditor handles keystrokes while the built-in textarea editor
// is open. ctrl+s saves with mtime conflict detection; esc cancels
// (changes are lost — the v1 contract is "you didn't save, you didn't
// keep"); ctrl+r reloads the file from disk discarding local edits.
// When the conflict flag is set the input switches to a small
// resolution menu (overwrite / reload / keep editing).
func (m Model) updateEditor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ed := m.editing

	if ed.conflict {
		switch msg.String() {
		case "o":
			// Overwrite — re-save with no mtime check.
			if err := actions.SaveFile(ed.path, []byte(ed.ta.Value()), time.Time{}); err != nil {
				m.setToast("save: " + err.Error())
				return m, nil
			}
			path := ed.path
			m.editing = nil
			m.setToast("saved (overwrote on-disk version)")
			m.invalidateBodyCache(path)
			m.loading = true
			return m, m.loadCmd()
		case "r":
			data, err := os.ReadFile(ed.path)
			if err != nil {
				m.setToast("reload: " + err.Error())
				return m, nil
			}
			ed.ta.SetValue(string(data))
			ed.initial = string(data)
			if mt, err := actions.FileMtime(ed.path); err == nil {
				ed.openMT = mt
			}
			ed.conflict = false
			return m, nil
		case "esc":
			ed.conflict = false
			return m, nil
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+s":
		if ed.entryMode {
			if err := ed.saveEntry(); err != nil {
				m.setToast("save: " + err.Error())
				return m, nil
			}
			path := ed.path
			m.editing = nil
			m.setToast("saved " + ed.entryKey)
			m.invalidateBodyCache(path)
			m.loading = true
			return m, m.loadCmd()
		}
		err := actions.SaveFile(ed.path, []byte(ed.ta.Value()), ed.openMT)
		if errors.Is(err, actions.ErrConflict) {
			ed.conflict = true
			return m, nil
		}
		if err != nil {
			m.setToast("save: " + err.Error())
			return m, nil
		}
		path := ed.path
		m.editing = nil
		m.setToast("saved " + path)
		m.invalidateBodyCache(path)
		m.loading = true
		return m, m.loadCmd()
	case "esc":
		m.editing = nil
		return m, nil
	case "ctrl+r":
		data, err := os.ReadFile(ed.path)
		if err != nil {
			m.setToast("reload: " + err.Error())
			return m, nil
		}
		ed.ta.SetValue(string(data))
		ed.initial = string(data)
		if mt, err := actions.FileMtime(ed.path); err == nil {
			ed.openMT = mt
		}
		return m, nil
	}

	var cmd tea.Cmd
	ed.ta, cmd = ed.ta.Update(msg)
	return m, cmd
}

// invalidateBodyCache drops every glamour render cached for path
// (across all widths). Called after any operation that might have
// changed the on-disk content of a single item.
func (m *Model) invalidateBodyCache(path string) {
	prefix := path + "|"
	for k := range m.glamourCache {
		if strings.HasPrefix(k, prefix) {
			delete(m.glamourCache, k)
		}
	}
}

// createOverlayText renders the body of the create-new overlay.
func createOverlayText(ov createOverlay) string {
	var lines []string
	lines = append(lines, titleStyle.Render("Create new "+ov.kind.String()))
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(fmt.Sprintf("  %s · %s · %s",
		ov.origin, ov.kind, ov.scope)))
	lines = append(lines, "")
	caret := "_"
	lines = append(lines, "  name: "+ov.name+caret)
	if ov.err != "" {
		lines = append(lines, "")
		lines = append(lines, dimStyle.Render("  "+ov.err))
	}
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(
		"  enter create · esc cancel · ctrl+u clear"))
	return strings.Join(lines, "\n")
}

// activate is the unified "drill in" action for Tab/Enter from any mode.
// On a group it toggles expand. On a leaf it opens the fullscreen detail
// view so the reader can scroll the body.
func (m Model) activate() Model {
	if m.cursor < 0 || m.cursor >= len(m.tree) {
		return m
	}
	n := m.tree[m.cursor]
	if n.isGroup {
		m.expanded[n.label] = !m.expanded[n.label]
		m.rebuildTree()
		return m
	}
	if n.isEmpty {
		// PRI-20 placeholder. Inert — no item to drill into.
		return m
	}
	m.detailFull = true
	m.detailScroll = 0
	return m
}

// currentItem returns the Item under the cursor, or false if the cursor
// is on a group / out of range.
func (m Model) currentItem() (model.Item, bool) {
	if m.cursor < 0 || m.cursor >= len(m.tree) {
		return model.Item{}, false
	}
	n := m.tree[m.cursor]
	if n.isGroup || n.itemIdx < 0 || n.itemIdx >= len(m.items) {
		return model.Item{}, false
	}
	return m.items[n.itemIdx], true
}

// updateConfirm processes y/n while a write action is pending.
func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y":
		op := *m.pending
		m.pending = nil
		var err error
		switch op.kind {
		case pendDelete:
			err = actions.Delete(op.item)
		case pendFix:
			err = actions.ApplyFix(op.fix)
		}
		if err != nil {
			m.setToast(formatActionError(op, err))
			return m, nil
		}
		m.setToast(fmt.Sprintf("%s ok: %s", op.verb(), op.item.Name))
		// Clear glamour cache for the affected path so re-rendering
		// after reload reflects new content.
		for k := range m.glamourCache {
			if strings.HasPrefix(k, op.item.Path+"|") {
				delete(m.glamourCache, k)
			}
		}
		m.loading = true
		return m, m.loadCmd()
	case "n", "esc":
		m.pending = nil
		return m, nil
	}
	return m, nil
}

func formatActionError(op pendingOp, err error) string {
	switch {
	case errors.Is(err, actions.ErrUnsupported):
		return fmt.Sprintf("%s: not supported for %s entries yet", op.verb(), op.item.Kind)
	case errors.Is(err, actions.ErrNoProject):
		return op.verb() + ": no project local scope (cd into a project)"
	case errors.Is(err, actions.ErrTargetExists):
		return op.verb() + ": target already exists"
	default:
		return op.verb() + ": " + err.Error()
	}
}

func (m *Model) setToast(s string) {
	m.toast = s
	m.toastUntil = time.Now().Add(4 * time.Second)
}

// updateFilter handles keystrokes while the filter editor is active.
//
// Live-filter UX: arrow keys and Tab keep navigating the tree without
// leaving the editor (so you can scan matches as you type). Enter exits
// the editor AND triggers the normal tree-mode enter (expand / open
// detail). j/k/h/l intentionally do NOT navigate here — they're letters
// the user might want in the filter text (e.g. searching "javascript").
func (m Model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterMode = false
		m.filterText = ""
		m.cursor = 0
		m.detailScroll = 0
		m.rebuildTree()
		return m, nil
	case "tab", "enter":
		// Drill into the current row directly from filter mode without
		// requiring the user to first exit the editor.
		m.filterMode = false
		return m.activate(), nil
	case "up", "down", "left", "right":
		return m.updateTree(msg)
	case "backspace":
		if len(m.filterText) > 0 {
			r := []rune(m.filterText)
			m.filterText = string(r[:len(r)-1])
			m.cursor = 0
			m.detailScroll = 0
			m.rebuildTree()
		}
		return m, nil
	case "ctrl+u":
		m.filterText = ""
		m.cursor = 0
		m.detailScroll = 0
		m.rebuildTree()
		return m, nil
	}

	if msg.Type == tea.KeyRunes {
		m.filterText += string(msg.Runes)
		m.cursor = 0
		m.detailScroll = 0
		m.rebuildTree()
		return m, nil
	}
	if msg.Type == tea.KeySpace {
		m.filterText += " "
		m.cursor = 0
		m.detailScroll = 0
		m.rebuildTree()
		return m, nil
	}
	return m, nil
}

func (m Model) updateTree(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		if m.cursor < len(m.tree)-1 {
			m.cursor++
			m.detailScroll = 0
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
			m.detailScroll = 0
		}
	case "h", "left":
		if m.cursor < len(m.tree) {
			n := m.tree[m.cursor]
			if n.isGroup {
				m.expanded[n.label] = false
				m.rebuildTree()
			}
		}
	case "l", "right":
		if m.cursor < len(m.tree) {
			n := m.tree[m.cursor]
			if n.isGroup {
				m.expanded[n.label] = !m.expanded[n.label]
				m.rebuildTree()
			} else {
				return m.activate(), nil
			}
		}
	case "t":
		// toggle JSON / TOML in detail (also reachable while focused on tree)
		m.detailFmt = nextFormat(m.detailFmt)
	}
	return m, nil
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.detailScroll++
	case "k", "up":
		if m.detailScroll > 0 {
			m.detailScroll--
		}
	case "pgdown", " ":
		m.detailScroll += 10
	case "pgup":
		m.detailScroll -= 10
		if m.detailScroll < 0 {
			m.detailScroll = 0
		}
	case "g", "home":
		m.detailScroll = 0
	case "G", "end":
		m.detailScroll = 1 << 30 // clamped in renderDetail
	case "t":
		m.detailFmt = nextFormat(m.detailFmt)
	case "tab", "enter", "esc", "h", "left":
		// Tab/Enter both drill in and drill out — pressing the same key
		// that took the user into fullscreen returns them. Esc/h/left
		// also back out for muscle-memory parity with the filter editor.
		if m.detailFull {
			m.detailFull = false
		} else {
			m.focus = focusTree
		}
	case "q":
		// In fullscreen mode, q exits to split view rather than quitting,
		// so a casual reader doesn't lose the app on a single key.
		if m.detailFull {
			m.detailFull = false
			return m, nil
		}
		return m, tea.Quit
	}
	return m, nil
}

func nextFormat(f detailFormat) detailFormat {
	switch f {
	case formatAuto:
		return formatJSON
	case formatJSON:
		return formatTOML
	default:
		return formatAuto
	}
}

// rebuildTree regenerates the visible flat tree from m.items + m.expanded.
func (m *Model) rebuildTree() {
	// Group items: Origin -> Kind -> Scope -> []Item.
	// `private` is only populated for KindSession (sessions started in
	// /tmp / tool config dirs / orchestrator worktrees go into a
	// separate subgroup that's collapsed by default — see
	// parse.IsPrivateSessionCwd).
	type kindBucket struct {
		global  []int
		local   []int
		private []int
	}
	type originBucket struct {
		kinds map[model.Kind]*kindBucket
	}
	buckets := map[model.Origin]*originBucket{}
	originOrder := []model.Origin{model.OriginClaude, model.OriginCodex, model.OriginGemini}
	// OriginShared is data-driven: only render the Shared origin when
	// the lazyagent store has at least one item, so a fresh install
	// doesn't show an empty Shared section above the real tools.
	for _, it := range m.items {
		if it.Origin == model.OriginShared {
			originOrder = append(originOrder, model.OriginShared)
			break
		}
	}
	kindOrder := []model.Kind{model.KindSkill, model.KindAgent, model.KindMCP, model.KindPrompt, model.KindMemory, model.KindHook, model.KindSession}

	for _, o := range originOrder {
		buckets[o] = &originBucket{kinds: map[model.Kind]*kindBucket{}}
		for _, k := range kindOrder {
			buckets[o].kinds[k] = &kindBucket{}
		}
	}
	filter := strings.ToLower(strings.TrimSpace(m.filterText))
	for i, it := range m.items {
		if filter != "" && !itemMatches(it, filter) {
			continue
		}
		// PRI-70: subagent (Task-tool spawn) transcripts are hidden
		// by default — there are typically far more of them than
		// real chats and they are not user-resumable. The G toggle
		// flips this for debugging.
		if it.Agent && !m.showAgentSessions {
			continue
		}
		b := buckets[it.Origin].kinds[it.Kind]
		switch {
		case it.Private:
			b.private = append(b.private, i)
		case it.Scope == model.ScopeGlobal:
			b.global = append(b.global, i)
		default:
			b.local = append(b.local, i)
		}
	}

	// Sessions read top-down by recency, everything else alphabetically
	// by name. The check on the bucket's first element is fine because a
	// single bucket holds exactly one Kind.
	sortItems := func(idxs []int) {
		if len(idxs) == 0 {
			return
		}
		if m.items[idxs[0]].Kind == model.KindSession {
			sort.SliceStable(idxs, func(a, b int) bool {
				return m.items[idxs[a]].Meta["lastUpdated"] > m.items[idxs[b]].Meta["lastUpdated"]
			})
			return
		}
		sort.SliceStable(idxs, func(a, b int) bool {
			return m.items[idxs[a]].Name < m.items[idxs[b]].Name
		})
	}

	var tree []node
	for _, o := range originOrder {
		oLabel := o.String()
		tree = append(tree, node{depth: 0, label: oLabel, isGroup: true, itemIdx: -1, collapsed: !m.expanded[oLabel]})
		if !m.expanded[oLabel] {
			continue
		}
		for _, k := range kindOrder {
			kPath := oLabel + "/" + k.String()
			b := buckets[o].kinds[k]
			privateVisible := !m.hidePrivateSessions || k != model.KindSession
			total := len(b.global) + len(b.local)
			if privateVisible {
				total += len(b.private)
			}
			// Hide empty kind groups when a filter is active so the tree
			// stays scannable. Without a filter, show them at 0 so the
			// user knows the kind exists.
			if filter != "" && total == 0 {
				continue
			}
			label := fmt.Sprintf("%s (%d)", k.String(), total)
			tree = append(tree, node{depth: 1, label: kPath, isGroup: true, itemIdx: -1, collapsed: !m.expanded[kPath]})
			// override displayed label by re-encoding via a sentinel: we keep
			// label = full path for stable expansion keying, but renderTree
			// will trim to the last segment. The (count) goes into Display.
			tree[len(tree)-1].label = kPath
			tree[len(tree)-1].labelOverride(label)

			if !m.expanded[kPath] {
				continue
			}
			// Global section
			if len(b.global) > 0 {
				gPath := kPath + "/Global"
				tree = append(tree, node{depth: 2, label: gPath, isGroup: true, itemIdx: -1, collapsed: !m.expanded[gPath]})
				if m.expanded[gPath] {
					switch k {
					case model.KindSession:
						tree = m.renderSessionLeaves(b.global, gPath, 3, tree)
					case model.KindHook:
						tree = m.renderHookLeaves(b.global, gPath, 3, tree)
					default:
						sortItems(b.global)
						for _, idx := range b.global {
							tree = append(tree, node{depth: 3, label: m.items[idx].Name, itemIdx: idx})
						}
					}
				}
			}
			localVisible := (m.projectDir != "" || m.allLocal) && len(b.local) > 0
			if localVisible {
				lPath := kPath + "/Local"
				tree = append(tree, node{depth: 2, label: lPath, isGroup: true, itemIdx: -1, collapsed: !m.expanded[lPath]})
				if m.expanded[lPath] {
					if k == model.KindSession {
						tree = m.renderSessionLeaves(b.local, lPath, 3, tree)
					} else if k == model.KindHook {
						tree = m.renderHookLeaves(b.local, lPath, 3, tree)
					} else {
						sortItems(b.local)
						if m.allLocal && m.allLocalModeB {
							// Mode B: bucket Local items by their project dir
							// (Item.Meta["project"]) and render one collapsible
							// subgroup per project, with the items underneath.
							byProject := map[string][]int{}
							projectOrder := []string{}
							for _, idx := range b.local {
								pdir := m.items[idx].Meta["project"]
								if pdir == "" {
									pdir = m.projectDir
								}
								if _, seen := byProject[pdir]; !seen {
									projectOrder = append(projectOrder, pdir)
								}
								byProject[pdir] = append(byProject[pdir], idx)
							}
							sort.Strings(projectOrder)
							for _, pdir := range projectOrder {
								pPath := lPath + "/" + pdir
								label := filepath.Base(pdir)
								if pdir == m.projectDir {
									label += " (cwd)"
								}
								tree = append(tree, node{depth: 3, label: pPath, isGroup: true, itemIdx: -1, collapsed: !m.expanded[pPath]})
								tree[len(tree)-1].labelOverride(label)
								if m.expanded[pPath] {
									for _, idx := range byProject[pdir] {
										tree = append(tree, node{depth: 4, label: m.items[idx].Name, itemIdx: idx})
									}
								}
							}
						} else {
							for _, idx := range b.local {
								tree = append(tree, node{depth: 3, label: m.items[idx].Name, itemIdx: idx})
							}
						}
					}
				}
			}
			// PRI-20: per-section empty placeholder. When the Kind group
			// is expanded but has no Global/Local/Private children, drop
			// a dimmed "no <kind> yet" leaf so the user knows the section
			// rendered correctly. itemIdx == -1 keeps it inert — j/k can
			// land on it, but tab/enter does nothing because activate()
			// short-circuits on a non-group node with no item.
			if filter == "" && len(b.global) == 0 && !localVisible &&
				(!privateVisible || len(b.private) == 0) {
				placeholder := "no " + strings.ToLower(k.String()) + " yet"
				tree = append(tree, node{depth: 2, label: placeholder, itemIdx: -1, isEmpty: true})
			}
			// Private subgroup: orchestrator / tmp / tool-internal
			// sessions. Collapsed by default so dozens of conductor
			// worktrees don't bury the rest of the tree on first paint.
			// `H` toggle suppresses the entire group when the user wants
			// a clean view.
			if privateVisible && len(b.private) > 0 {
				pPath := kPath + "/Private"
				tree = append(tree, node{depth: 2, label: pPath, isGroup: true, itemIdx: -1, collapsed: !m.expanded[pPath]})
				if m.expanded[pPath] {
					if k == model.KindSession {
						tree = m.renderSessionLeaves(b.private, pPath, 3, tree)
					} else {
						sortItems(b.private)
						for _, idx := range b.private {
							tree = append(tree, node{depth: 3, label: m.items[idx].Name, itemIdx: idx})
						}
					}
				}
			}
		}
	}

	m.tree = tree
	if m.cursor >= len(m.tree) {
		m.cursor = len(m.tree) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	// When a filter is active, jump the cursor to the first leaf so j/k
	// land on actual items immediately instead of forcing the user to
	// scroll past 2-3 group rows.
	if filter != "" {
		for i, n := range m.tree {
			if !n.isGroup && n.itemIdx >= 0 {
				m.cursor = i
				break
			}
		}
	}
}

// renderSessionLeaves emits a hierarchical Sessions sub-tree under
// parentPath: <parentPath>/<project>/<date-bucket>/<leaf>. Project is
// taken from Item.Meta["project"] (falling back to "(no project)"),
// date bucket from Item.Meta["lastUpdated"]. Empty buckets are
// dropped so a project with one chat today doesn't render four
// (Today / Yesterday / This week / Older) collapsible nodes for the
// same leaf. PRI-70.
func (m *Model) renderSessionLeaves(idxs []int, parentPath string, baseDepth int, tree []node) []node {
	if len(idxs) == 0 {
		return tree
	}
	type slot struct {
		items map[string][]int
	}
	byProject := map[string]*slot{}
	var projectOrder []string
	for _, idx := range idxs {
		proj := m.items[idx].Meta["project"]
		if proj == "" {
			proj = "(no project)"
		}
		s := byProject[proj]
		if s == nil {
			s = &slot{items: map[string][]int{}}
			byProject[proj] = s
			projectOrder = append(projectOrder, proj)
		}
		bucket := dateBucket(m.items[idx].Meta["lastUpdated"], time.Now())
		s.items[bucket] = append(s.items[bucket], idx)
	}
	// Stable, predictable order: cwd-project first if it surfaced
	// (matches user expectation that "this project" sits at the top),
	// then everything else alphabetically.
	cwdProjectName := ""
	if m.projectDir != "" {
		cwdProjectName = filepath.Base(m.projectDir)
	}
	sort.SliceStable(projectOrder, func(i, j int) bool {
		if projectOrder[i] == cwdProjectName {
			return true
		}
		if projectOrder[j] == cwdProjectName {
			return false
		}
		return projectOrder[i] < projectOrder[j]
	})

	bucketOrder := []string{"Today", "Yesterday", "This week", "Older"}
	for _, proj := range projectOrder {
		projPath := parentPath + "/" + proj
		tree = append(tree, node{depth: baseDepth, label: projPath, isGroup: true, itemIdx: -1, collapsed: !m.expanded[projPath]})
		tree[len(tree)-1].labelOverride(proj)
		if !m.expanded[projPath] {
			continue
		}
		slots := byProject[proj]
		for _, bucket := range bucketOrder {
			list := slots.items[bucket]
			if len(list) == 0 {
				continue
			}
			bPath := projPath + "/" + bucket
			tree = append(tree, node{depth: baseDepth + 1, label: bPath, isGroup: true, itemIdx: -1, collapsed: !m.expanded[bPath]})
			tree[len(tree)-1].labelOverride(bucket)
			if !m.expanded[bPath] {
				continue
			}
			sort.SliceStable(list, func(i, j int) bool {
				return m.items[list[i]].Meta["lastUpdated"] > m.items[list[j]].Meta["lastUpdated"]
			})
			for _, idx := range list {
				tree = append(tree, node{depth: baseDepth + 2, label: m.items[idx].Name, itemIdx: idx})
			}
		}
	}
	return tree
}

// renderHookLeaves renders hook items grouped by Meta["event"]
// (PreToolUse / PostToolUse / SessionStart / ...). Each event is its
// own collapsible subgroup; events open by default so the user sees
// the hooks immediately on first paint. Hooks without an event meta
// fall through to a "(no event)" bucket — defensive: should not happen
// in practice but keeps malformed config from disappearing silently.
func (m *Model) renderHookLeaves(idxs []int, parentPath string, baseDepth int, tree []node) []node {
	if len(idxs) == 0 {
		return tree
	}
	byEvent := map[string][]int{}
	var eventOrder []string
	for _, idx := range idxs {
		ev := m.items[idx].Meta["event"]
		if ev == "" {
			ev = "(no event)"
		}
		if _, seen := byEvent[ev]; !seen {
			eventOrder = append(eventOrder, ev)
		}
		byEvent[ev] = append(byEvent[ev], idx)
	}
	sort.Strings(eventOrder)
	for _, ev := range eventOrder {
		evPath := parentPath + "/" + ev
		// Default-open: events not yet present in m.expanded count as
		// open. The session/local subgroups follow the opposite
		// convention (default-collapsed) — hooks are usually a short
		// list, so showing them by default beats hiding them.
		if _, set := m.expanded[evPath]; !set {
			m.expanded[evPath] = true
		}
		tree = append(tree, node{depth: baseDepth, label: evPath, isGroup: true, itemIdx: -1, collapsed: !m.expanded[evPath]})
		tree[len(tree)-1].labelOverride(ev)
		if !m.expanded[evPath] {
			continue
		}
		list := byEvent[ev]
		sort.SliceStable(list, func(i, j int) bool {
			return m.items[list[i]].Name < m.items[list[j]].Name
		})
		for _, idx := range list {
			tree = append(tree, node{depth: baseDepth + 1, label: m.items[idx].Name, itemIdx: idx})
		}
	}
	return tree
}

// dateBucket classifies an RFC3339 timestamp into one of the four
// bins the Sessions tree groups by. Boundaries are local-TZ calendar
// cuts: today is "from local 00:00 onward", yesterday is the previous
// 24-hour calendar day, "This week" covers the 5 days before that,
// everything earlier (or unparseable) is "Older". Now is taken as a
// parameter so tests can pin specific boundaries deterministically.
func dateBucket(ts string, now time.Time) string {
	if ts == "" {
		return "Older"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "Older"
	}
	tLocal := t.Local()
	nowLocal := now.Local()
	year, month, day := nowLocal.Date()
	todayStart := time.Date(year, month, day, 0, 0, 0, 0, nowLocal.Location())
	yesterdayStart := todayStart.AddDate(0, 0, -1)
	// "This week" covers the 5 calendar days before yesterday (i.e.
	// 2..6 days ago). Day 7 ago and earlier falls through to "Older".
	weekStart := todayStart.AddDate(0, 0, -6)
	switch {
	case !tLocal.Before(todayStart):
		return "Today"
	case !tLocal.Before(yesterdayStart):
		return "Yesterday"
	case !tLocal.Before(weekStart):
		return "This week"
	default:
		return "Older"
	}
}

// usageFooter sums the per-session cost across every Claude session
// item in m.items and returns a "Today: $X · 7d: $Y · 30d: $Z" string
// for the third title-row slot. Returns "" when there are no priced
// sessions or when the privacy toggle (H) is on. Codex / Gemini are
// counted with their own usage data when adapters populate it; today
// only Claude does.
func (m Model) usageFooter() string {
	if m.hidePrivateSessions {
		// Same toggle that suppresses private sessions also masks the
		// cost line — privacy-conscious users won't want to read their
		// spend on a shared screen either.
		return ""
	}
	now := time.Now()
	var today, week, month float64
	var anyPriced bool
	for _, it := range m.items {
		if it.Kind != model.KindSession {
			continue
		}
		raw := it.Meta["cost_usd"]
		if raw == "" {
			continue
		}
		cost, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339, it.Meta["lastUpdated"])
		if err != nil {
			continue
		}
		age := now.Sub(ts)
		if age < 24*time.Hour {
			today += cost
		}
		if age < 7*24*time.Hour {
			week += cost
		}
		if age < 30*24*time.Hour {
			month += cost
		}
		anyPriced = true
	}
	if !anyPriced {
		return ""
	}
	return fmt.Sprintf("usage · today $%.2f · 7d $%.2f · 30d $%.2f", today, week, month)
}

// defaultStatusLine builds the bottom hint for the normal browse mode,
// showing only the keys that actually apply to whatever is under the
// cursor. Modal modes (filter, picker, editor, conflict) have their
// own short hints and don't go through here.
func (m Model) defaultStatusLine() string {
	tail := []string{"/ filter", "r reload", "? help", "q quit"}

	if m.cursor < 0 || m.cursor >= len(m.tree) {
		return " " + strings.Join(tail, " · ") + " "
	}
	n := m.tree[m.cursor]

	if !n.isGroup && !n.isEmpty {
		it := m.items[n.itemIdx]
		ctx := []string{"tab/enter open", "e edit"}
		if it.Storage == model.StorageEntry {
			// PRI-76: surface the inline / form-mode editor on
			// StorageEntry rows so the user discovers it without
			// hunting through `?` help.
			ctx = append(ctx, "E form")
		} else {
			ctx = append(ctx, "E inline")
		}
		if it.Kind == model.KindMCP || it.Storage == model.StorageEntry {
			ctx = append(ctx, "t json/toml")
		}
		ctx = append(ctx, "d del")
		if actions.CanPlace(it) {
			ctx = append(ctx, "p place")
		}
		if it.Drift {
			ctx = append(ctx, "R resync")
		}
		if it.Kind == model.KindSession {
			ctx = append(ctx, "R resume", "T new-tab")
		}
		return " " + strings.Join(append(ctx, tail...), " · ") + " "
	}

	// Group node. tab/enter toggles expand. n new only on a Scope-level
	// group whose Kind we can scaffold (Skill / Agent / Prompt).
	verb := "expand"
	if !n.collapsed {
		verb = "collapse"
	}
	ctx := []string{"tab/enter " + verb}

	if parts := strings.Split(n.label, "/"); len(parts) == 3 {
		if k, ok := model.ParseKind(parts[1]); ok {
			switch k {
			case model.KindSkill, model.KindAgent, model.KindPrompt:
				ctx = append(ctx, "n new")
			}
		}
	}
	return " " + strings.Join(append(ctx, tail...), " · ") + " "
}

// helpText returns the static help-overlay body. Kept short so it fits in
// most terminals without further scrolling.
func helpText() string {
	return "" +
		titleStyle.Render("lazyagent — keys") + "\n\n" +
		"  j/k      navigate up/down (scroll body in detail)\n" +
		"  h/l      collapse / expand group\n" +
		"  tab      open item full-screen / back out (toggle)\n" +
		"  enter    same as tab (drill into item or expand group)\n" +
		"  space    page down in fullscreen detail\n" +
		"  g / G    jump to top / end in fullscreen detail\n" +
		"  /        filter items by name/description\n" +
		"  esc      clear filter / cancel filter editor / cancel confirm\n" +
		"  t        toggle JSON / TOML for MCP entries\n" +
		"  d        delete item (asks y/n)\n" +
		"  f        auto-fix invalid item (rewrites bad frontmatter / hook entry)\n" +
		"  F        bulk auto-fix every invalid item in the tree\n" +
		"  p        place item — pick which (Origin × Scope) cells project\n" +
		"           the item from the library; bytes live once in\n" +
		"           ~/.lazyagent/library and project back to each chosen cell\n" +
		"  R        resync drifted shared item — or resume a session (Sessions kind)\n" +
		"  S        sync-all: bulk Place every shareable global item to every tool\n" +
		"  T        resume a session in a new terminal tab (TUI stays open)\n" +
		"  H        toggle visibility of Private sessions (persists across runs)\n" +
		"  G        toggle visibility of subagent (Task-spawn) sessions — off by default\n" +
		"  e        open in $EDITOR — for StorageEntry items (MCP / Hook /\n" +
		"           codex profile) it edits a temp JSON fragment, not the\n" +
		"           whole config; the entry is written back on save\n" +
		"  E        edit in built-in editor — form for known entry shapes\n" +
		"           (MCP / Hook), JSON textarea for plain files / unknown\n" +
		"           entries; ctrl+s save · ctrl+m toggle list mode · esc cancel\n" +
		"  n        create new Skill / Agent / Prompt\n" +
		"  i        install from a github.com / gist URL\n" +
		"  u        usage / cost summary across loaded sessions\n" +
		"  b        context budget — passive token cost of installed items\n" +
		"  U        update an installed item to the origin's latest sha\n" +
		"  Z        undo / restore from snapshots\n" +
		"  A        toggle all-local mode (fold every discovered project's items)\n" +
		"  B        toggle Mode B (per-project subgroups under Local; needs A)\n" +
		"  r        reload all sources\n" +
		"  ?        toggle this help\n" +
		"  q        quit\n" +
		"\n" + dimStyle.Render("press any key to close")
}

// crossPickerText renders the cross-tool target picker overlay.
// confirmText is the body of the "are you sure?" overlay shown for
// destructive actions. Currently only delete uses this — copy/move
// were merged into the place overlay (key `p`).
func confirmText(p pendingOp, _ string) string {
	var lines []string
	lines = append(lines, titleStyle.Render(p.verb()))
	lines = append(lines, "")
	lines = append(lines, "  "+p.item.Name+dimStyle.Render(
		fmt.Sprintf("  (%s · %s · %s)", p.item.Origin, p.item.Kind, p.item.Scope)))
	lines = append(lines, dimStyle.Render("  "+p.item.Path))
	lines = append(lines, "")
	switch p.kind {
	case pendDelete:
		lines = append(lines, "Permanently remove from disk.")
	case pendFix:
		lines = append(lines, dimStyle.Render("Reason: "+p.fix.Reason))
		lines = append(lines, "")
		lines = append(lines, "Proposed change:")
		lines = append(lines, "")
		lines = append(lines, fixDiffPreview(p.fix))
		lines = append(lines, "")
		lines = append(lines, dimStyle.Render("Re-validates after write; rolls back if still invalid."))
	}
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render("y to confirm · n / esc to cancel"))
	return strings.Join(lines, "\n")
}

// fixDiffPreview renders a compact line-oriented diff between the
// before/after bytes of a FixPlan. We don't pull in a real diff library
// for this — the typical fix touches one or two frontmatter lines and
// the user just wants to see what changed. Lines added show with `+`;
// lines removed with `-`. Long files are truncated past 12 changes so
// the overlay stays readable.
func fixDiffPreview(plan actions.FixPlan) string {
	before := strings.Split(string(plan.Before), "\n")
	after := strings.Split(string(plan.After), "\n")
	var lines []string
	beforeSet := map[string]int{}
	for _, ln := range before {
		beforeSet[ln]++
	}
	afterSet := map[string]int{}
	for _, ln := range after {
		afterSet[ln]++
	}
	const maxLines = 12
	count := 0
	for _, ln := range before {
		if afterSet[ln] > 0 {
			afterSet[ln]--
			continue
		}
		if count == maxLines {
			lines = append(lines, dimStyle.Render("  …"))
			count++
			continue
		}
		if count < maxLines {
			lines = append(lines, "  - "+ln)
		}
		count++
	}
	count = 0
	for _, ln := range after {
		if beforeSet[ln] > 0 {
			beforeSet[ln]--
			continue
		}
		if count == maxLines {
			lines = append(lines, dimStyle.Render("  …"))
			count++
			continue
		}
		if count < maxLines {
			lines = append(lines, "  + "+ln)
		}
		count++
	}
	if len(lines) == 0 {
		return dimStyle.Render("  (no textual change)")
	}
	return strings.Join(lines, "\n")
}

// overlay renders `inner` centered on top of `base`, both sized to the
// outer (w x h) viewport. `inner` is wrapped in a bordered box.
func overlay(base, inner string, w, h int) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7aa2f7")).
		Padding(1, 2).
		Render(inner)

	// Place the box centered. lipgloss.Place handles padding around it.
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box,
		lipgloss.WithWhitespaceChars(" "),
	)
}

// itemMatches reports whether item i should appear under the given filter
// string (already lower-cased and trimmed). Matches name first, then
// description, with a simple substring rule.
func itemMatches(it model.Item, filter string) bool {
	if strings.Contains(strings.ToLower(it.Name), filter) {
		return true
	}
	if it.Description != "" && strings.Contains(strings.ToLower(it.Description), filter) {
		return true
	}
	return false
}

// labelOverride is a tiny shim to attach a display label without adding
// another field for now. We piggy-back on Meta-less node by stuffing the
// override into expanded-key map? Simpler: store it inline.
func (n *node) labelOverride(s string) { n.display = s }

func (m Model) View() string {
	if m.loading {
		return "loading..."
	}
	if m.err != nil {
		return "error: " + m.err.Error() + "\n\nq to quit"
	}

	w := m.width
	h := m.height
	if w == 0 {
		w = 100
	}
	if h == 0 {
		h = 30
	}

	const (
		marginTop    = 1
		marginLeft   = 2
		marginRight  = 2
		marginBottom = 1
		statusLines  = 1
		gap          = 1 // gap between the two panels so borders don't kiss
	)

	// Each panel's rendered width = Width() (content) + 2 (left+right border).
	// Two panels + gap must fit within (w - marginLeft - marginRight).
	// Same logic vertically: rendered height = Height() + 2 (top+bottom border).
	const panelBorderW = 2
	const panelBorderH = 2

	availW := w - marginLeft - marginRight
	bannerLines := 0
	if m.updateAvailable != "" && !m.updateBannerOff {
		bannerLines = 1
	}
	availH := h - marginTop - marginBottom - statusLines - bannerLines
	if availW < 40 {
		availW = 40
	}
	if availH < 6 {
		availH = 6
	}
	// Subtract borders for two panels + the gap between them.
	innerW := availW - panelBorderW*2 - gap
	if innerW < 20 {
		innerW = 20
	}
	contentH := availH - panelBorderH
	if contentH < 4 {
		contentH = 4
	}

	var body string
	if len(m.items) == 0 && m.editing == nil && !m.detailFull && m.installing == nil && m.creating == nil {
		// PRI-20: empty-state. When every adapter returned zero items
		// (fresh install, no project markers in cwd) the split view
		// shows two empty panels and the user has no signal that the
		// TUI loaded successfully. Replace the body with a centered
		// logo + hint block. We keep the surrounding panel border so
		// help / install overlays still anchor correctly when opened.
		fullW := availW - panelBorderW
		if fullW < 20 {
			fullW = 20
		}
		body = focusedPanelStyle.Width(fullW).Height(contentH).Render(renderEmptyState(fullW, contentH))
	} else if m.editing != nil {
		// Editor takes the entire inner area, just like detailFull.
		// The editor's own resize() already sized the textarea on
		// open / WindowSizeMsg; here we only render and frame it.
		fullW := availW - panelBorderW
		if fullW < 20 {
			fullW = 20
		}
		body = focusedPanelStyle.Width(fullW).Height(contentH).Render(editorView(m.editing))
	} else if m.detailFull {
		// Fullscreen: detail panel takes the entire inner area.
		fullW := availW - panelBorderW
		if fullW < 20 {
			fullW = 20
		}
		right := m.renderDetail(fullW, contentH)
		body = focusedPanelStyle.Width(fullW).Height(contentH).Render(right)
	} else {
		leftW := innerW / 3
		if leftW < 20 {
			leftW = 20
		}
		rightW := innerW - leftW

		left := m.renderTree(leftW, contentH)
		right := m.renderDetail(rightW, contentH)

		leftPanel := panelStyle.Width(leftW).Height(contentH).Render(left)
		rightPanel := panelStyle.Width(rightW).Height(contentH).Render(right)
		if m.focus == focusTree {
			leftPanel = focusedPanelStyle.Width(leftW).Height(contentH).Render(left)
		} else {
			rightPanel = focusedPanelStyle.Width(rightW).Height(contentH).Render(right)
		}

		body = lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, strings.Repeat(" ", gap), rightPanel)
	}
	if m.helpOpen {
		body = overlay(body, helpText(), innerW+gap+panelBorderW*2, contentH+panelBorderH)
	} else if m.pending != nil {
		body = overlay(body, confirmText(*m.pending, m.projectDir),
			innerW+gap+panelBorderW*2, contentH+panelBorderH)
	} else if m.placePicker != nil {
		body = overlay(body, placePickerText(*m.placePicker),
			innerW+gap+panelBorderW*2, contentH+panelBorderH)
	} else if m.resyncPicker != nil {
		body = overlay(body, resyncPickerText(*m.resyncPicker),
			innerW+gap+panelBorderW*2, contentH+panelBorderH)
	} else if m.creating != nil {
		body = overlay(body, createOverlayText(*m.creating),
			innerW+gap+panelBorderW*2, contentH+panelBorderH)
	} else if m.installing != nil {
		body = overlay(body, installOverlayText(m.installing),
			innerW+gap+panelBorderW*2, contentH+panelBorderH)
	} else if m.syncing != nil {
		body = overlay(body, syncOverlayText(*m.syncing),
			innerW+gap+panelBorderW*2, contentH+panelBorderH)
	} else if m.fixing != nil {
		body = overlay(body, fixOverlayText(*m.fixing),
			innerW+gap+panelBorderW*2, contentH+panelBorderH)
	} else if m.usaging != nil {
		body = overlay(body, usageOverlayText(computeUsageStats(m.items, m.usaging.window, time.Now())),
			innerW+gap+panelBorderW*2, contentH+panelBorderH)
	} else if m.budgeting != nil {
		body = overlay(body, budgetOverlayText(budget.Estimate(m.items), m.budgeting.window),
			innerW+gap+panelBorderW*2, contentH+panelBorderH)
	} else if m.forming != nil {
		body = overlay(body, formView(m.forming),
			innerW+gap+panelBorderW*2, contentH+panelBorderH)
	} else if m.restoreOverlay != nil {
		body = overlay(body, restoreOverlayText(*m.restoreOverlay),
			innerW+gap+panelBorderW*2, contentH+panelBorderH)
	}
	var statusLine string
	switch {
	case m.toast != "" && time.Now().Before(m.toastUntil):
		statusLine = " " + m.toast
	case m.editing != nil && m.editing.conflict:
		statusLine = " conflict · o overwrite · r reload from disk · esc keep editing "
	case m.editing != nil:
		statusLine = " editor · ctrl+s save · esc cancel · ctrl+r reload from disk "
	case m.detailFull:
		statusLine = " j/k scroll · space pgdn · g/G top/end · t json/toml · tab/esc back "
	case m.filterMode:
		statusLine = " filter · ↑↓ navigate · tab/enter open · esc cancel · backspace edit · ctrl+u clear "
	case m.placePicker != nil:
		statusLine = " arrows move · space toggle · enter apply · esc cancel "
	case m.syncing != nil:
		if m.syncing.applied {
			statusLine = " sync done · esc close "
		} else {
			statusLine = " sync · j/k navigate · y apply · o overwrite-on-conflict · esc cancel "
		}
	case m.fixing != nil:
		if m.fixing.applied {
			statusLine = " fix done · esc close "
		} else {
			statusLine = " fix-all · j/k navigate · y apply fixable · esc cancel "
		}
	case m.usaging != nil:
		statusLine = " usage · tab cycle window · esc close "
	case m.budgeting != nil:
		statusLine = " context budget · tab cycle reference · esc close "
	case m.forming != nil:
		statusLine = " form · ctrl+s save · tab next · ctrl+m toggle list mode · esc cancel "
	case m.resyncPicker != nil:
		statusLine = " c canonical wins · t tool wins · esc cancel "
	default:
		statusLine = m.defaultStatusLine()
	}
	status := statusStyle.Render(truncRunes(statusLine, availW))

	// PRI-19: render the update banner directly above the status line
	// when the background poll found a newer release and the user has
	// not silenced it yet. updateBannerOff flips on the first key press
	// of the day so we honour the dismissal for the rest of the run too.
	var banner string
	if m.updateAvailable != "" && !m.updateBannerOff {
		banner = renderUpdateBanner(m.updateAvailable, m.updateURL, m.installSource, availW)
	}

	// Build the final output with explicit margins. lipgloss Padding/Margin
	// on the outer style doesn't reliably translate into visible whitespace
	// over AltScreen for multi-line content, so we do it by hand: blank
	// lines for the top margin, and a left-side pad on every visible row.
	// Total rows must equal h exactly — too many and the top scrolls off.
	var stacked string
	if banner != "" {
		stacked = lipgloss.JoinVertical(lipgloss.Left, body, banner, status)
	} else {
		stacked = lipgloss.JoinVertical(lipgloss.Left, body, status)
	}
	pad := strings.Repeat(" ", marginLeft)
	lines := strings.Split(stacked, "\n")
	padded := make([]string, 0, marginTop+len(lines)+marginBottom)
	for i := 0; i < marginTop; i++ {
		padded = append(padded, "")
	}
	for _, ln := range lines {
		padded = append(padded, pad+ln)
	}
	for i := 0; i < marginBottom; i++ {
		padded = append(padded, "")
	}
	// Cap to terminal height to be safe — avoid the dreaded "AltScreen
	// scrolls and the top vanishes" when math is off by one.
	if len(padded) > h {
		padded = padded[:h]
	}
	return strings.Join(padded, "\n")
}

func (m Model) renderTree(w, h int) string {
	contentW := w - 2 // panel padding (1 per side)
	if contentW < 10 {
		contentW = 10
	}

	lines := []string{
		titleStyle.Render(truncRunes("lazyagent", contentW)),
	}
	if m.projectDir != "" {
		lines = append(lines, dimStyle.Render(truncRunes("project: "+m.projectDir, contentW)))
	} else {
		lines = append(lines, dimStyle.Render(truncRunes("(no project local scope)", contentW)))
	}
	switch {
	case m.filterMode:
		lines = append(lines, truncRunes("/"+m.filterText+"█", contentW))
	case m.filterText != "":
		lines = append(lines, dimStyle.Render(truncRunes("filter: "+m.filterText+"  (esc to clear)", contentW)))
	default:
		// PRI-31: when no filter is active, surface the usage / cost
		// aggregate here. Hidden when the H privacy toggle is on so a
		// shoulder-surfer can't read the user's spend at a glance.
		if footer := m.usageFooter(); footer != "" {
			lines = append(lines, dimStyle.Render(truncRunes(footer, contentW)))
		} else {
			lines = append(lines, "")
		}
	}

	avail := h - len(lines)
	if avail < 1 {
		avail = 1
	}

	// keep cursor visible inside `avail` rows
	start := 0
	if m.cursor >= avail {
		start = m.cursor - avail + 1
	}
	end := start + avail
	if end > len(m.tree) {
		end = len(m.tree)
	}

	for i := start; i < end; i++ {
		n := m.tree[i]
		indent := strings.Repeat("  ", n.depth)
		var raw string
		var styled string
		if n.isGroup {
			marker := "▾"
			if !m.expanded[n.label] {
				marker = "▸"
			}
			disp := n.display
			if disp == "" {
				disp = lastSeg(n.label)
			}
			raw = indent + marker + " " + disp
			raw = truncRunes(raw, contentW)
			styled = groupStyle.Render(raw)
		} else if n.isEmpty {
			// PRI-20: dim "no skills yet" placeholder for empty kind groups.
			raw = indent + "  " + n.label
			raw = truncRunes(raw, contentW)
			styled = dimStyle.Render(raw)
		} else {
			label := n.label
			drifted := false
			invalid := false
			warning := false
			cwdGone := false
			// (s) badge for items that resolve into the lazyagent shared
			// store. Cheap signal that the bytes are canonical and the
			// item shows up under multiple Origins simultaneously. Skip
			// for the Shared origin itself — every leaf there is shared
			// by definition, so the badge would just add noise.
			if n.itemIdx >= 0 && n.itemIdx < len(m.items) {
				it := m.items[n.itemIdx]
				if it.Shared && it.Origin != model.OriginShared {
					label += " (s)"
				}
				if it.Drift {
					label += " (drift)"
					drifted = true
				}
				// PRI-4: surface the source project for items folded in
				// from the global indexer. Items in cwd-project don't
				// carry the Meta key, so they render unchanged.
				if proj, ok := it.Meta["project"]; ok && proj != "" && proj != m.projectDir {
					label += " [" + filepath.Base(proj) + "]"
				}
				// PRI-18: surface frontmatter problems in the tree so a
				// user with a malformed file knows where to look. Errors
				// take precedence over warnings (`(invalid)` swallows the
				// `(?)`).
				switch {
				case it.ParseError != "":
					label += " (invalid)"
					invalid = true
				case len(it.ValidationWarnings) > 0:
					label += " (?)"
					warning = true
				}
				// Sessions whose project dir was deleted from disk —
				// transcript is still readable, but `R` would only
				// produce a confusing upstream-CLI failure. Mark
				// visually so the user knows resume won't work.
				if it.Meta["cwdGone"] == "1" {
					label += " (cwd gone)"
					cwdGone = true
				}
			}
			raw = indent + "  " + label
			raw = truncRunes(raw, contentW)
			styled = raw
			switch {
			case invalid:
				styled = invalidStyle.Render(raw)
			case drifted:
				styled = driftStyle.Render(raw)
			case warning:
				styled = warnStyle.Render(raw)
			case cwdGone:
				// Dim — same treatment as empty-group placeholders.
				// Lower weight than (drift) since this isn't user-fixable.
				styled = dimStyle.Render(raw)
			}
		}
		if i == m.cursor && m.focus == focusTree {
			styled = selectedStyle.Render(padRightW(raw, contentW))
		}
		lines = append(lines, styled)
	}

	return joinExactly(lines, h)
}

func lastSeg(p string) string {
	i := strings.LastIndex(p, "/")
	if i == -1 {
		return p
	}
	return p[i+1:]
}

// truncRunes shortens s to fit within w terminal columns, appending '…'
// when the string is cut. Treats input as plain text (no embedded ANSI).
func truncRunes(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	var b strings.Builder
	cw := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if cw+rw > w-1 {
			break
		}
		b.WriteRune(r)
		cw += rw
	}
	b.WriteRune('…')
	return b.String()
}

// padRightW pads s on the right with spaces so its rendered width equals w.
// Width-aware via runewidth (handles wide CJK chars and emoji correctly).
func padRightW(s string, w int) string {
	sw := runewidth.StringWidth(s)
	if sw >= w {
		return s
	}
	return s + strings.Repeat(" ", w-sw)
}

// joinExactly returns a single string of exactly h lines, padding with empty
// rows or truncating from the bottom. Use this on every panel-rendering path
// so panel height never overflows its allotted space (which would push the
// status bar off-screen).
func joinExactly(lines []string, h int) string {
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// wrapLines hard-wraps each input "logical line" to fit within w columns and
// returns the resulting flat list of display rows. Empty input lines remain
// empty (one blank row per blank input). Width-aware via runewidth, so wide
// runes (emoji, CJK) advance the cursor correctly.
func wrapLines(lines []string, w int) []string {
	if w < 1 {
		w = 1
	}
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if ln == "" {
			out = append(out, "")
			continue
		}
		var cur []rune
		curW := 0
		for _, r := range ln {
			rw := runewidth.RuneWidth(r)
			if rw == 0 {
				rw = 1
			}
			if curW+rw > w {
				out = append(out, string(cur))
				cur = cur[:0]
				curW = 0
			}
			cur = append(cur, r)
			curW += rw
		}
		out = append(out, string(cur))
	}
	return out
}

func (m Model) renderDetail(w, h int) string {
	contentW := w - 2 // panel padding (1 per side)
	if contentW < 10 {
		contentW = 10
	}

	if len(m.tree) == 0 || m.cursor >= len(m.tree) {
		return joinExactly([]string{dimStyle.Render("(empty)")}, h)
	}
	n := m.tree[m.cursor]
	if n.isGroup || n.itemIdx < 0 {
		return joinExactly([]string{dimStyle.Render("select an item with enter / l")}, h)
	}
	it := m.items[n.itemIdx]

	// Header lines (already truncated to contentW, so no wrapping needed).
	var wrapped []string
	wrapped = append(wrapped, titleStyle.Render(truncRunes(it.Name, contentW)))
	wrapped = append(wrapped, dimStyle.Render(truncRunes(
		fmt.Sprintf("%s · %s · %s", it.Origin, it.Kind, it.Scope), contentW)))
	wrapped = append(wrapped, dimStyle.Render(truncRunes("path: "+it.Path, contentW)))

	// Validation banner (PRI-18). A red block for parse errors, a yellow
	// strip for soft warnings — both go above the description so the user
	// sees them before the content even when scrolled to the top.
	if it.ParseError != "" {
		wrapped = append(wrapped, "")
		wrapped = append(wrapped, invalidStyle.Render(truncRunes("⚠ invalid frontmatter — item still listed but won't be picked up correctly by the tool", contentW)))
		for _, line := range wrapLines([]string{it.ParseError}, contentW) {
			wrapped = append(wrapped, invalidStyle.Render(line))
		}
	}
	if len(it.ValidationWarnings) > 0 {
		wrapped = append(wrapped, "")
		wrapped = append(wrapped, warnStyle.Render(truncRunes("validation warnings:", contentW)))
		for _, w := range it.ValidationWarnings {
			for _, line := range wrapLines([]string{"  • " + w}, contentW) {
				wrapped = append(wrapped, warnStyle.Render(line))
			}
		}
	}

	// Description: short, plain-wrap.
	if it.Description != "" {
		wrapped = append(wrapped, "")
		wrapped = append(wrapped, wrapLines([]string{it.Description}, contentW)...)
	}

	// Body / config: render through glamour (markdown + syntax highlight),
	// cached per (item path, format, width). Sessions lazy-load the full
	// transcript on first access.
	if c := m.detailContent(it); c != "" {
		wrapped = append(wrapped, "")
		wrapped = append(wrapped, m.renderMarkdown(it.Path, m.detailFmt, c, contentW)...)
	}

	total := len(wrapped)
	avail := h
	hasIndicator := total > avail
	if hasIndicator {
		avail = h - 1
		if avail < 1 {
			avail = 1
		}
	}

	scroll := m.detailScroll
	maxScroll := total - avail
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	end := scroll + avail
	if end > total {
		end = total
	}
	visible := wrapped[scroll:end]

	if !hasIndicator {
		return joinExactly(visible, h)
	}

	pct := 100
	if total > 0 {
		pct = (end * 100) / total
	}
	indicator := dimStyle.Render(truncRunes(
		fmt.Sprintf("%d%% (%d-%d / %d)  j/k scroll", pct, scroll+1, end, total),
		contentW))

	// Pad body to (h-1) rows, then append indicator on the last row.
	body := make([]string, 0, h)
	body = append(body, visible...)
	for len(body) < h-1 {
		body = append(body, "")
	}
	body = append(body, indicator)
	return joinExactly(body, h)
}

// renderMarkdown returns the glamour-rendered body as a slice of display
// rows (already wrapped to contentW with ANSI styling). Results are cached
// per (path, format, width) — invalidated on resize because width is part
// of the key, and on reload because Update wipes the cache.
//
// On any error we fall back to a plain hard-wrap so the user still sees
// the content.
func (m Model) renderMarkdown(path string, f detailFormat, content string, contentW int) []string {
	key := fmt.Sprintf("%s|%d|%d", path, f, contentW)
	if cached, ok := m.glamourCache[key]; ok {
		return cached
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(contentW),
	)
	var out []string
	if err == nil {
		rendered, rerr := r.Render(content)
		if rerr == nil {
			rendered = strings.TrimRight(rendered, "\n")
			out = strings.Split(rendered, "\n")
		}
	}
	if out == nil {
		out = wrapLines(strings.Split(content, "\n"), contentW)
	}
	m.glamourCache[key] = out
	return out
}

// detailContent picks what to feed into the glamour pipeline for the
// detail panel. Sessions get a lazily built full transcript (cached
// by Item.Path); everything else falls through to selectDetailContent
// which honours the json/toml format toggle for config-shaped items.
func (m *Model) detailContent(it model.Item) string {
	if it.Kind != model.KindSession {
		return selectDetailContent(it, m.detailFmt)
	}
	if cached, ok := m.sessionBodyCache[it.Path]; ok {
		return cached
	}
	var body string
	switch it.Origin {
	case model.OriginClaude:
		body = parse.BuildClaudeTranscript(it.Path)
	case model.OriginGemini:
		body = parse.BuildGeminiTranscript(it.Path)
	default:
		// Codex (SQLite) lands in slice 3 — fall back to the preview
		// stamped at adapter time so the panel still shows something.
		body = it.Body
	}
	m.sessionBodyCache[it.Path] = body
	return body
}

func selectDetailContent(it model.Item, f detailFormat) string {
	hasJSON := it.RawJSON != ""
	hasTOML := it.RawTOML != ""
	switch f {
	case formatJSON:
		if hasJSON {
			return "```json\n" + it.RawJSON + "\n```"
		}
	case formatTOML:
		if hasTOML {
			return "```toml\n" + it.RawTOML + "\n```"
		}
	}
	// auto / fallback: prefer raw config for config-shaped items, else body
	if hasJSON {
		return "```json\n" + it.RawJSON + "\n```"
	}
	if hasTOML {
		return "```toml\n" + it.RawTOML + "\n```"
	}
	return it.Body
}
