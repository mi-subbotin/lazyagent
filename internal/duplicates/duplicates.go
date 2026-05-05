// Package duplicates groups model.Items that look like the "same thing"
// across (origin, scope) cells.
//
// Two passes run, in order. An item ends up in the FIRST group it
// qualifies for and never both:
//
//  1. Same-name pass: items sharing (Kind, Name) across different
//     (Origin, Scope) cells. Pairs that resolve to the same library
//     directory are skipped — canonical-vs-projection is by design,
//     not a duplicate.
//
//  2. Same-content pass: items with the same sha256 of their normalised
//     body + sorted-frontmatter (markdown) or sorted-key JSON
//     (StorageEntry).
//
// KindSession and KindMemory are skipped: sessions are inherently
// per-conversation and memory files are singletons per scope.
package duplicates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/parse"
	"github.com/mi-subbotin/lazyagent/internal/store"
)

// DupGroup is a set of items the detector considers duplicates of one
// another. By construction len(Items) >= 2.
type DupGroup struct {
	// Key uniquely identifies the group across a single Find call.
	// Format:
	//   name:<kind>:<name>            — same-name pass
	//   body:<kind>:<short-hash>      — same-content pass
	Key   string
	Items []model.Item
}

// Find groups items by shape. The returned slice is stable-ordered by
// Key. Items not part of any duplicate group are not represented.
func Find(items []model.Item) []DupGroup {
	var groups []DupGroup
	used := make(map[int]bool, len(items))

	type idx struct {
		kind model.Kind
		name string
	}

	// Pass 1: same-name across (Origin, Scope) cells.
	nameBuckets := map[idx][]int{}
	for i, it := range items {
		if !eligible(it) {
			continue
		}
		nameBuckets[idx{kind: it.Kind, name: it.Name}] = append(nameBuckets[idx{kind: it.Kind, name: it.Name}], i)
	}
	keys := make([]idx, 0, len(nameBuckets))
	for k := range nameBuckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].kind != keys[b].kind {
			return keys[a].kind < keys[b].kind
		}
		return keys[a].name < keys[b].name
	})

	for _, k := range keys {
		idxs := nameBuckets[k]
		members := filterRealDuplicates(items, idxs)
		if len(members) < 2 {
			continue
		}
		grp := DupGroup{
			Key:   fmt.Sprintf("name:%s:%s", k.kind, k.name),
			Items: make([]model.Item, 0, len(members)),
		}
		for _, i := range members {
			grp.Items = append(grp.Items, items[i])
			used[i] = true
		}
		groups = append(groups, grp)
	}

	// Pass 2: same-content among items not yet grouped.
	type bodyKey struct {
		kind model.Kind
		hash string
	}
	bodyBuckets := map[bodyKey][]int{}
	for i, it := range items {
		if used[i] || !eligible(it) {
			continue
		}
		h := hashItem(it)
		if h == "" {
			continue
		}
		bk := bodyKey{kind: it.Kind, hash: h}
		bodyBuckets[bk] = append(bodyBuckets[bk], i)
	}
	bkeys := make([]bodyKey, 0, len(bodyBuckets))
	for k := range bodyBuckets {
		bkeys = append(bkeys, k)
	}
	sort.Slice(bkeys, func(a, b int) bool {
		if bkeys[a].kind != bkeys[b].kind {
			return bkeys[a].kind < bkeys[b].kind
		}
		return bkeys[a].hash < bkeys[b].hash
	})
	for _, k := range bkeys {
		idxs := bodyBuckets[k]
		if len(idxs) < 2 {
			continue
		}
		short := k.hash
		if len(short) > 12 {
			short = short[:12]
		}
		grp := DupGroup{
			Key:   fmt.Sprintf("body:%s:%s", k.kind, short),
			Items: make([]model.Item, 0, len(idxs)),
		}
		for _, i := range idxs {
			grp.Items = append(grp.Items, items[i])
		}
		groups = append(groups, grp)
	}

	return groups
}

// eligible reports whether an item kind participates in duplicate
// detection. Sessions and memory files are skipped by design.
func eligible(it model.Item) bool {
	switch it.Kind {
	case model.KindSession, model.KindMemory:
		return false
	}
	return true
}

// filterRealDuplicates drops canonical-vs-projection pairs from a same-name
// bucket. Two items both Shared and resolving to the same library dir
// are by-design projections of one canonical entry, not duplicates.
//
// Returns indexes into `items`. When more than one library dir is
// represented across the bucket, every item is preserved — a conflict
// across canonicals is exactly the kind of duplication this catches.
func filterRealDuplicates(items []model.Item, idxs []int) []int {
	if len(idxs) < 2 {
		return idxs
	}
	canonicals := make(map[string]int, len(idxs))
	allShared := true
	for _, i := range idxs {
		it := items[i]
		if !it.Shared {
			allShared = false
		}
		if it.Path != "" {
			c := store.CanonicalItemDir(it.Path)
			if c != "" {
				canonicals[c]++
			}
		}
	}
	if allShared && len(canonicals) == 1 {
		// Every item points at the same canonical; not a duplicate.
		return nil
	}
	return idxs
}

// hashItem returns a hex-encoded sha256 over the item's normalised
// shape. Empty string when no meaningful payload is available.
//
// For markdown items (Skill / Agent / Prompt / Memory) the input is
// `sortedFrontmatter + "\n" + normalisedBody`. For StorageEntry items
// the input is the entry's JSON value with sorted keys (RawJSON, since
// json.Marshal of a parsed map sorts keys deterministically).
func hashItem(it model.Item) string {
	var payload string
	if it.Storage == model.StorageEntry {
		payload = normaliseEntry(it)
	} else {
		payload = normaliseMarkdown(it.Body)
	}
	if payload == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// normaliseMarkdown produces a deterministic representation of a
// markdown body: `key: value` frontmatter lines sorted alphabetically,
// followed by the body with trailing whitespace stripped per line and
// no trailing blank lines.
func normaliseMarkdown(src string) string {
	if src == "" {
		return ""
	}
	fm := parse.Parse(src)
	keys := make([]string, 0, len(fm.Fields))
	for k := range fm.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(fm.Fields[k])
		b.WriteByte('\n')
	}
	b.WriteString(normaliseBody(fm.Body))
	return b.String()
}

// normaliseBody strips trailing whitespace per line and drops trailing
// blank lines.
func normaliseBody(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t\r")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// normaliseEntry produces a deterministic JSON representation of a
// StorageEntry's value. Falls back to RawTOML or Body when RawJSON is
// missing.
func normaliseEntry(it model.Item) string {
	src := it.RawJSON
	if src == "" {
		src = it.Body
	}
	if src == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(src), &v); err != nil {
		// Fall back to the trimmed source so identical raw inputs still
		// hash equally even when neither side is parseable.
		return strings.TrimSpace(src)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return strings.TrimSpace(src)
	}
	return string(out)
}
