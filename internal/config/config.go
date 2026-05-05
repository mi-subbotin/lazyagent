// Package config defines the on-disk schema for ~/.lazyagent/config.toml,
// loads it with sensible defaults baked in, and writes it back atomically.
//
// A missing file is the expected first-run state — Load returns the full
// default Config with no error. Partial files are layered over defaults,
// so users only need to put the keys they care about overriding.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the on-disk schema. Field tags map 1:1 to TOML keys; any
// section absent from the file falls back to Default().
type Config struct {
	Search  SearchConfig  `toml:"search"`
	UI      UIConfig      `toml:"ui"`
	Tools   ToolsConfig   `toml:"tools"`
	Install InstallConfig `toml:"install"`
	Updates UpdatesConfig `toml:"updates"`
	Logging LoggingConfig `toml:"logging"`
	Backup  BackupConfig  `toml:"backup"`
	Usage   UsageConfig   `toml:"usage"`
	// Guardrails (PRI-67) — thresholds and disabled list for the
	// SessionStart hook-driven rules.
	Guardrails GuardrailsConfig `toml:"guardrails"`
}

// SearchConfig — global indexer roots and exclusions (PRI-4 / PRI-10).
type SearchConfig struct {
	Roots          []string `toml:"roots"`
	IgnorePaths    []string `toml:"ignore_paths"`
	FollowSymlinks bool     `toml:"follow_symlinks"`
}

// UIConfig — TUI defaults.
type UIConfig struct {
	Theme         string `toml:"theme"`
	DefaultMode   string `toml:"default_mode"`
	DisplayMode   string `toml:"display_mode"`
	DefaultOrigin string `toml:"default_origin"`
}

// ToolsConfig — which adapters to load.
type ToolsConfig struct {
	Claude bool `toml:"claude"`
	Codex  bool `toml:"codex"`
	Gemini bool `toml:"gemini"`
	Shared bool `toml:"shared"`
}

// InstallConfig — github-install cache (PRI-3).
type InstallConfig struct {
	CacheDir    string `toml:"cache_dir"`
	GcAfterDays int    `toml:"gc_after_days"`
}

// UpdatesConfig — periodic version check (PRI-19).
type UpdatesConfig struct {
	CheckIntervalDays int  `toml:"check_interval_days"`
	Notify            bool `toml:"notify"`
}

// LoggingConfig — slog target and verbosity (PRI-17).
type LoggingConfig struct {
	Level  string `toml:"level"`
	File   string `toml:"file"`
	Format string `toml:"format"`
}

// BackupConfig — automatic snapshot retention (PRI-92). KeepLast caps
// how many snapshots Prune leaves on disk after a destructive action;
// values <= 0 disable pruning.
type BackupConfig struct {
	KeepLast int `toml:"keep_last"`
}

// GuardrailsConfig — thresholds for the PRI-67 guardrails. Disabled
// lists guardrail names that should be skipped even when explicitly
// installed; install/uninstall remains the primary on/off switch but
// this lets a user kill an installed rule without rewriting hooks.
type GuardrailsConfig struct {
	TooManySkillsThreshold int      `toml:"too_many_skills_threshold"`
	MemoryBloatBytes       int      `toml:"memory_bloat_bytes"`
	Disabled               []string `toml:"disabled"`
}

// UsageConfig — session-log scan thresholds (PRI-95). UnusedDays is
// the cutoff for the "(unused Nd)" badge: an item is flagged when its
// LastSeen is older than this many days. Zero or negative disables the
// badge.
type UsageConfig struct {
	UnusedDays int `toml:"unused_days"`
}

// Default returns a fully-populated Config with the baked-in defaults.
// Modifying the returned value is safe — every call yields a fresh struct.
func Default() *Config {
	return &Config{
		Search: SearchConfig{
			Roots:          []string{"$HOME"},
			IgnorePaths:    []string{},
			FollowSymlinks: false,
		},
		UI: UIConfig{
			Theme:         "tokyonight",
			DefaultMode:   "cwd",
			DisplayMode:   "origin",
			DefaultOrigin: "all",
		},
		Tools: ToolsConfig{
			Claude: true,
			Codex:  true,
			Gemini: true,
			Shared: false,
		},
		Install: InstallConfig{
			CacheDir:    "~/.lazyagent/cache",
			GcAfterDays: 30,
		},
		Updates: UpdatesConfig{
			CheckIntervalDays: 7,
			Notify:            true,
		},
		Logging: LoggingConfig{
			Level:  "warn",
			File:   "~/.lazyagent/logs/lazyagent.log",
			Format: "text",
		},
		Backup: BackupConfig{
			KeepLast: 50,
		},
		Usage: UsageConfig{
			UnusedDays: 30,
		},
		Guardrails: GuardrailsConfig{
			TooManySkillsThreshold: 100,
			MemoryBloatBytes:       8192,
		},
	}
}

// DefaultPath returns the standard config path under $HOME/.lazyagent/.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lazyagent", "config.toml"), nil
}

// LoadReport carries non-fatal diagnostics from a Load operation.
// Callers typically forward these to the logger as warnings.
type LoadReport struct {
	// Path is the resolved file path that was actually read. Empty when
	// the file did not exist and defaults were returned.
	Path string
	// UnknownKeys lists TOML keys present in the file but not part of
	// the schema. Useful for catching typos in user config.
	UnknownKeys []string
	// ValidationErrors lists enum / range violations that were
	// auto-corrected to defaults during Load.
	ValidationErrors []string
}

// Load reads path, layers it over Default(), validates enum fields, and
// returns the resulting Config plus a LoadReport. A missing file yields
// Default() with an empty report and a nil error — that is the normal
// first-run case, not a failure.
func Load(path string) (*Config, *LoadReport, error) {
	cfg := Default()
	report := &LoadReport{}
	meta, err := toml.DecodeFile(path, cfg)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, report, nil
	}
	if err != nil {
		return nil, report, fmt.Errorf("read %s: %w", path, err)
	}
	report.Path = path
	for _, k := range meta.Undecoded() {
		report.UnknownKeys = append(report.UnknownKeys, k.String())
	}
	report.ValidationErrors = validate(cfg)
	return cfg, report, nil
}

// Save writes cfg to path atomically (temp file + rename). Creates the
// parent directory if needed.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.*.toml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	enc := toml.NewEncoder(tmp)
	if err := enc.Encode(cfg); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// ExpandPath replaces a leading "~" or "$HOME" in s with the current
// user's home directory. Use it right before path-using operations,
// not at load time, so Save round-trips the original tokens.
func ExpandPath(s string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return s
	}
	switch s {
	case "~", "$HOME":
		return home
	}
	if strings.HasPrefix(s, "~/") {
		return filepath.Join(home, s[2:])
	}
	if strings.HasPrefix(s, "$HOME/") {
		return filepath.Join(home, s[len("$HOME/"):])
	}
	return s
}
