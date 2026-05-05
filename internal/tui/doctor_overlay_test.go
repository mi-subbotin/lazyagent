package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/doctor"
)

// writeDoctorRec drops a doctor-<id>.json file under ~/.lazyagent so
// doctor.Latest() can resolve it. The id is fixed so the tests can
// assert against it.
func writeDoctorRec(t *testing.T, rec doctor.Recommendations) {
	t.Helper()
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".lazyagent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "doctor-1700000000.json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestDoctorOverlay_EmptyState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := newTestModel(t, fixtureItems(), "")
	m = feed(t, m, "!")
	if m.doctorOverlay == nil {
		t.Fatalf("! did not open doctorOverlay")
	}
	body := doctorOverlayText(*m.doctorOverlay)
	if !strings.Contains(body, "No recommendations on disk") {
		t.Errorf("expected empty-state hint, got:\n%s", body)
	}
}

func TestDoctorOverlay_BuildRows_MatchesItems(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	rec := doctor.Recommendations{
		CLI: "claude",
		Duplicates: []doctor.DupSuggestion{
			{Names: []string{"alpha", "beta"}, Reason: "very similar"},
		},
		Unused: []doctor.UnusedSuggestion{
			{Name: "reviewer", Kind: "Agent", Reason: "no recent use"},
			{Name: "ghost", Kind: "Agent", Reason: "phantom"},
		},
		Other: []doctor.FreeFormSuggestion{
			{Title: "tip", Body: "consider archiving older skills"},
		},
	}
	writeDoctorRec(t, rec)

	m := newTestModel(t, fixtureItems(), "")
	m = feed(t, m, "!")
	if m.doctorOverlay == nil {
		t.Fatalf("! did not open doctorOverlay")
	}
	rows := m.doctorOverlay.rows
	if got := len(rows); got != 4 {
		t.Fatalf("rows=%d, want 4", got)
	}
	// Duplicates: alpha + beta both exist → actionable.
	if !rows[0].actionable {
		t.Errorf("duplicate row should be actionable when 2+ matches")
	}
	if len(rows[0].items) != 2 {
		t.Errorf("duplicate row items=%d, want 2", len(rows[0].items))
	}
	// Unused reviewer: 1 match → actionable.
	if !rows[1].actionable {
		t.Errorf("unused row for reviewer should be actionable")
	}
	// Unused ghost: 0 matches → not actionable.
	if rows[2].actionable {
		t.Errorf("unused row for ghost should not be actionable (no match)")
	}
	// Other: never actionable.
	if rows[3].actionable {
		t.Errorf("other row must never be actionable")
	}
}

func TestDoctorOverlay_ToggleAndConfirm(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	rec := doctor.Recommendations{
		CLI: "claude",
		Unused: []doctor.UnusedSuggestion{
			{Name: "reviewer", Kind: "Agent", Reason: "stale"},
		},
	}
	writeDoctorRec(t, rec)

	m := newTestModel(t, fixtureItems(), "")
	m = feed(t, m, "!")
	if m.doctorOverlay == nil {
		t.Fatalf("! did not open doctorOverlay")
	}
	// Toggle the unused row.
	m = feed(t, m, " ")
	if !m.doctorOverlay.rows[0].checked {
		t.Errorf("space did not toggle checked")
	}
	// y → confirm phase (since 1 row is checked).
	m = feed(t, m, "y")
	if m.doctorOverlay.phase != doctorPhaseConfirm {
		t.Errorf("phase=%v, want confirm", m.doctorOverlay.phase)
	}
	body := doctorOverlayText(*m.doctorOverlay)
	if !strings.Contains(body, "1 unused suggestion") {
		t.Errorf("confirm body missing unused count:\n%s", body)
	}
	// n → back to list.
	m = feed(t, m, "n")
	if m.doctorOverlay.phase != doctorPhaseList {
		t.Errorf("phase=%v, want list after n", m.doctorOverlay.phase)
	}
}

func TestDoctorOverlay_ToggleAll(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	rec := doctor.Recommendations{
		Duplicates: []doctor.DupSuggestion{
			{Names: []string{"alpha", "beta"}, Reason: "x"},
		},
		Unused: []doctor.UnusedSuggestion{
			{Name: "reviewer", Kind: "Agent"},
		},
		Other: []doctor.FreeFormSuggestion{
			{Title: "info"},
		},
	}
	writeDoctorRec(t, rec)

	m := newTestModel(t, fixtureItems(), "")
	m = feed(t, m, "!")
	m = feed(t, m, "a")
	checked := 0
	for _, r := range m.doctorOverlay.rows {
		if r.checked {
			checked++
		}
	}
	if checked != 2 {
		t.Errorf("after toggle-all, checked=%d, want 2 (Other never togglable)", checked)
	}
	// Second a should clear them all.
	m = feed(t, m, "a")
	for _, r := range m.doctorOverlay.rows {
		if r.checked {
			t.Errorf("toggle-all twice should clear; row %s still checked", r.title)
		}
	}
}

func TestDoctorOverlay_EmptyApplyToast(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	rec := doctor.Recommendations{
		Unused: []doctor.UnusedSuggestion{{Name: "reviewer", Kind: "Agent"}},
	}
	writeDoctorRec(t, rec)

	m := newTestModel(t, fixtureItems(), "")
	m = feed(t, m, "!")
	// y without checking anything → toast, stays in list phase.
	m = feed(t, m, "y")
	if m.doctorOverlay.phase != doctorPhaseList {
		t.Errorf("phase=%v, want list", m.doctorOverlay.phase)
	}
	if !strings.Contains(m.toast, "nothing selected") {
		t.Errorf("toast=%q, want nothing-selected", m.toast)
	}
}
