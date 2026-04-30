package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mi-subbotin/lazyagent/internal/install"
	"github.com/mi-subbotin/lazyagent/internal/model"
)

// newUpdateOverlay reuses the install overlay struct in "fetch only"
// mode for the U hotkey: we already know the target (it's the item
// the cursor sits on), so we skip URL input and target picking and
// jump straight from fetch → conflict-style replace prompt.
func newUpdateOverlay(it model.Item) *installOverlay {
	ov := newInstallOverlay()
	ov.phase = phaseInstallFetch
	ov.url = "" // filled by startUpdateForItem before fetch returns
	return ov
}

// startUpdateForItem looks up the item in installed.toml, resolves
// its origin ref to a fresh sha, fetches the new tarball, and once
// done routes back through the standard installAppliedMsg path with
// Overwrite=true. Returns an error if the item wasn't installed via
// `lazyagent install` (no manifest entry).
func startUpdateForItem(it model.Item) (tea.Cmd, error) {
	manifestPath, err := install.DefaultPath()
	if err != nil {
		return nil, err
	}
	manifest, err := install.Load(manifestPath)
	if err != nil {
		return nil, err
	}
	var entry *install.Install
	for i := range manifest.Installs {
		if manifest.Installs[i].TargetPath == it.Path {
			entry = &manifest.Installs[i]
			break
		}
	}
	if entry == nil {
		return nil, errors.New("not installed via `lazyagent install` — nothing to update from")
	}

	originURL := entry.OriginURL
	oldSHA := entry.SHA
	target := install.Target{Origin: entry.TargetOrigin, Scope: entry.TargetScope}
	if entry.TargetScope == "local" {
		target.ProjectDir = filepath.Dir(it.Path)
	}
	cand := install.Candidate{
		Kind:      it.Kind,
		Name:      entry.Name,
		Storage:   it.Storage,
		SourceRel: entry.SourceRel,
	}

	return func() tea.Msg {
		spec, err := install.ParseURL(originURL)
		if err != nil {
			return installAppliedMsg{err: fmt.Errorf("origin url: %w", err)}
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return installAppliedMsg{err: err}
		}
		cacheDir := filepath.Join(home, ".lazyagent", "cache")
		client := install.NewClient(cacheDir)

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		newSHA, err := client.Resolve(ctx, spec)
		if err != nil {
			return installAppliedMsg{err: fmt.Errorf("resolve: %w", err)}
		}
		if newSHA == oldSHA {
			return installAppliedMsg{summary: []string{
				fmt.Sprintf("%s: already at %s — nothing to update", entry.Name, oldSHA[:short(len(oldSHA), 8)]),
			}}
		}
		repoDir, err := client.Fetch(ctx, spec, newSHA)
		if err != nil {
			return installAppliedMsg{err: fmt.Errorf("fetch: %w", err)}
		}

		newEntry, err := install.Apply(repoDir, cand, target, originURL, newSHA, install.ApplyOptions{Overwrite: true})
		if err != nil {
			return installAppliedMsg{err: fmt.Errorf("apply: %w", err)}
		}

		manifest, err := install.Load(manifestPath)
		if err != nil {
			return installAppliedMsg{err: err}
		}
		manifest.Add(newEntry)
		if err := manifest.Save(manifestPath); err != nil {
			return installAppliedMsg{err: fmt.Errorf("save manifest: %w", err)}
		}

		return installAppliedMsg{summary: []string{
			fmt.Sprintf("updated %s", entry.Name),
			fmt.Sprintf("  %s → %s", oldSHA[:short(len(oldSHA), 8)], newSHA[:short(len(newSHA), 8)]),
			fmt.Sprintf("  path: %s", newEntry.TargetPath),
		}}
	}, nil
}
