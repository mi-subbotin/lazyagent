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
