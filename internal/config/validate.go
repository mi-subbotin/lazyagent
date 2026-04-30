package config

import "fmt"

var (
	validThemes     = []string{"tokyonight", "catppuccin", "nord"}
	validModes      = []string{"cwd", "global"}
	validDisplays   = []string{"origin", "local-grouped"}
	validOrigins    = []string{"all", "claude", "codex", "gemini"}
	validLogLevels  = []string{"debug", "info", "warn", "error"}
	validLogFormats = []string{"text", "json"}
)

// validate replaces invalid enum values in cfg with their defaults and
// returns one human-readable message per fix-up. Range-only constraints
// (non-negative integers) are handled the same way.
func validate(cfg *Config) []string {
	var msgs []string
	def := Default()

	if !contains(validThemes, cfg.UI.Theme) {
		msgs = append(msgs, fmt.Sprintf("ui.theme=%q invalid (allowed: %v); resetting to %q", cfg.UI.Theme, validThemes, def.UI.Theme))
		cfg.UI.Theme = def.UI.Theme
	}
	if !contains(validModes, cfg.UI.DefaultMode) {
		msgs = append(msgs, fmt.Sprintf("ui.default_mode=%q invalid (allowed: %v); resetting to %q", cfg.UI.DefaultMode, validModes, def.UI.DefaultMode))
		cfg.UI.DefaultMode = def.UI.DefaultMode
	}
	if !contains(validDisplays, cfg.UI.DisplayMode) {
		msgs = append(msgs, fmt.Sprintf("ui.display_mode=%q invalid (allowed: %v); resetting to %q", cfg.UI.DisplayMode, validDisplays, def.UI.DisplayMode))
		cfg.UI.DisplayMode = def.UI.DisplayMode
	}
	if !contains(validOrigins, cfg.UI.DefaultOrigin) {
		msgs = append(msgs, fmt.Sprintf("ui.default_origin=%q invalid (allowed: %v); resetting to %q", cfg.UI.DefaultOrigin, validOrigins, def.UI.DefaultOrigin))
		cfg.UI.DefaultOrigin = def.UI.DefaultOrigin
	}
	if !contains(validLogLevels, cfg.Logging.Level) {
		msgs = append(msgs, fmt.Sprintf("logging.level=%q invalid (allowed: %v); resetting to %q", cfg.Logging.Level, validLogLevels, def.Logging.Level))
		cfg.Logging.Level = def.Logging.Level
	}
	if !contains(validLogFormats, cfg.Logging.Format) {
		msgs = append(msgs, fmt.Sprintf("logging.format=%q invalid (allowed: %v); resetting to %q", cfg.Logging.Format, validLogFormats, def.Logging.Format))
		cfg.Logging.Format = def.Logging.Format
	}
	if cfg.Updates.CheckIntervalDays < 0 {
		msgs = append(msgs, fmt.Sprintf("updates.check_interval_days=%d invalid (must be >= 0); resetting to %d", cfg.Updates.CheckIntervalDays, def.Updates.CheckIntervalDays))
		cfg.Updates.CheckIntervalDays = def.Updates.CheckIntervalDays
	}
	if cfg.Install.GcAfterDays < 0 {
		msgs = append(msgs, fmt.Sprintf("install.gc_after_days=%d invalid (must be >= 0); resetting to %d", cfg.Install.GcAfterDays, def.Install.GcAfterDays))
		cfg.Install.GcAfterDays = def.Install.GcAfterDays
	}

	return msgs
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
