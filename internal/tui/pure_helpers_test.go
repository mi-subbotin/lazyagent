package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/actions"
	"github.com/mi-subbotin/lazyagent/internal/install"
	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/state"
)

func TestUpgradeHintFor(t *testing.T) {
	cases := []struct {
		src     string
		url     string
		want    string
		desc    string
	}{
		{"brew", "ignored", "brew upgrade lazyagent", "brew always wins"},
		{"go-install", "ignored", "go install github.com/mi-subbotin/lazyagent/cmd/lazyagent@latest", "go-install canned hint"},
		{"unknown", "https://example/r", "https://example/r", "unknown source falls back to URL"},
		{"", "", "github.com/mi-subbotin/lazyagent/releases", "no source no URL → releases page"},
	}
	for _, c := range cases {
		if got := upgradeHintFor(c.src, c.url); got != c.want {
			t.Errorf("%s: got %q want %q", c.desc, got, c.want)
		}
	}
}

func TestRenderUpdateBannerEmpty(t *testing.T) {
	if got := renderUpdateBanner("", "", "", 80); got != "" {
		t.Errorf("empty version → empty banner, got %q", got)
	}
}

func TestRenderUpdateBannerContent(t *testing.T) {
	got := renderUpdateBanner("v0.5.0", "", "brew", 120)
	if !strings.Contains(got, "v0.5.0") {
		t.Errorf("banner missing version: %q", got)
	}
	if !strings.Contains(got, "brew upgrade") {
		t.Errorf("banner missing brew hint: %q", got)
	}
}

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"v0.5.0": "v0.5.0",
		"0.5.0":  "v0.5.0",
		"1.2.3":  "v1.2.3",
	}
	for in, want := range cases {
		if got := normalizeVersion(in); got != want {
			t.Errorf("normalizeVersion(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestIsUpdateBannerDismissed(t *testing.T) {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)

	if isUpdateBannerDismissed(state.State{}, "v0.5.0", now) {
		t.Error("zero state → not dismissed")
	}

	dismissed := state.State{UpdateBannerDismissedFor: "0.5.0", UpdateBannerDismissedDate: "2026-05-02"}
	if !isUpdateBannerDismissed(dismissed, "v0.5.0", now) {
		t.Error("matching version (with v-prefix normalisation) and date → dismissed")
	}

	staleDate := state.State{UpdateBannerDismissedFor: "0.5.0", UpdateBannerDismissedDate: "2026-05-01"}
	if isUpdateBannerDismissed(staleDate, "v0.5.0", now) {
		t.Error("yesterday's dismissal must not silence today's banner")
	}

	wrongVersion := state.State{UpdateBannerDismissedFor: "0.4.9", UpdateBannerDismissedDate: "2026-05-02"}
	if isUpdateBannerDismissed(wrongVersion, "v0.5.0", now) {
		t.Error("dismissal for older version must not silence newer banner")
	}
}

func TestResyncPickerText(t *testing.T) {
	p := resyncPicker{item: model.Item{Name: "foo", Kind: model.KindSkill}}
	got := resyncPickerText(p)
	for _, want := range []string{"Resync foo", "canonical", "tool wins", "esc"} {
		if !strings.Contains(got, want) {
			t.Errorf("resyncPickerText missing %q:\n%s", want, got)
		}
	}
}

func TestNewResyncPicker(t *testing.T) {
	it := model.Item{Name: "x", Kind: model.KindAgent}
	p := newResyncPicker(it)
	if p == nil || p.item.Name != "x" {
		t.Errorf("expected picker carrying item, got %+v", p)
	}
}

func TestJoinPlaceTargets(t *testing.T) {
	ts := []actions.ProjectionTarget{
		{Origin: model.OriginClaude, Scope: model.ScopeGlobal},
		{Origin: model.OriginGemini, Scope: model.ScopeLocal},
	}
	got := joinPlaceTargets(ts)
	if !strings.Contains(got, "Claude/Global") || !strings.Contains(got, "Gemini/Local") {
		t.Errorf("joinPlaceTargets=%q", got)
	}
	if got := joinPlaceTargets(nil); got != "" {
		t.Errorf("empty input must yield empty string, got %q", got)
	}
}

func TestFirstReason(t *testing.T) {
	row := []placeCell{
		{enabled: true},
		{enabled: false, reason: "lossy md→toml"},
		{enabled: false, reason: "second"},
	}
	if got := firstReason(row); got != "lossy md→toml" {
		t.Errorf("firstReason=%q, want first disabled reason", got)
	}
	if got := firstReason([]placeCell{{enabled: true}}); got != "" {
		t.Errorf("no disabled cells → empty, got %q", got)
	}
}

func TestAnyLossy(t *testing.T) {
	if !anyLossy([]placeCell{{lossy: false}, {lossy: true}}) {
		t.Error("expected true with at least one lossy")
	}
	if anyLossy([]placeCell{{}, {}}) {
		t.Error("no lossy cells → false")
	}
}

func TestPlacePickerTextPickPhase(t *testing.T) {
	p := placePicker{
		item:    model.Item{Name: "alpha", Kind: model.KindSkill},
		origins: []model.Origin{model.OriginClaude, model.OriginCodex},
		scopes:  []model.Scope{model.ScopeGlobal, model.ScopeLocal},
		cells: [][]placeCell{
			{{enabled: true, checked: true}, {enabled: true}},
			{{enabled: false, reason: "no codex skill"}, {enabled: false, reason: "no codex skill"}},
		},
		cursorRow: 0,
		cursorCol: 0,
	}
	got := placePickerText(p)
	for _, want := range []string{"Place alpha", "Library", "[x]", "[ ]", "no codex skill", "arrows", "apply"} {
		if !strings.Contains(got, want) {
			t.Errorf("placePickerText missing %q:\n%s", want, got)
		}
	}
}

func TestPlacePickerTextEntry(t *testing.T) {
	p := placePicker{
		item:    model.Item{Name: "mcp-x", Kind: model.KindMCP, Storage: model.StorageEntry},
		origins: []model.Origin{model.OriginClaude},
		scopes:  []model.Scope{model.ScopeGlobal},
		cells:   [][]placeCell{{{enabled: true, checked: true}}},
	}
	got := placePickerText(p)
	if !strings.Contains(got, "Library: n/a") {
		t.Errorf("entry items should declare Library: n/a, got:\n%s", got)
	}
}

func TestPlacePickerTextLossyAnnotation(t *testing.T) {
	p := placePicker{
		item:    model.Item{Name: "x", Kind: model.KindSkill},
		origins: []model.Origin{model.OriginClaude},
		scopes:  []model.Scope{model.ScopeGlobal},
		cells:   [][]placeCell{{{enabled: true, lossy: true}}},
	}
	got := placePickerText(p)
	if !strings.Contains(got, "(lossy)") {
		t.Errorf("expected (lossy) annotation, got:\n%s", got)
	}
}

func TestPlaceConfirmText(t *testing.T) {
	p := placePicker{
		phase: placePhaseConfirm,
		item:  model.Item{Name: "alpha"},
		conflicts: []actions.ShareConflict{
			{Target: model.OriginClaude, Path: "/foo", Kind: "file"},
			{Target: model.OriginCodex, Path: "/bar", Kind: "symlink"},
		},
	}
	got := placePickerText(p)
	for _, want := range []string{"Overwrite 2 existing", "/foo", "/bar", "alpha"} {
		if !strings.Contains(got, want) {
			t.Errorf("placeConfirmText missing %q:\n%s", want, got)
		}
	}
}

func TestCountMutating(t *testing.T) {
	plan := actions.Plan{
		Ops: []actions.PlanOp{
			{Action: actions.ActionImport},
			{Action: actions.ActionSkip},
			{Action: actions.ActionResync},
			{Action: actions.ActionSkip},
		},
	}
	if got := countMutating(plan); got != 2 {
		t.Errorf("countMutating=%d, want 2", got)
	}
	if got := countMutating(actions.Plan{}); got != 0 {
		t.Errorf("empty plan → 0, got %d", got)
	}
}

func TestNewSyncOverlay(t *testing.T) {
	plan := actions.Plan{Ops: []actions.PlanOp{{Action: actions.ActionImport}}}
	ov := newSyncOverlay(plan)
	if ov == nil {
		t.Fatal("expected non-nil overlay")
	}
	if len(ov.plan.Ops) != 1 {
		t.Errorf("plan not propagated, got %d ops", len(ov.plan.Ops))
	}
}

func TestSyncOverlayTextPreApply(t *testing.T) {
	plan := actions.Plan{
		Ops: []actions.PlanOp{
			{Action: actions.ActionImport, Item: model.Item{Origin: model.OriginClaude, Kind: model.KindSkill, Name: "alpha"}},
			{Action: actions.ActionSkip, Item: model.Item{Origin: model.OriginCodex, Kind: model.KindAgent, Name: "beta"}, Reason: "ok"},
		},
	}
	got := syncOverlayText(syncOverlay{plan: plan})
	for _, want := range []string{"Plan:", "alpha", "[import]", "[skip]", "[esc] cancel"} {
		if !strings.Contains(got, want) {
			t.Errorf("pre-apply text missing %q:\n%s", want, got)
		}
	}
}

func TestSyncOverlayTextPostApply(t *testing.T) {
	plan := actions.Plan{Ops: []actions.PlanOp{{Action: actions.ActionImport}}}
	s := syncOverlay{
		plan:    plan,
		applied: true,
		errs:    []error{strErr("first error"), strErr("second")},
	}
	got := syncOverlayText(s)
	for _, want := range []string{"Sync complete", "first error", "second", "[esc] close"} {
		if !strings.Contains(got, want) {
			t.Errorf("post-apply text missing %q:\n%s", want, got)
		}
	}
}

func TestSyncOverlayTextEmptyPlan(t *testing.T) {
	got := syncOverlayText(syncOverlay{})
	if !strings.Contains(got, "(no items to consider)") {
		t.Errorf("empty plan should hint to user, got:\n%s", got)
	}
}

// strErr is a minimal error value with the given message — keeps the
// post-apply test independent of any real action error type.
type strErr string

func (e strErr) Error() string { return string(e) }

func TestInstallTargetOptionLabel(t *testing.T) {
	enabled := installTargetOption{origin: "Claude", scope: "global"}
	if got, want := enabled.label(), "Claude (global)"; got != want {
		t.Errorf("label=%q, want %q", got, want)
	}
	disabled := installTargetOption{origin: "Codex", scope: "local", disabled: true, reason: "no profile path"}
	if got := disabled.label(); !strings.Contains(got, "no profile path") {
		t.Errorf("disabled label should embed reason: %q", got)
	}
}

func TestFirstNextPrevEnabled(t *testing.T) {
	opts := []installTargetOption{
		{disabled: true},
		{disabled: false},
		{disabled: true},
		{disabled: false},
	}
	if got := firstEnabled(opts); got != 1 {
		t.Errorf("firstEnabled=%d, want 1", got)
	}
	if got := nextEnabled(opts, 1); got != 3 {
		t.Errorf("nextEnabled(1)=%d, want 3", got)
	}
	if got := nextEnabled(opts, 3); got != 3 {
		t.Errorf("nextEnabled past last enabled stays put, got %d", got)
	}
	if got := prevEnabled(opts, 3); got != 1 {
		t.Errorf("prevEnabled(3)=%d, want 1", got)
	}
	if got := prevEnabled(opts, 1); got != 1 {
		t.Errorf("prevEnabled past first stays put, got %d", got)
	}

	allDisabled := []installTargetOption{{disabled: true}, {disabled: true}}
	if got := firstEnabled(allDisabled); got != 0 {
		t.Errorf("all-disabled returns 0, got %d", got)
	}
}

func TestNewInstallOverlay(t *testing.T) {
	ov := newInstallOverlay()
	if ov == nil || ov.phase != phaseInstallURL {
		t.Errorf("expected fresh overlay at phaseInstallURL, got %+v", ov)
	}
}

func TestShortHelper(t *testing.T) {
	if got := short(3, 8); got != 3 {
		t.Errorf("short(3,8)=%d, want 3 (n < max)", got)
	}
	if got := short(20, 8); got != 8 {
		t.Errorf("short(20,8)=%d, want 8 (clamped)", got)
	}
	if got := short(8, 8); got != 8 {
		t.Errorf("short(8,8)=%d, want 8 (n == max → max)", got)
	}
}

func TestInstallOverlayTextURLPhase(t *testing.T) {
	ov := &installOverlay{phase: phaseInstallURL, url: "github.com/foo/bar"}
	got := installOverlayText(ov)
	for _, want := range []string{"Install from GitHub", "github.com/foo/bar", "enter", "esc"} {
		if !strings.Contains(got, want) {
			t.Errorf("URL phase missing %q:\n%s", want, got)
		}
	}
}

func TestInstallOverlayTextURLPhaseWithError(t *testing.T) {
	ov := &installOverlay{phase: phaseInstallURL, url: "bad", err: "not a github url"}
	got := installOverlayText(ov)
	if !strings.Contains(got, "not a github url") {
		t.Errorf("error not rendered:\n%s", got)
	}
}

func TestInstallOverlayTextFetchPhase(t *testing.T) {
	ov := &installOverlay{phase: phaseInstallFetch, url: "github.com/x/y"}
	got := installOverlayText(ov)
	for _, want := range []string{"Working on github.com/x/y", "few seconds"} {
		if !strings.Contains(got, want) {
			t.Errorf("fetch phase missing %q:\n%s", want, got)
		}
	}
}

func TestInstallOverlayTextPickPhase(t *testing.T) {
	ov := &installOverlay{
		phase: phaseInstallPick,
		sha:   "abcdef1234567890",
		candidates: []install.Candidate{
			{Kind: model.KindSkill, Name: "alpha", Description: "first"},
			{Kind: model.KindAgent, Name: "beta", ParseError: "bad"},
		},
		selected: []bool{true, false},
		cursor:   0,
	}
	got := installOverlayText(ov)
	for _, want := range []string{"abcdef12", "alpha", "beta", "[x]", "[ ]", "(invalid)"} {
		if !strings.Contains(got, want) {
			t.Errorf("pick phase missing %q:\n%s", want, got)
		}
	}
}

func TestInstallOverlayTextTargetPhase(t *testing.T) {
	ov := &installOverlay{
		phase: phaseInstallTarget,
		targetOpts: []installTargetOption{
			{origin: "Claude", scope: "global"},
			{origin: "Codex", scope: "local", disabled: true, reason: "no codex skill"},
		},
		targetCursor: 0,
	}
	got := installOverlayText(ov)
	for _, want := range []string{"pick target", "Claude", "Codex", "no codex skill"} {
		if !strings.Contains(got, want) {
			t.Errorf("target phase missing %q:\n%s", want, got)
		}
	}
}

func TestInstallOverlayTextConfirmPhase(t *testing.T) {
	ov := &installOverlay{
		phase: phaseInstallConfirm,
		conflicts: []installConflict{
			{cand: install.Candidate{Name: "alpha"}, target: "/tmp/alpha"},
		},
	}
	got := installOverlayText(ov)
	for _, want := range []string{"already exists", "/tmp/alpha", "Conflict 1 / 1", "replace", "keep", "skip"} {
		if !strings.Contains(got, want) {
			t.Errorf("confirm phase missing %q:\n%s", want, got)
		}
	}
}

func TestTrimReason(t *testing.T) {
	if got := trimReason("short error"); got != "short error" {
		t.Errorf("short pass-through, got %q", got)
	}
	if got := trimReason("primary cause — extra detail"); got != "primary cause" {
		t.Errorf("em-dash splitter, got %q", got)
	}
	long := strings.Repeat("x", 80)
	if got := trimReason(long); !strings.HasSuffix(got, "…") || len(got) > 65 {
		t.Errorf("long string should be truncated with …, got %q", got)
	}
}

func TestBuildInstallTargetsLocalGate(t *testing.T) {
	cand := install.Candidate{Kind: model.KindSkill, Name: "alpha", Storage: model.StorageDir}

	withProject := buildInstallTargets(cand, true)
	withoutProject := buildInstallTargets(cand, false)

	for _, opt := range withoutProject {
		if opt.scope == "local" && !opt.disabled {
			t.Errorf("local target should be disabled when no project: %+v", opt)
		}
		if opt.scope == "local" && opt.reason != "no project dir" {
			t.Errorf("disabled local should say 'no project dir', got %q", opt.reason)
		}
	}
	if len(withProject) != len(withoutProject) {
		t.Errorf("len(opts) should be stable across hasProject toggle, got %d vs %d",
			len(withProject), len(withoutProject))
	}
	if len(withProject) == 0 {
		t.Error("expected at least one origin/scope combination")
	}
}

func TestDirOrFileExists(t *testing.T) {
	dir := t.TempDir()
	if dirOrFileExists(filepath.Join(dir, "missing"), model.StorageFile) {
		t.Error("missing file → false")
	}
	f := filepath.Join(dir, "present")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !dirOrFileExists(f, model.StorageFile) {
		t.Error("real file → true")
	}
	// StorageDir checks the parent dir, so a non-existent leaf still
	// returns true when its parent exists.
	if !dirOrFileExists(filepath.Join(dir, "any"), model.StorageDir) {
		t.Error("StorageDir resolves to parent existence — should be true here")
	}
}

func TestCountUnfixable(t *testing.T) {
	entries := []fixEntry{
		{},                    // fixable (empty reason)
		{reason: "broken"},    // unfixable
		{reason: "no plan"},   // unfixable
	}
	if got := countUnfixable(entries); got != 2 {
		t.Errorf("countUnfixable=%d, want 2", got)
	}
	if got := countUnfixable(nil); got != 0 {
		t.Errorf("nil → 0, got %d", got)
	}
}

func TestFixOverlayTextPreApply(t *testing.T) {
	entries := []fixEntry{
		{item: model.Item{Origin: model.OriginClaude, Kind: model.KindSkill, Name: "alpha"}},
		{item: model.Item{Origin: model.OriginCodex, Kind: model.KindAgent, Name: "beta"}, reason: "no plan"},
	}
	got := fixOverlayText(fixOverlay{entries: entries})
	for _, want := range []string{"Invalid items: 2", "fixable: 1", "alpha", "beta", "[fix]", "[skip]", "no plan"} {
		if !strings.Contains(got, want) {
			t.Errorf("pre-apply fix text missing %q:\n%s", want, got)
		}
	}
}

func TestFixOverlayTextPostApply(t *testing.T) {
	entries := []fixEntry{
		{item: model.Item{Name: "alpha"}},
		{item: model.Item{Name: "beta"}, reason: "no plan"},
	}
	f := fixOverlay{
		entries: entries,
		applied: true,
		fixed:   1,
		errs:    []error{strErr("write failed")},
	}
	got := fixOverlayText(f)
	for _, want := range []string{"Fix-all complete", "1 / 2", "1 errors", "write failed", "Skipped 1 unfixable"} {
		if !strings.Contains(got, want) {
			t.Errorf("post-apply fix text missing %q:\n%s", want, got)
		}
	}
}

func TestEditorDirtyAndResizeAndView(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skill.md")
	body := "---\nname: alpha\n---\n\nbody\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal,
		Path: path, Storage: model.StorageFile, Name: "alpha",
	}
	e, err := newEditorState(it)
	if err != nil {
		t.Fatalf("newEditorState: %v", err)
	}
	if e.dirty() {
		t.Error("freshly-loaded buffer must not be dirty")
	}
	e.ta.SetValue(body + "extra")
	if !e.dirty() {
		t.Error("modified buffer should be dirty")
	}
	e.resize(80, 30)
	e.resize(40, 4) // exercise the min-height clamp branch
	view := editorView(e)
	for _, want := range []string{"edit Claude", "skill.md", "ctrl+s save"} {
		if !strings.Contains(view, want) {
			t.Errorf("editorView missing %q:\n%s", want, view)
		}
	}
	// Conflict banner takes over body.
	e.conflict = true
	if got := editorView(e); !strings.Contains(got, "conflict") {
		t.Errorf("expected conflict banner, got:\n%s", got)
	}
}

func TestInstallOverlayTextDonePhase(t *testing.T) {
	ov := &installOverlay{
		phase:   phaseInstallDone,
		summary: []string{"installed alpha at /target/alpha"},
	}
	got := installOverlayText(ov)
	if !strings.Contains(got, "installed alpha") {
		t.Errorf("summary not rendered:\n%s", got)
	}

	ov2 := &installOverlay{phase: phaseInstallDone, err: "fetch failed"}
	if got := installOverlayText(ov2); !strings.Contains(got, "fetch failed") {
		t.Errorf("error not rendered in done phase:\n%s", got)
	}
}
