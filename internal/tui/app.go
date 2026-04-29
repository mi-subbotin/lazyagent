package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/mi-subbotin/lazyagent/internal/actions"
	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
	"github.com/mi-subbotin/lazyagent/internal/sources"
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
	pendCopy
	pendMove
)

type pendingOp struct {
	kind pendingKind
	item model.Item // snapshot — survives even if list reshuffles
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

// crossPicker drives the "where to copy across tools" overlay. Options
// are pre-filtered to those SupportsCross approves of, so the user only
// ever sees combinations the actions package can actually perform.
type crossPicker struct {
	item    model.Item
	options []crossOption
}

type crossOption struct {
	target   model.Origin
	scope    model.Scope
	lossy    bool
	disabled bool
	reason   string
}

func (o crossOption) label() string {
	s := fmt.Sprintf("%s (%s)", o.target, o.scope)
	switch {
	case o.disabled:
		s += " — " + o.reason
	case o.lossy:
		s += " — lossy"
	}
	return s
}

func (p pendingOp) verb() string {
	switch p.kind {
	case pendDelete:
		return "Delete"
	case pendCopy:
		return "Copy to other scope"
	case pendMove:
		return "Move to other scope"
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

	// crossPicker is non-nil while the user is choosing a target tool
	// for cross-tool copy. The user picks via numeric keys (1-N).
	crossPicker *crossPicker

	// sharePicker is non-nil while the multi-select share overlay is
	// open. Targets are toggled with space, confirmed with enter.
	sharePicker *sharePicker

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
}

func New(srcs []sources.Source, projectDir string) Model {
	return Model{
		srcs:             srcs,
		projectDir:       projectDir,
		expanded:         defaultExpanded(),
		loading:          true,
		glamourCache:     map[string][]string{},
		sessionBodyCache: map[string]string{},
	}
}

func defaultExpanded() map[string]bool {
	return map[string]bool{
		"Claude":         true,
		"Claude/Skills":  true,
		"Claude/Agents":  true,
		"Claude/MCP":     true,
		"Claude/Prompts": true,
		"Claude/Memory":  true,
		"Codex":          true,
		"Codex/Skills":   true,
		"Codex/Agents":   true,
		"Codex/MCP":      true,
		"Codex/Prompts":  true,
		"Codex/Memory":   true,
		"Gemini":         true,
		"Gemini/Skills":  true,
		"Gemini/Agents":  true,
		"Gemini/MCP":     true,
		"Gemini/Prompts": true,
		"Gemini/Memory":  true,
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
			it.Drift = store.IsDriftedAgainst(*it, canonical)
		}
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

	case tea.KeyMsg:
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

		// Cross-tool target picker: digits select, esc cancels.
		if m.crossPicker != nil {
			return m.updateCrossPicker(msg)
		}

		// Share picker: multi-select checklist; space toggles, enter commits.
		if m.sharePicker != nil {
			return m.updateSharePicker(msg)
		}

		// Resync picker: c/t/esc.
		if m.resyncPicker != nil {
			return m.updateResyncPicker(msg)
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
		case "c":
			if it, ok := m.currentItem(); ok {
				m.pending = &pendingOp{kind: pendCopy, item: it}
			}
			return m, nil
		case "m":
			if it, ok := m.currentItem(); ok {
				m.pending = &pendingOp{kind: pendMove, item: it}
			}
			return m, nil
		case "x":
			if it, ok := m.currentItem(); ok {
				if p := newCrossPicker(it, m.projectDir); p != nil {
					m.crossPicker = p
				} else {
					m.setToast("no cross-tool targets for this item")
				}
			}
			return m, nil
		case "s":
			it, ok := m.currentItem()
			if !ok {
				return m, nil
			}
			p, err := newSharePicker(it)
			if err != nil {
				m.setToast(err.Error())
				return m, nil
			}
			m.sharePicker = p
			return m, nil
		case "R":
			it, ok := m.currentItem()
			if !ok {
				return m, nil
			}
			if !it.Drift {
				m.setToast("not drifted — nothing to resync")
				return m, nil
			}
			m.resyncPicker = newResyncPicker(it)
			return m, nil
		case "e":
			it, ok := m.currentItem()
			if !ok {
				return m, nil
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
		case "E":
			it, ok := m.currentItem()
			if !ok {
				return m, nil
			}
			if it.Storage == model.StorageEntry {
				m.setToast("entries: use 'e' for raw config edit (E support coming)")
				return m, nil
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

// newCrossPicker builds a picker overlay listing every (target, scope)
// destination so the user sees the full landscape — disabled rows
// explain why a particular destination is unavailable instead of being
// silently filtered out. Returns nil only when literally nothing is
// selectable (in which case the caller surfaces a toast).
func newCrossPicker(it model.Item, projectDir string) *crossPicker {
	var opts []crossOption
	var anyEnabled bool
	for _, target := range []model.Origin{model.OriginClaude, model.OriginCodex, model.OriginGemini} {
		if target == it.Origin {
			continue
		}
		supported := actions.SupportsCross(it, target)
		for _, scope := range []model.Scope{model.ScopeGlobal, model.ScopeLocal} {
			opt := crossOption{target: target, scope: scope}
			switch {
			case !supported:
				opt.disabled = true
				opt.reason = unsupportedCrossReason(it, target)
			case scope == model.ScopeLocal && projectDir == "":
				opt.disabled = true
				opt.reason = "no project local scope"
			default:
				opt.lossy = actions.IsLossyCross(it, target)
				anyEnabled = true
			}
			opts = append(opts, opt)
		}
	}
	if !anyEnabled {
		return nil
	}
	return &crossPicker{item: it, options: opts}
}

// unsupportedCrossReason returns a short human-readable reason why a
// particular target origin cannot accept the given item. With current
// support all three tools have skills / agents / mcp / prompts / memory,
// so this is just a fallback for unknown-future combinations.
func unsupportedCrossReason(it model.Item, target model.Origin) string {
	return "no mapping defined"
}

// updateCrossPicker handles keystrokes while the cross-tool target
// picker overlay is open.
func (m Model) updateCrossPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	if s == "esc" || s == "q" {
		m.crossPicker = nil
		return m, nil
	}
	if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		idx := int(s[0] - '1')
		if idx >= len(m.crossPicker.options) {
			return m, nil
		}
		opt := m.crossPicker.options[idx]
		if opt.disabled {
			m.setToast(opt.reason)
			return m, nil
		}
		it := m.crossPicker.item
		m.crossPicker = nil
		err := actions.CrossCopy(it, opt.target, opt.scope, m.projectDir)
		if err != nil {
			m.setToast(fmt.Sprintf("cross-copy: %s", err.Error()))
			return m, nil
		}
		m.setToast(fmt.Sprintf("Copied %s → %s (%s)", it.Name, opt.target, opt.scope))
		m.loading = true
		return m, m.loadCmd()
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
		case pendCopy:
			err = actions.Copy(op.item, m.projectDir)
		case pendMove:
			err = actions.Move(op.item, m.projectDir)
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
	kindOrder := []model.Kind{model.KindSkill, model.KindAgent, model.KindMCP, model.KindPrompt, model.KindMemory, model.KindSession}

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
			total := len(b.global) + len(b.local) + len(b.private)
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
					sortItems(b.global)
					for _, idx := range b.global {
						tree = append(tree, node{depth: 3, label: m.items[idx].Name, itemIdx: idx})
					}
				}
			}
			if m.projectDir != "" && len(b.local) > 0 {
				lPath := kPath + "/Local"
				tree = append(tree, node{depth: 2, label: lPath, isGroup: true, itemIdx: -1, collapsed: !m.expanded[lPath]})
				if m.expanded[lPath] {
					sortItems(b.local)
					for _, idx := range b.local {
						tree = append(tree, node{depth: 3, label: m.items[idx].Name, itemIdx: idx})
					}
				}
			}
			// Private subgroup: orchestrator / tmp / tool-internal
			// sessions. Collapsed by default so dozens of conductor
			// worktrees don't bury the rest of the tree on first paint.
			if len(b.private) > 0 {
				pPath := kPath + "/Private"
				tree = append(tree, node{depth: 2, label: pPath, isGroup: true, itemIdx: -1, collapsed: !m.expanded[pPath]})
				if m.expanded[pPath] {
					sortItems(b.private)
					for _, idx := range b.private {
						tree = append(tree, node{depth: 3, label: m.items[idx].Name, itemIdx: idx})
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

	if !n.isGroup {
		it := m.items[n.itemIdx]
		ctx := []string{"tab/enter open", "e edit"}
		if it.Storage != model.StorageEntry {
			ctx = append(ctx, "E inline")
		}
		if it.Kind == model.KindMCP || it.Storage == model.StorageEntry {
			ctx = append(ctx, "t json/toml")
		}
		ctx = append(ctx, "d del", "c copy", "m move", "x cross")
		switch {
		case it.Shared:
			ctx = append(ctx, "s reshare")
		case actions.CanShare(it):
			ctx = append(ctx, "s share")
		}
		if it.Drift {
			ctx = append(ctx, "R resync")
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
		"  c        copy item to the other scope (Global ↔ Local)\n" +
		"  m        move item to the other scope\n" +
		"  x        cross-tool copy (pick target Origin / scope)\n" +
		"  s        share to lazyagent store + project to selected tools\n" +
		"  R        resync drifted shared item (canonical / tool wins)\n" +
		"  e        open in $EDITOR (external)\n" +
		"  E        edit in built-in editor (ctrl+s save · esc cancel)\n" +
		"  n        create new Skill / Agent / Prompt\n" +
		"  r        reload all sources\n" +
		"  ?        toggle this help\n" +
		"  q        quit\n" +
		"\n" + dimStyle.Render("press any key to close")
}

// crossPickerText renders the cross-tool target picker overlay.
func crossPickerText(p crossPicker) string {
	var lines []string
	lines = append(lines, titleStyle.Render("Cross-tool copy"))
	lines = append(lines, "")
	lines = append(lines, "  "+p.item.Name+dimStyle.Render(
		fmt.Sprintf("  (%s · %s · %s)", p.item.Origin, p.item.Kind, p.item.Scope)))
	lines = append(lines, "")
	lines = append(lines, "Choose target:")
	for i, opt := range p.options {
		row := fmt.Sprintf("  %d. %s", i+1, opt.label())
		if opt.disabled {
			row = dimStyle.Render(row)
		}
		lines = append(lines, row)
	}
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(
		fmt.Sprintf("press 1-%d to select · esc to cancel", len(p.options))))
	return strings.Join(lines, "\n")
}

// confirmText is the body of the "are you sure?" overlay shown for
// destructive actions.
func confirmText(p pendingOp, projectDir string) string {
	target := "(other scope)"
	if p.kind == pendCopy || p.kind == pendMove {
		switch p.item.Scope {
		case model.ScopeGlobal:
			if projectDir == "" {
				target = "Local — but no project detected!"
			} else {
				target = "Local (" + projectDir + ")"
			}
		case model.ScopeLocal:
			target = "Global"
		}
	}

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
	case pendCopy:
		lines = append(lines, "Copy to: "+target)
	case pendMove:
		lines = append(lines, "Move to: "+target)
	}
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render("y to confirm · n / esc to cancel"))
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
	availH := h - marginTop - marginBottom - statusLines
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
	if m.editing != nil {
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
	} else if m.crossPicker != nil {
		body = overlay(body, crossPickerText(*m.crossPicker),
			innerW+gap+panelBorderW*2, contentH+panelBorderH)
	} else if m.sharePicker != nil {
		body = overlay(body, sharePickerText(*m.sharePicker),
			innerW+gap+panelBorderW*2, contentH+panelBorderH)
	} else if m.resyncPicker != nil {
		body = overlay(body, resyncPickerText(*m.resyncPicker),
			innerW+gap+panelBorderW*2, contentH+panelBorderH)
	} else if m.creating != nil {
		body = overlay(body, createOverlayText(*m.creating),
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
	case m.crossPicker != nil:
		statusLine = " 1-9 select target · esc cancel "
	case m.sharePicker != nil:
		statusLine = " ↑↓ move · space toggle · enter share · esc cancel "
	case m.resyncPicker != nil:
		statusLine = " c canonical wins · t tool wins · esc cancel "
	default:
		statusLine = m.defaultStatusLine()
	}
	status := statusStyle.Render(truncRunes(statusLine, availW))

	// Build the final output with explicit margins. lipgloss Padding/Margin
	// on the outer style doesn't reliably translate into visible whitespace
	// over AltScreen for multi-line content, so we do it by hand: blank
	// lines for the top margin, and a left-side pad on every visible row.
	// Total rows must equal h exactly — too many and the top scrolls off.
	stacked := lipgloss.JoinVertical(lipgloss.Left, body, status)
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
		lines = append(lines, "")
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
		} else {
			label := n.label
			drifted := false
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
			}
			raw = indent + "  " + label
			raw = truncRunes(raw, contentW)
			styled = raw
			if drifted {
				styled = driftStyle.Render(raw)
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
