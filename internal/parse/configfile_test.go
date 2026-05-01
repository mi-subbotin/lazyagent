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
