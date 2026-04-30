package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mi-subbotin/lazyagent/internal/config"
)

// runLogsSubcommand dispatches `lazyagent logs <verb>`.
//
// Verbs:
//
//	path      print the resolved log file path
//	tail [-n] print the last N lines of the active log file (default 50)
//	clean     remove the active log file plus any rotated siblings
//
// Verbs are intentionally minimal — `lazyagent logs tail -f` is not
// provided because a normal `tail -f $(lazyagent logs path)` does the
// same thing without re-implementing rotation-aware following.
func runLogsSubcommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: lazyagent logs <path|tail|clean>")
	}
	path := resolveLogPath()
	switch args[0] {
	case "path":
		fmt.Println(path)
		return nil
	case "tail":
		return logsTail(args[1:], path)
	case "clean":
		return logsClean(path)
	default:
		return fmt.Errorf("unknown logs subcommand %q (try: path | tail | clean)", args[0])
	}
}

// resolveLogPath reads the log file location from config, falling back
// to the baked-in default. Errors during config load are silenced here
// — the helper is meant to "just print a path" and degrades gracefully.
func resolveLogPath() string {
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return config.ExpandPath(config.Default().Logging.File)
	}
	cfg, _, err := config.Load(cfgPath)
	if err != nil || cfg == nil {
		cfg = config.Default()
	}
	return config.ExpandPath(cfg.Logging.File)
}

func logsTail(args []string, path string) error {
	fs := flag.NewFlagSet("logs tail", flag.ContinueOnError)
	n := fs.Int("n", 50, "number of trailing lines to print")
	if err := fs.Parse(args); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "no log file at %s yet — nothing to tail\n", path)
			return nil
		}
		return err
	}
	defer f.Close()
	lines, err := readLastLines(f, *n)
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}

// readLastLines returns at most n trailing lines from r, in order. Lines
// are read fully (no truncation), which is safe for log files that are
// typically thousands rather than millions of lines.
func readLastLines(r io.Reader, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	buf := make([]string, 0, n)
	for scanner.Scan() {
		buf = append(buf, scanner.Text())
		if len(buf) > n {
			buf = buf[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return buf, nil
}

func logsClean(path string) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	// lumberjack-rotated siblings are named like "lazyagent-2026-04-30T12-00-00.000.log"
	// or "lazyagent-2026-04-30T12-00-00.000.log.gz" — both share the
	// stem (filename without extension).
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == base {
			continue
		}
		if !strings.HasPrefix(name, stem+"-") {
			continue
		}
		if !(strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".log.gz")) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
