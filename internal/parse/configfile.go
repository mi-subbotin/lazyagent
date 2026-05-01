// Package parse, configfile.go: round-trip primitives for JSON and TOML
// config files used by Claude (.claude.json, project .mcp.json), Codex
// (config.toml), Gemini (settings.json), and the lazyagent shared store
// manifest. All public functions key into nested maps via slash-joined
// paths ("mcpServers/linear", "profiles/default") so callers don't need
// to know the underlying map[string]any shape.
package parse

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// ConfigFormat identifies the on-disk format of a config file. Read
// returns this so callers can hand it back to Write without re-deriving
// from the path; FormatFromExt is the standalone way to pick a format
// when starting from scratch.
type ConfigFormat int

const (
	FormatJSON ConfigFormat = iota
	FormatTOML
)

// FormatFromExt picks JSON for everything except `*.toml`.
func FormatFromExt(path string) ConfigFormat {
	if strings.HasSuffix(strings.ToLower(path), ".toml") {
		return FormatTOML
	}
	return FormatJSON
}

// Read parses the file at path as JSON or TOML based on its extension.
// An empty file (or one containing only whitespace) yields an empty map
// rather than an error — callers can immediately Set values into it.
// Returns fs.ErrNotExist if the file is missing.
func Read(path string) (map[string]any, ConfigFormat, error) {
	f := FormatFromExt(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, f, err
	}
	out := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return out, f, nil
	}
	switch f {
	case FormatTOML:
		if err := toml.Unmarshal(data, &out); err != nil {
			return nil, f, fmt.Errorf("%s: %w", path, err)
		}
	default:
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, f, fmt.Errorf("%s: %w", path, err)
		}
	}
	return out, f, nil
}

// Write serializes data to path in the requested format. Parent
// directories are created as needed. Writes are atomic: data lands in a
// `<path>.tmp` sibling and is renamed into place on success, so a
// crashed or canceled write never leaves a half-written config behind.
func Write(path string, data map[string]any, f ConfigFormat) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var blob []byte
	switch f {
	case FormatTOML:
		var b bytes.Buffer
		if err := toml.NewEncoder(&b).Encode(data); err != nil {
			return err
		}
		blob = b.Bytes()
	default:
		out, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return err
		}
		blob = append(out, '\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SplitKey splits a slash-joined key path ("mcpServers/linear") into
// component parts. An empty string returns nil.
func SplitKey(k string) []string {
	if k == "" {
		return nil
	}
	return strings.Split(k, "/")
}

// Get walks a nested map[string]any / []any tree and returns the value
// at the slash-joined key path, or (nil, false) if any intermediate
// node is missing, mistyped, or out-of-range. Numeric segments are
// interpreted as array indices when the current node is an []any —
// this lets callers walk into Claude `hooks/<event>/0/0`-shaped paths
// without a custom navigator.
func Get(m map[string]any, keyPath string) (any, bool) {
	parts := SplitKey(keyPath)
	if len(parts) == 0 {
		return nil, false
	}
	var cur any = m
	for i, p := range parts {
		next, ok := step(cur, p)
		if !ok {
			return nil, false
		}
		if i == len(parts)-1 {
			return next, true
		}
		cur = next
	}
	return nil, false
}

// step descends one segment into either a map[string]any or a []any.
// Returns the child value and whether the step succeeded. Used by the
// array-aware Get / Delete walkers.
func step(node any, segment string) (any, bool) {
	switch n := node.(type) {
	case map[string]any:
		v, ok := n[segment]
		return v, ok
	case []any:
		idx, err := strconv.Atoi(segment)
		if err != nil || idx < 0 || idx >= len(n) {
			return nil, false
		}
		return n[idx], true
	}
	return nil, false
}

// Set assigns v at the given key path, creating intermediate maps where
// necessary. Existing non-map values along the path are overwritten.
func Set(m map[string]any, keyPath string, v any) {
	parts := SplitKey(keyPath)
	if len(parts) == 0 {
		return
	}
	cur := m
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = v
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
}

// Delete removes the entry at the given key path. Returns true if a
// value was actually removed. Intermediate maps that become empty are
// left in place — pruning surprises users who expect their `mcpServers`
// key to remain (just empty) after the last server is removed.
//
// Array-aware: a numeric leaf segment under an []any parent splices
// the element out (preserving order). The same applies to intermediate
// segments — the walk descends into arrays when the segment parses as
// an int and the parent is a slice. Used by the hooks Delete path
// where keys look like `hooks/<event>/<idx>/<idx>`.
func Delete(m map[string]any, keyPath string) bool {
	parts := SplitKey(keyPath)
	if len(parts) == 0 {
		return false
	}
	if len(parts) == 1 {
		if _, ok := m[parts[0]]; !ok {
			return false
		}
		delete(m, parts[0])
		return true
	}
	parent, ok := walkToParent(m, parts[:len(parts)-1])
	if !ok {
		return false
	}
	leaf := parts[len(parts)-1]
	switch p := parent.(type) {
	case map[string]any:
		if _, ok := p[leaf]; !ok {
			return false
		}
		delete(p, leaf)
		return true
	case *sliceRef:
		idx, err := strconv.Atoi(leaf)
		if err != nil || idx < 0 || idx >= len(*p.s) {
			return false
		}
		*p.s = append((*p.s)[:idx], (*p.s)[idx+1:]...)
		p.commit()
		return true
	}
	return false
}

// sliceRef wraps a []any in a way that the array-aware walker can
// mutate the slice header in its containing parent. commit() writes
// the new slice header back to the parent that referenced it.
type sliceRef struct {
	s      *[]any
	commit func()
}

// walkToParent descends `parts` one segment at a time and returns the
// node holding the would-be leaf. Maps are returned directly; arrays
// are wrapped in *sliceRef so callers can splice elements without
// losing the reference through the parent map.
func walkToParent(m map[string]any, parts []string) (any, bool) {
	if len(parts) == 0 {
		return m, true
	}
	var cur any = m
	for i, seg := range parts {
		switch n := cur.(type) {
		case map[string]any:
			v, ok := n[seg]
			if !ok {
				return nil, false
			}
			if arr, ok := v.([]any); ok {
				ref := &sliceRef{s: &arr, commit: func() { n[seg] = arr }}
				if i == len(parts)-1 {
					return ref, true
				}
				cur = ref
				continue
			}
			cur = v
		case *sliceRef:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(*n.s) {
				return nil, false
			}
			v := (*n.s)[idx]
			if arr, ok := v.([]any); ok {
				captured := arr
				outer := n
				outerIdx := idx
				ref := &sliceRef{
					s: &captured,
					commit: func() {
						(*outer.s)[outerIdx] = captured
						outer.commit()
					},
				}
				if i == len(parts)-1 {
					return ref, true
				}
				cur = ref
				continue
			}
			cur = v
		default:
			return nil, false
		}
	}
	return cur, true
}

// ReadEntry returns the value at keyPath inside the config file at path
// along with the file's format. Convenience wrapper around Read + Get
// for callers that only care about a single entry.
func ReadEntry(path, keyPath string) (any, ConfigFormat, error) {
	data, f, err := Read(path)
	if err != nil {
		return nil, f, err
	}
	v, ok := Get(data, keyPath)
	if !ok {
		return nil, f, fmt.Errorf("entry %q not found in %s", keyPath, path)
	}
	return v, f, nil
}

// WriteEntry sets keyPath = value inside the config file at path,
// preserving everything else in the file. The file is created (with an
// empty config) if it does not exist; format is inferred from the
// extension. Round-trip caveat: comments in TOML files are lost because
// BurntSushi/toml's encoder cannot emit them — JSON has no such issue.
func WriteEntry(path, keyPath string, value any) error {
	data, f, err := Read(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		data = map[string]any{}
		f = FormatFromExt(path)
	}
	Set(data, keyPath, value)
	return Write(path, data, f)
}

// DeleteEntry removes keyPath from the config file at path. Idempotent:
// if the entry is already absent we succeed silently and rewrite the
// file unchanged. Missing config file returns fs.ErrNotExist — callers
// usually want to treat that as already-deleted.
func DeleteEntry(path, keyPath string) error {
	data, f, err := Read(path)
	if err != nil {
		return err
	}
	Delete(data, keyPath)
	return Write(path, data, f)
}
