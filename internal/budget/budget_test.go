package budget

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func TestCountTokensRoughlyMatchesChars(t *testing.T) {
	// Sanity check the encoder loads. Rough heuristic: tokens are
	// always ≤ chars; for English prose typically chars/3..4. We
	// don't pin an exact count because the vocab can update.
	in := "The quick brown fox jumps over the lazy dog. " // 45 chars
	got := CountTokens(in)
	if got <= 0 || got > len(in) {
		t.Errorf("CountTokens=%d; want 0 < n ≤ %d", got, len(in))
	}
}

func TestCountTokensEmpty(t *testing.T) {
	if CountTokens("") != 0 {
		t.Error("empty string should yield 0 tokens")
	}
}

func TestEstimateItemFile(t *testing.T) {
	ResetEstimateCache()
	t.Cleanup(ResetEstimateCache)
	dir := t.TempDir()
	path := filepath.Join(dir, "skill.md")
	body := "# Skill body\nSome instructions here that an LLM would load on activation.\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal,
		Storage: model.StorageFile, Path: path,
	}
	tokens, ok := EstimateItem(it)
	if !ok {
		t.Fatal("expected ok=true for a markdown skill")
	}
	if tokens <= 0 {
		t.Errorf("expected non-zero tokens, got %d", tokens)
	}
	// Cache hit on second call returns same value.
	tokens2, _ := EstimateItem(it)
	if tokens2 != tokens {
		t.Errorf("cached read returned %d, first read %d", tokens2, tokens)
	}
}

func TestEstimateItemSkillDir(t *testing.T) {
	ResetEstimateCache()
	t.Cleanup(ResetEstimateCache)
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillMD := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillMD, []byte("# main body that gets loaded"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A side-asset that should NOT be counted.
	if err := os.WriteFile(filepath.Join(skillDir, "data.csv"), []byte("a,b,c\n1,2,3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal,
		Storage: model.StorageDir, Path: skillMD,
	}
	tokens, ok := EstimateItem(it)
	if !ok || tokens <= 0 {
		t.Errorf("expected non-zero token count for skill dir, got %d ok=%v", tokens, ok)
	}
}

func TestEstimateItemSession(t *testing.T) {
	_, ok := EstimateItem(model.Item{Kind: model.KindSession})
	if ok {
		t.Error("Session items should be excluded from passive context budget")
	}
}

func TestEstimateItemHook(t *testing.T) {
	_, ok := EstimateItem(model.Item{Kind: model.KindHook})
	if ok {
		t.Error("Hooks should be excluded from passive context budget")
	}
}

func TestEstimateItemEntry(t *testing.T) {
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal,
		Storage: model.StorageEntry,
		RawJSON: `{"command":"npx","args":["foo"]}`,
	}
	tokens, ok := EstimateItem(it)
	if !ok {
		t.Error("MCP entries should be measurable")
	}
	if tokens <= 0 {
		t.Errorf("expected non-zero tokens for entry, got %d", tokens)
	}
}

func TestEstimateRollsUpByGroup(t *testing.T) {
	ResetEstimateCache()
	t.Cleanup(ResetEstimateCache)
	dir := t.TempDir()
	mk := func(name, body string) string {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal, Storage: model.StorageFile, Path: mk("a.md", "hello world")},
		{Origin: model.OriginClaude, Kind: model.KindSkill, Scope: model.ScopeGlobal, Storage: model.StorageFile, Path: mk("b.md", "another skill body")},
		{Origin: model.OriginClaude, Kind: model.KindAgent, Scope: model.ScopeGlobal, Storage: model.StorageFile, Path: mk("c.md", "agent definition body")},
		{Kind: model.KindSession}, // skipped
	}
	sum := Estimate(items)
	if sum.Items != 3 {
		t.Errorf("sum.Items=%d; want 3 (sessions skipped)", sum.Items)
	}
	if sum.Total <= 0 {
		t.Errorf("expected non-zero total, got %d", sum.Total)
	}
	if len(sum.Groups) != 2 {
		t.Errorf("expected 2 groups (Skill+Agent), got %d", len(sum.Groups))
	}
	for _, g := range sum.Groups {
		switch g.Kind {
		case model.KindSkill:
			if g.Items != 2 {
				t.Errorf("Skill group items=%d; want 2", g.Items)
			}
		case model.KindAgent:
			if g.Items != 1 {
				t.Errorf("Agent group items=%d; want 1", g.Items)
			}
		}
	}
}

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{500, "500"},
		{1500, "1.5k"},
		{12_300, "12.3k"},
		{2_500_000, "2.5M"},
	}
	for _, c := range cases {
		if got := FormatTokens(c.n); got != c.want {
			t.Errorf("FormatTokens(%d) = %q; want %q", c.n, got, c.want)
		}
	}
}
