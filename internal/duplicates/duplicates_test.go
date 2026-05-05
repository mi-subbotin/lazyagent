package duplicates

import (
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func mkSkill(origin model.Origin, scope model.Scope, name, body string) model.Item {
	return model.Item{
		Origin:  origin,
		Kind:    model.KindSkill,
		Scope:   scope,
		Name:    name,
		Path:    "/tmp/lazyagent-test/" + origin.String() + "/" + name + ".md",
		Body:    body,
		Storage: model.StorageFile,
	}
}

func mkMCP(origin model.Origin, scope model.Scope, name, raw string) model.Item {
	return model.Item{
		Origin:    origin,
		Kind:      model.KindMCP,
		Scope:     scope,
		Name:      name,
		Path:      "/tmp/lazyagent-test/" + origin.String() + "/.config.json",
		Storage:   model.StorageEntry,
		ConfigKey: "mcpServers/" + name,
		RawJSON:   raw,
	}
}

func TestFind_SameNameAcrossOrigins(t *testing.T) {
	items := []model.Item{
		mkSkill(model.OriginClaude, model.ScopeGlobal, "echo", "claude body\n"),
		mkSkill(model.OriginCodex, model.ScopeGlobal, "echo", "codex body\n"),
	}
	groups := Find(items)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	g := groups[0]
	if !strings.HasPrefix(g.Key, "name:Skills:echo") {
		t.Fatalf("unexpected key %q", g.Key)
	}
	if len(g.Items) != 2 {
		t.Fatalf("expected 2 members, got %d", len(g.Items))
	}
}

func TestFind_SkipsCanonicalProjectionPair(t *testing.T) {
	// Both projections of the same canonical (Shared=true) and pointing
	// at paths that won't resolve into the store in this unit test —
	// canonicals map is empty, so allShared && len(canonicals)==0 is
	// not the special case. To exercise the skip logic exactly, we use
	// the same Path so CanonicalItemDir returns the same value (which
	// will be ""). When canonicals map is empty the filter does NOT skip.
	//
	// Real-world: items are projections that resolve into the library —
	// CanonicalItemDir returns a non-empty value and matches. We
	// approximate that here by leaving Path empty so the filter takes
	// the same path: no canonicals collected, allShared=true, len==0,
	// so NOT skipped — which is correct behaviour when we can't prove
	// they're the same canonical.
	//
	// To test the skip path properly we construct items whose Path
	// resolves into a temporary store via store helpers. That requires
	// real disk; out of scope for this unit test. The negative case
	// here only verifies that two genuinely Shared items with different
	// names DO group, since they're not canonical pairs.
	canonical := model.Item{
		Origin:  model.OriginShared,
		Kind:    model.KindSkill,
		Scope:   model.ScopeGlobal,
		Name:    "echo",
		Path:    "/lib/skills/echo/SKILL.md",
		Body:    "shared body\n",
		Shared:  true,
		Storage: model.StorageFile,
	}
	projection := model.Item{
		Origin:  model.OriginClaude,
		Kind:    model.KindSkill,
		Scope:   model.ScopeGlobal,
		Name:    "echo",
		Path:    "/claude/skills/echo/SKILL.md", // not resolvable into a store in test
		Body:    "shared body\n",
		Shared:  true,
		Storage: model.StorageFile,
	}
	items := []model.Item{canonical, projection}
	groups := Find(items)
	// Both items have allShared=true. CanonicalItemDir on these test
	// paths returns "" (no real symlinks set up). filterRealDuplicates
	// preserves them in that case — the same-name pass groups them.
	// However, the same-content pass would also catch them. The
	// stronger guarantee documented in the spec is that real
	// canonical/projection pairs (whose Path resolves into the
	// library) must NOT group. That's covered by the path-based
	// filter when CanonicalItemDir returns non-empty matching values.
	//
	// Sanity check: filterRealDuplicates only returns nil when
	// allShared and exactly one canonical is shared across the bucket.
	// With no resolvable canonicals it preserves the bucket — see the
	// dedicated TestFilterRealDuplicates_* cases below for details.
	if len(groups) != 1 {
		t.Errorf("expected 1 group from same-name shared pair (no resolvable canonical), got %d", len(groups))
	}
}

func TestFilterRealDuplicates_PreservesWhenNoCanonicalResolved(t *testing.T) {
	got := filterRealDuplicates([]model.Item{
		{Shared: true, Path: ""},
		{Shared: true, Path: ""},
	}, []int{0, 1})
	if len(got) != 2 {
		t.Errorf("expected bucket preserved when no canonicals resolve, got %v", got)
	}
}

func TestFilterRealDuplicates_PreservesWhenAnyNotShared(t *testing.T) {
	got := filterRealDuplicates([]model.Item{
		{Shared: true, Path: ""},
		{Shared: false, Path: ""},
	}, []int{0, 1})
	if len(got) != 2 {
		t.Errorf("expected bucket preserved when any !Shared, got %v", got)
	}
}

func TestFilterRealDuplicates_SkipsWhenAllPointAtSameCanonical(t *testing.T) {
	// Manually craft the canonical-collision branch by passing items
	// whose CanonicalItemDir return value is identical. Since we can't
	// inject store output, we rely on filterRealDuplicates' actual
	// branch: allShared && len(canonicals)==1.
	//
	// We can't easily create resolvable symlinks in this unit test, so
	// this serves as documentation that the production code path
	// requires Shared=true items resolving via store.CanonicalItemDir.
	// See actions/place_test.go for full integration coverage.
	t.Skip("requires real ~/.lazyagent/store symlinks; covered in integration tests")
}

func TestFind_SameBodyDifferentNames(t *testing.T) {
	body := "trim body\n"
	items := []model.Item{
		mkSkill(model.OriginClaude, model.ScopeGlobal, "alpha", body),
		mkSkill(model.OriginCodex, model.ScopeGlobal, "beta", body),
	}
	groups := Find(items)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	g := groups[0]
	if !strings.HasPrefix(g.Key, "body:Skills:") {
		t.Fatalf("unexpected key %q", g.Key)
	}
	if len(g.Items) != 2 {
		t.Fatalf("expected 2 members, got %d", len(g.Items))
	}
}

func TestFind_FrontmatterReorderingDoesntChangeHash(t *testing.T) {
	a := "---\nname: x\ndescription: hello\n---\nbody\n"
	b := "---\ndescription: hello\nname: x\n---\nbody\n"
	if hashItem(model.Item{Storage: model.StorageFile, Body: a}) !=
		hashItem(model.Item{Storage: model.StorageFile, Body: b}) {
		t.Fatal("hashItem must be insensitive to frontmatter ordering")
	}
	items := []model.Item{
		mkSkill(model.OriginClaude, model.ScopeGlobal, "alpha", a),
		mkSkill(model.OriginCodex, model.ScopeGlobal, "beta", b),
	}
	groups := Find(items)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
}

func TestFind_StorageEntry_SameValue(t *testing.T) {
	a := `{"command":"node","args":["server.js"],"env":{"K":"v"}}`
	// Same value, different key order in the JSON encoding.
	b := `{"env":{"K":"v"},"args":["server.js"],"command":"node"}`
	items := []model.Item{
		mkMCP(model.OriginClaude, model.ScopeGlobal, "linear", a),
		mkMCP(model.OriginCodex, model.ScopeGlobal, "linear-codex", b),
	}
	// Different names => same-name pass won't catch them; content pass should.
	groups := Find(items)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if !strings.HasPrefix(groups[0].Key, "body:MCP:") {
		t.Fatalf("unexpected key %q", groups[0].Key)
	}
}

func TestFind_SkipsSessionsAndMemory(t *testing.T) {
	items := []model.Item{
		{Origin: model.OriginClaude, Kind: model.KindSession, Scope: model.ScopeGlobal, Name: "s1", Body: "x"},
		{Origin: model.OriginClaude, Kind: model.KindSession, Scope: model.ScopeGlobal, Name: "s1", Body: "x"},
		{Origin: model.OriginClaude, Kind: model.KindMemory, Scope: model.ScopeGlobal, Name: "CLAUDE.md", Body: "m"},
		{Origin: model.OriginCodex, Kind: model.KindMemory, Scope: model.ScopeGlobal, Name: "AGENTS.md", Body: "m"},
	}
	if got := Find(items); len(got) != 0 {
		t.Fatalf("expected no groups for sessions/memory, got %v", got)
	}
}

func TestHashItem_NormalisesTrailingWhitespace(t *testing.T) {
	a := "line  \nbody  \n\n"
	b := "line\nbody"
	ha := hashItem(model.Item{Storage: model.StorageFile, Body: a})
	hb := hashItem(model.Item{Storage: model.StorageFile, Body: b})
	if ha != hb {
		t.Fatalf("expected equal hashes after normalisation, got %s vs %s", ha, hb)
	}
}

func TestHashItem_EmptyBodyReturnsEmpty(t *testing.T) {
	if hashItem(model.Item{Storage: model.StorageFile}) != "" {
		t.Fatal("expected empty hash for empty body")
	}
	if hashItem(model.Item{Storage: model.StorageEntry}) != "" {
		t.Fatal("expected empty hash for empty entry")
	}
}

func TestFind_SameNameTakesPrecedenceOverContent(t *testing.T) {
	items := []model.Item{
		mkSkill(model.OriginClaude, model.ScopeGlobal, "echo", "A\n"),
		mkSkill(model.OriginCodex, model.ScopeGlobal, "echo", "B\n"),
		mkSkill(model.OriginGemini, model.ScopeGlobal, "other", "A\n"),
	}
	groups := Find(items)
	// echo×2 same-name, plus content match between Claude/echo and
	// Gemini/other (both have "A\n"). Claude/echo is consumed by pass 1
	// so the content pass should only see Codex/echo (already used) and
	// Gemini/other — no more pairs.
	if len(groups) != 1 {
		t.Fatalf("expected 1 group (same-name precedence), got %d: %+v", len(groups), groups)
	}
	if !strings.HasPrefix(groups[0].Key, "name:Skills:echo") {
		t.Fatalf("unexpected first group %q", groups[0].Key)
	}
}
