// Package lazyagent is the Source adapter for the lazyagent canonical
// store at ~/.lazyagent/store/. Items here always have Scope=Global —
// the store has no project-local concept (per PRI-2 v1).
//
// When the store directory does not exist (i.e. the user has not run
// `lazyagent shared init`), List returns an empty slice without error.
// That keeps the Shared origin invisible until the user opts in.
package lazyagent

import (
	"context"
	"os"

	"github.com/mi-subbotin/lazyagent/internal/model"
	"github.com/mi-subbotin/lazyagent/internal/store"
)

type Source struct{}

func (Source) Name() string { return "lazyagent" }

func (s Source) List(_ context.Context, _ string) ([]model.Item, error) {
	if !store.Initialised() {
		return nil, nil
	}

	groups, err := store.ListItems()
	if err != nil {
		return nil, err
	}

	var out []model.Item
	for kind, entries := range groups {
		for _, e := range entries {
			it, ok := buildItem(kind, e)
			if !ok {
				continue
			}
			out = append(out, it)
		}
	}
	return out, nil
}

// buildItem turns a store.ItemEntry into a model.Item. Body is read
// from the canonical body file inside the item directory; the path
// convention varies by Kind (SKILL.md for skills to match Claude's
// expectation, <kind>.md otherwise). MCP items don't have a single
// body file in v1 — those land in a follow-up.
func buildItem(k model.Kind, e store.ItemEntry) (model.Item, bool) {
	bodyName, supported := bodyFileName(k)
	if !supported {
		return model.Item{}, false
	}
	bodyPath := e.Dir + "/" + bodyName
	data, err := os.ReadFile(bodyPath)
	if err != nil {
		// Item directory exists but the body file is missing — broken
		// store state. Skip silently for now; PRI-18 will surface this
		// as an `(invalid)` badge.
		return model.Item{}, false
	}

	desc := e.Manifest.Name
	if e.Manifest.Version != "" {
		desc += " " + e.Manifest.Version
	}

	return model.Item{
		Origin:      model.OriginShared,
		Kind:        k,
		Scope:       model.ScopeGlobal,
		Name:        e.Manifest.Name,
		Path:        bodyPath,
		Description: desc,
		Body:        string(data),
		Storage:     storageFor(k),
		Shared:      true,
	}, true
}

// bodyFileName delegates to store.CanonicalBodyName so the layout
// has a single owner; this wrapper survives only because the legacy
// helper had a different name in callers.
func bodyFileName(k model.Kind) (string, bool) {
	return store.CanonicalBodyName(k)
}

// storageFor mirrors how the per-tool adapters categorise the same
// Kind: skills are directory-shaped (the SKILL.md sits inside its
// own folder with assets), single-file kinds are StorageFile.
func storageFor(k model.Kind) model.Storage {
	if k == model.KindSkill {
		return model.StorageDir
	}
	return model.StorageFile
}
