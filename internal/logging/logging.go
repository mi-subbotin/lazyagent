// Package logging configures the global slog handler so the rest of
// the codebase can call slog.Warn / slog.Info / slog.Debug without
// caring about destinations or rotation.
//
// All output goes to a rotating file (defaults to
// ~/.lazyagent/logs/lazyagent.log) — never to stdout/stderr while the
// TUI is running, since the bubbletea altscreen would mangle it.
// Failures during Init fall back to a no-op writer rather than aborting
// boot: the TUI is more important than the logs.
package logging

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	lumberjack "gopkg.in/natefinch/lumberjack.v2"

	"github.com/mi-subbotin/lazyagent/internal/config"
)

// Options carry runtime overrides applied on top of the config — these
// mirror the CLI flags introduced in PRI-17.C so the wiring lives in
// one place.
type Options struct {
	// LevelOverride, if non-empty, replaces cfg.Logging.Level
	// (e.g. "debug" set by -v / --verbose).
	LevelOverride string
	// FileOverride, if non-empty, replaces cfg.Logging.File.
	FileOverride string
	// FormatOverride, if non-empty, replaces cfg.Logging.Format
	// ("text" | "json").
	FormatOverride string
}

// Init configures the global slog logger to write to the file from
// cfg.Logging — with daily rotation via lumberjack — at the level and
// format from cfg or the matching Options override. It returns the
// resolved log path plus an io.Closer that callers should defer to
// flush the rotation buffer cleanly on shutdown.
//
// A Mkdir or Open failure does not return early: the global handler is
// set to a discard writer, the error is returned, and the rest of the
// app keeps running without logging. Logging that breaks boot would be
// worse than no logging.
func Init(cfg *config.Config, opts Options) (path string, closer io.Closer, err error) {
	level := pickLevel(stringOr(opts.LevelOverride, cfg.Logging.Level))
	format := stringOr(opts.FormatOverride, cfg.Logging.Format)
	resolved := config.ExpandPath(stringOr(opts.FileOverride, cfg.Logging.File))

	if mkErr := os.MkdirAll(filepath.Dir(resolved), 0o755); mkErr != nil {
		setDiscardDefault(level, format)
		return resolved, noopCloser{}, fmt.Errorf("create log dir %s: %w", filepath.Dir(resolved), mkErr)
	}

	lj := &lumberjack.Logger{
		Filename:   resolved,
		MaxAge:     7, // keep one week of rotated logs
		MaxBackups: 7, // bound on disk usage even if rotation triggers more often
		LocalTime:  true,
		Compress:   true,
	}
	// Touch the file so callers can stat the path immediately after Init,
	// and surface any permissions error before we hand back the closer.
	if openErr := touch(resolved); openErr != nil {
		setDiscardDefault(level, format)
		return resolved, noopCloser{}, fmt.Errorf("open log file %s: %w", resolved, openErr)
	}
	slog.SetDefault(slog.New(newHandler(lj, level, format)))
	return resolved, lj, nil
}

func newHandler(w io.Writer, level slog.Level, format string) slog.Handler {
	opts := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(format, "json") {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

func setDiscardDefault(level slog.Level, format string) {
	slog.SetDefault(slog.New(newHandler(io.Discard, level, format)))
}

func touch(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// pickLevel maps a config string onto a slog level. Unknown values fall
// back to warn, mirroring the config validator's own default.
func pickLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}

func stringOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ErrNoLogFile is returned by helpers like Tail when the log file does
// not exist yet (typical on a brand-new install before the user has
// done anything that would log a warning).
var ErrNoLogFile = errors.New("log file does not exist yet")

type noopCloser struct{}

func (noopCloser) Close() error { return nil }
