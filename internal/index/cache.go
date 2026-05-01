package index

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Cache is the on-disk shape of ~/.lazyagent/index.json. Only one
// snapshot lives in the file at a time — we rewrite it atomically on
// every successful Discover call.
type Cache struct {
	GeneratedAt int64     `json:"generated_at"` // unix seconds of the last full walk
	Roots       []string  `json:"roots"`
	Projects    []Project `json:"projects"`
}

// MarkerMtimes is set on each Project after a Discover() call so the
// cache can detect mutations cheaply on the next launch — see
// MtimesUnchanged. Embedded as a per-project map (marker filename →
// unix-seconds mtime) inside Project itself.

// CachePath returns the canonical location of the index cache. Sits
// next to state.json and config.toml under ~/.lazyagent/.
func CachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lazyagent", "index.json"), nil
}

// LoadCache reads the cache file. A missing file returns a zero Cache
// and no error — that is the normal first-run case. A malformed file
// returns the parse error, but the caller can simply ignore it and
// rebuild from scratch via Discover.
func LoadCache() (Cache, error) {
	path, err := CachePath()
	if err != nil {
		return Cache{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Cache{}, nil
		}
		return Cache{}, err
	}
	var c Cache
	if err := json.Unmarshal(data, &c); err != nil {
		return Cache{}, err
	}
	return c, nil
}

// SaveCache rewrites the cache file atomically. Returns the path so
// callers can log it.
func SaveCache(c Cache) (string, error) {
	path, err := CachePath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return path, err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return path, err
	}
	tmp, err := os.CreateTemp(dir, ".index-*.json")
	if err != nil {
		return path, err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return path, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return path, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return path, err
	}
	return path, nil
}

// IsFresh reports whether the cache was generated within `maxAge` of
// `now`. Callers use it to decide between "use the cache as-is" and
// "kick off a background re-walk". Empty cache is never fresh.
func IsFresh(c Cache, now time.Time, maxAge time.Duration) bool {
	if c.GeneratedAt <= 0 {
		return false
	}
	return now.Sub(time.Unix(c.GeneratedAt, 0)) < maxAge
}

// MtimesUnchanged stats every marker recorded in c.Projects and
// reports whether all mtimes match what the walker saw last time. When
// true, nothing under any known project root has been touched since
// the cache was generated — so a re-walk would just rediscover the
// same set, modulo brand-new project roots created between launches
// (which require an explicit reload to pick up). Returns false on the
// first marker mismatch, missing-marker, or stat error.
//
// PRI-56: lets us amortise a cold $HOME walk over many launches when
// nothing has changed, while still re-walking promptly when the user
// edits any of their tool config dirs.
func MtimesUnchanged(c Cache) bool {
	if len(c.Projects) == 0 {
		return false
	}
	for _, p := range c.Projects {
		if len(p.MarkerMtimes) == 0 {
			// Cache predates the mtime tracking added in PRI-56 — treat
			// as stale so the next launch triggers a re-walk that
			// populates the new field.
			return false
		}
		for marker, recorded := range p.MarkerMtimes {
			info, err := os.Stat(filepath.Join(p.Path, marker))
			if err != nil {
				return false
			}
			if info.ModTime().Unix() != recorded {
				return false
			}
		}
	}
	return true
}
