package tui

import (
	"sort"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/actions"
	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestApplyMerge_RejectsItemWithoutDupGroup(t *testing.T) {
	m := Model{}
	err := m.applyMerge(model.Item{Name: "echo"})
	if err == nil {
		t.Fatal("expected error for item without DupGroup")
	}
}

func TestMergeTargets_UnionsCellsAcrossGroup(t *testing.T) {
	m := Model{
		items: []model.Item{
			{Origin: model.OriginClaude, Scope: model.ScopeGlobal, DupGroup: "g1"},
			{Origin: model.OriginCodex, Scope: model.ScopeGlobal, DupGroup: "g1"},
			{Origin: model.OriginGemini, Scope: model.ScopeGlobal, DupGroup: "g1"},
			{Origin: model.OriginShared, Scope: model.ScopeGlobal, DupGroup: "g1"}, // skipped
			{Origin: model.OriginClaude, Scope: model.ScopeLocal, DupGroup: "other"},
		},
	}
	got := m.mergeTargets(model.Item{DupGroup: "g1"})
	want := []actions.ProjectionTarget{
		{Origin: model.OriginClaude, Scope: model.ScopeGlobal},
		{Origin: model.OriginCodex, Scope: model.ScopeGlobal},
		{Origin: model.OriginGemini, Scope: model.ScopeGlobal},
	}
	sort.Slice(got, func(i, j int) bool {
		if got[i].Origin != got[j].Origin {
			return got[i].Origin < got[j].Origin
		}
		return got[i].Scope < got[j].Scope
	})
	if len(got) != len(want) {
		t.Fatalf("expected %d targets, got %d (%+v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("target[%d]: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMergeTargets_DeduplicatesSameCell(t *testing.T) {
	m := Model{
		items: []model.Item{
			{Origin: model.OriginClaude, Scope: model.ScopeGlobal, DupGroup: "g1"},
			{Origin: model.OriginClaude, Scope: model.ScopeGlobal, DupGroup: "g1"},
		},
	}
	got := m.mergeTargets(model.Item{DupGroup: "g1"})
	if len(got) != 1 {
		t.Fatalf("expected 1 unique target, got %d", len(got))
	}
}
