package parse

import (
	"reflect"
	"testing"
)

func TestGetWalksMaps(t *testing.T) {
	m := map[string]any{
		"a": map[string]any{
			"b": "c",
		},
	}
	got, ok := Get(m, "a/b")
	if !ok || got != "c" {
		t.Errorf("Get(a/b) = %v, %v; want c, true", got, ok)
	}
	if _, ok := Get(m, "a/missing"); ok {
		t.Error("Get on missing key should return false")
	}
}

func TestGetWalksArrays(t *testing.T) {
	m := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{"command": "echo first"},
						map[string]any{"command": "echo second"},
					},
				},
			},
		},
	}
	got, ok := Get(m, "hooks/PreToolUse/0/hooks/1/command")
	if !ok || got != "echo second" {
		t.Errorf("Get nested = %v, %v; want echo second, true", got, ok)
	}
	if _, ok := Get(m, "hooks/PreToolUse/9/matcher"); ok {
		t.Error("out-of-range index should return false")
	}
}

func TestDeleteSplicesArrayElement(t *testing.T) {
	m := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{"matcher": "Bash", "hooks": []any{
					map[string]any{"command": "first"},
					map[string]any{"command": "second"},
					map[string]any{"command": "third"},
				}},
			},
		},
	}
	if !Delete(m, "hooks/PreToolUse/0/hooks/1") {
		t.Fatal("Delete should report success")
	}
	got, _ := Get(m, "hooks/PreToolUse/0/hooks")
	arr, ok := got.([]any)
	if !ok {
		t.Fatalf("hooks array shape lost: %T", got)
	}
	if len(arr) != 2 {
		t.Fatalf("array length after splice = %d, want 2", len(arr))
	}
	commands := []string{
		arr[0].(map[string]any)["command"].(string),
		arr[1].(map[string]any)["command"].(string),
	}
	want := []string{"first", "third"}
	if !reflect.DeepEqual(commands, want) {
		t.Errorf("commands after splice = %v, want %v", commands, want)
	}
}

func TestDeleteEntireArrayElement(t *testing.T) {
	m := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{"matcher": "Bash"},
				map[string]any{"matcher": "Read"},
			},
		},
	}
	if !Delete(m, "hooks/PreToolUse/0") {
		t.Fatal("Delete should succeed on top-level array index")
	}
	arr, _ := Get(m, "hooks/PreToolUse")
	if len(arr.([]any)) != 1 {
		t.Errorf("after deleting index 0, array len = %d, want 1", len(arr.([]any)))
	}
	if Delete(m, "hooks/PreToolUse/9") {
		t.Error("out-of-range delete should fail")
	}
}

func TestSetArrayIndexReplace(t *testing.T) {
	m := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{"matcher": "Bash", "hooks": []any{
					map[string]any{"command": "first"},
					map[string]any{"command": "second"},
				}},
			},
		},
	}
	Set(m, "hooks/PreToolUse/0/hooks/1", map[string]any{"command": "replaced"})
	got, _ := Get(m, "hooks/PreToolUse/0/hooks/1/command")
	if got != "replaced" {
		t.Errorf("Set replace at idx 1 = %v, want replaced", got)
	}
	got2, _ := Get(m, "hooks/PreToolUse/0/hooks/0/command")
	if got2 != "first" {
		t.Errorf("sibling at idx 0 disturbed: %v", got2)
	}
}

func TestSetArrayAppendAtLen(t *testing.T) {
	m := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{"matcher": "Bash"},
			},
		},
	}
	Set(m, "hooks/PreToolUse/1", map[string]any{"matcher": "Read"})
	arr, _ := Get(m, "hooks/PreToolUse")
	asSlice, ok := arr.([]any)
	if !ok || len(asSlice) != 2 {
		t.Fatalf("after append len = %d, want 2 (got %T)", len(asSlice), arr)
	}
	if asSlice[1].(map[string]any)["matcher"] != "Read" {
		t.Errorf("appended element wrong: %v", asSlice[1])
	}
}

func TestSetArrayOutOfRangeNoOp(t *testing.T) {
	m := map[string]any{"a": []any{"x"}}
	Set(m, "a/9", "noop")
	if got, _ := Get(m, "a/9"); got != nil {
		t.Errorf("out-of-range Set should be no-op, got %v", got)
	}
	if got, _ := Get(m, "a/0"); got != "x" {
		t.Errorf("existing element disturbed: %v", got)
	}
}

func TestAppendCreatesArrayWhenMissing(t *testing.T) {
	m := map[string]any{}
	if !Append(m, "hooks/PreToolUse", map[string]any{"matcher": "Bash"}) {
		t.Fatal("Append should report success")
	}
	arr, _ := Get(m, "hooks/PreToolUse")
	if len(arr.([]any)) != 1 {
		t.Errorf("array len after Append = %d, want 1", len(arr.([]any)))
	}
}

func TestAppendExtendsExistingArray(t *testing.T) {
	m := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{map[string]any{"matcher": "Bash"}},
		},
	}
	if !Append(m, "hooks/PreToolUse", map[string]any{"matcher": "Read"}) {
		t.Fatal("Append should succeed on existing array")
	}
	arr, _ := Get(m, "hooks/PreToolUse")
	if len(arr.([]any)) != 2 {
		t.Errorf("len after Append = %d, want 2", len(arr.([]any)))
	}
}

func TestAppendRefusesNonSlice(t *testing.T) {
	m := map[string]any{"hooks": "not-a-slice"}
	if Append(m, "hooks", map[string]any{}) {
		t.Error("Append onto a non-slice should fail")
	}
	if m["hooks"] != "not-a-slice" {
		t.Errorf("non-slice value mutated to %v", m["hooks"])
	}
}

func TestDeleteMapKeyUnchanged(t *testing.T) {
	m := map[string]any{"a": "b", "c": "d"}
	if !Delete(m, "a") {
		t.Fatal("Delete leaf map key should succeed")
	}
	if _, ok := m["a"]; ok {
		t.Error("a still present")
	}
	if m["c"] != "d" {
		t.Error("sibling key c was disturbed")
	}
}

// PRI-78: Claude per-project MCP entries are keyed by absolute path
// — "/Users/me/Projects/x" — whose embedded slashes used to collide
// with the slash separator in SplitKey. JoinKey escapes each segment
// and SplitKey decodes it, so paths round-trip without leaking into
// the separator. Verified end-to-end: build a key with a path
// segment, walk the resulting nested map with Get, retrieve the
// expected value.
func TestJoinKeySplitKeyRoundTripPathSegment(t *testing.T) {
	abs := "/Users/me/Projects/x"
	key := JoinKey("projects", abs, "mcpServers", "linear")
	parts := SplitKey(key)
	want := []string{"projects", abs, "mcpServers", "linear"}
	if len(parts) != len(want) {
		t.Fatalf("SplitKey len=%d; want %d (parts=%q)", len(parts), len(want), parts)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Errorf("part %d = %q; want %q", i, parts[i], want[i])
		}
	}
}

func TestEscapeKeySegmentRoundTrip(t *testing.T) {
	cases := []string{
		"plain",
		"/Users/me/x",
		"with~tilde",
		"/has~/both",
		"~01-tricky",
	}
	for _, c := range cases {
		if got := UnescapeKeySegment(EscapeKeySegment(c)); got != c {
			t.Errorf("round-trip %q → %q", c, got)
		}
	}
}

func TestGetWithEscapedPathKey(t *testing.T) {
	// Simulate Claude .claude.json shape: projects → <abs path> →
	// mcpServers → <name> → entry. JoinKey + Get must walk it
	// transparently.
	abs := "/Users/me/Projects/x"
	tree := map[string]any{
		"projects": map[string]any{
			abs: map[string]any{
				"mcpServers": map[string]any{
					"linear": map[string]any{"command": "npx"},
				},
			},
		},
	}
	key := JoinKey("projects", abs, "mcpServers", "linear")
	got, ok := Get(tree, key)
	if !ok {
		t.Fatalf("Get failed for key %q", key)
	}
	m, ok := got.(map[string]any)
	if !ok || m["command"] != "npx" {
		t.Errorf("Got = %+v; want map with command=npx", got)
	}
}

func TestSplitKeyEmptyAndPlain(t *testing.T) {
	if SplitKey("") != nil {
		t.Error("empty string should split to nil")
	}
	parts := SplitKey("a/b/c")
	if len(parts) != 3 || parts[0] != "a" || parts[2] != "c" {
		t.Errorf("plain split lost: %+v", parts)
	}
}
