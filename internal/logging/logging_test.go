package logging

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/config"
)

// withTempLogFile returns a config pointed at a fresh log path inside a
// per-test temp dir, plus the resolved path itself.
func withTempLogFile(t *testing.T) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "lazyagent.log")
	cfg := config.Default()
	cfg.Logging.File = path
	cfg.Logging.Level = "debug"
	cfg.Logging.Format = "text"
	return cfg, path
}

func TestInit_CreatesFileAndDefaultLogger(t *testing.T) {
	cfg, path := withTempLogFile(t)
	resolved, closer, err := Init(cfg, Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer closer.Close()
	if resolved != path {
		t.Errorf("resolved path = %q, want %q", resolved, path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("log file not created: %v", err)
	}
	slog.Warn("hello-from-test", "key", "value")
	if err := closer.Close(); err != nil {
		t.Fatalf("closer.Close: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "hello-from-test") {
		t.Errorf("log file does not contain test message; got %q", string(body))
	}
}

func TestInit_LevelFiltering(t *testing.T) {
	cfg, path := withTempLogFile(t)
	cfg.Logging.Level = "warn"
	_, closer, err := Init(cfg, Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	slog.Debug("should-be-filtered")
	slog.Info("should-also-be-filtered")
	slog.Warn("should-pass")
	closer.Close()
	body, _ := os.ReadFile(path)
	s := string(body)
	if strings.Contains(s, "should-be-filtered") {
		t.Errorf("debug message leaked at warn level; log: %q", s)
	}
	if strings.Contains(s, "should-also-be-filtered") {
		t.Errorf("info message leaked at warn level; log: %q", s)
	}
	if !strings.Contains(s, "should-pass") {
		t.Errorf("warn message missing from log; log: %q", s)
	}
}

func TestInit_FormatJSON(t *testing.T) {
	cfg, path := withTempLogFile(t)
	cfg.Logging.Format = "json"
	_, closer, err := Init(cfg, Options{})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	slog.Warn("json-event", "tool", "claude", "count", 3)
	closer.Close()
	body, _ := os.ReadFile(path)
	line := strings.TrimSpace(string(body))
	if line == "" {
		t.Fatal("empty log file")
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("log line is not valid JSON: %v\nline: %s", err, line)
	}
	if rec["msg"] != "json-event" {
		t.Errorf("rec.msg = %v, want json-event", rec["msg"])
	}
	if rec["tool"] != "claude" {
		t.Errorf("rec.tool = %v, want claude", rec["tool"])
	}
}

func TestInit_OverrideBeatsConfig(t *testing.T) {
	cfg, path := withTempLogFile(t)
	cfg.Logging.Level = "error"
	cfg.Logging.Format = "text"
	_, closer, err := Init(cfg, Options{
		LevelOverride:  "debug",
		FormatOverride: "json",
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	slog.Debug("debug-via-override")
	closer.Close()
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "debug-via-override") {
		t.Errorf("override level=debug did not let debug message through; log: %q", string(body))
	}
	if !strings.Contains(string(body), `"msg"`) {
		t.Errorf("override format=json did not produce JSON output; log: %q", string(body))
	}
}

func TestPickLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{" warn ", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelWarn},
		{"loud", slog.LevelWarn},
	}
	for _, c := range cases {
		if got := pickLevel(c.in); got != c.want {
			t.Errorf("pickLevel(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
