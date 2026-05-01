// Package updates polls the GitHub releases API for a new lazyagent
// version and surfaces an "↑ vX.Y.Z available" banner in the TUI.
//
// Design (PRI-19):
//   - Source of truth is github.com/<owner>/<repo>/releases/latest, not
//     the homebrew tap. The tap lags GoReleaser by minutes, and users
//     installed via `go install` never see it at all.
//   - Cache lives in ~/.lazyagent/state.json (LastUpdateCheckAt +
//     LatestKnownVersion) — separate from the canonical store so a
//     reset of the user's UI prefs cannot pin them to a stale version.
//   - The check runs in a goroutine after TUI init so a slow GitHub
//     never blocks startup. Failures are silent (logged via slog) —
//     telling a user their update check failed is louder than the
//     update banner itself, and they almost certainly do not care.
//   - Comparison goes through golang.org/x/mod/semver so pre-releases
//     and build metadata sort correctly. The "v" prefix is normalised
//     so "v1.2.3" and "1.2.3" compare equal.
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/mi-subbotin/lazyagent/internal/state"
)

// DefaultRepo is the canonical lazyagent repo. Centralised here so the
// goroutine in main.go and any future CLI subcommand share one constant.
const (
	DefaultOwner = "mi-subbotin"
	DefaultRepo  = "lazyagent"
)

// Release is the trimmed-down view of the GitHub releases payload that
// the banner needs.
type Release struct {
	Version     string    // "v1.2.3"
	URL         string    // html_url, for the banner
	PublishedAt time.Time // when the tag was cut
}

// FetchLatest hits api.github.com and parses the /releases/latest
// payload. The 3-second timeout matches the issue's design note —
// users on slow links see no banner this run, and we try again next
// startup. The function is exported for tests to swap in a custom
// transport via ctx-bound http.Client (see WithClient).
func FetchLatest(ctx context.Context, owner, repo string) (Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "lazyagent-update-check")
	client := clientFromContext(ctx)
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// No releases yet (typical on a fresh repo) — treat as a
		// non-error so the caller does not log a scary message.
		return Release{}, ErrNoReleases
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Release{}, fmt.Errorf("github releases: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var payload struct {
		TagName     string    `json:"tag_name"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return Release{}, fmt.Errorf("decode github releases: %w", err)
	}
	if payload.Draft || payload.Prerelease {
		// /releases/latest already filters drafts and prereleases, but
		// be defensive — a future GitHub change should not silently
		// nag users to install a beta.
		return Release{}, ErrNoReleases
	}
	v := normalizeTag(payload.TagName)
	if v == "" || !semver.IsValid(v) {
		return Release{}, fmt.Errorf("github releases: unrecognised tag %q", payload.TagName)
	}
	return Release{
		Version:     v,
		URL:         payload.HTMLURL,
		PublishedAt: payload.PublishedAt,
	}, nil
}

// ErrNoReleases is returned when the repo has no published, non-draft,
// non-prerelease releases. Callers treat it as "nothing to compare
// against" rather than a real error.
var ErrNoReleases = errors.New("no published releases")

// IsNewer reports whether latest > current under SemVer ordering.
// Both inputs may be bare ("1.2.3") or v-prefixed ("v1.2.3"); empty
// strings sort last so a missing current always shows the banner.
// "dev" / "unknown" / anything non-semver in current also counts as
// older — so go-run / non-release builds always see the upgrade hint.
func IsNewer(current, latest string) bool {
	c := normalizeTag(current)
	l := normalizeTag(latest)
	if !semver.IsValid(l) {
		return false
	}
	if !semver.IsValid(c) {
		return true
	}
	return semver.Compare(l, c) > 0
}

// ShouldCheck reports whether the LastUpdateCheckAt cache is stale and
// a fresh poll should run. intervalDays = 0 disables the check entirely
// (callers map this from config.Updates.Notify = false). A negative or
// missing LastUpdateCheckAt always returns true so first-run users see
// the banner without needing to wait a week.
func ShouldCheck(s state.State, intervalDays int, now time.Time) bool {
	if intervalDays <= 0 {
		return false
	}
	if s.LastUpdateCheckAt <= 0 {
		return true
	}
	last := time.Unix(s.LastUpdateCheckAt, 0)
	return now.Sub(last) >= time.Duration(intervalDays)*24*time.Hour
}

// RecordCheck stamps the cache with the result of a successful poll
// and persists it. Any save error is returned so the caller can log it,
// but the in-memory state is mutated either way — the worst case is the
// banner reappears next launch, never a crash.
func RecordCheck(s state.State, latest string, now time.Time) (state.State, error) {
	s.LastUpdateCheckAt = now.Unix()
	if v := normalizeTag(latest); v != "" {
		s.LatestKnownVersion = v
	}
	return s, state.Save(s)
}

// DismissBannerForToday writes a per-day suppression keyed by version,
// so a key tap silences the banner until either tomorrow or a newer
// release. The version argument is the one currently being shown.
func DismissBannerForToday(s state.State, version string, now time.Time) (state.State, error) {
	s.UpdateBannerDismissedFor = normalizeTag(version)
	s.UpdateBannerDismissedDate = now.Format("2006-01-02")
	return s, state.Save(s)
}

// IsBannerDismissed reports whether the user already silenced the
// banner for the given version on the current calendar day.
func IsBannerDismissed(s state.State, version string, now time.Time) bool {
	if s.UpdateBannerDismissedFor == "" || s.UpdateBannerDismissedDate == "" {
		return false
	}
	if normalizeTag(s.UpdateBannerDismissedFor) != normalizeTag(version) {
		return false
	}
	return s.UpdateBannerDismissedDate == now.Format("2006-01-02")
}

// normalizeTag returns the input as a semver-friendly "vX.Y.Z" string,
// or empty if the input is obviously not a version (dev / unknown /
// none / blank). Whitespace and a stray leading "release-" prefix get
// stripped so misformatted tags still parse.
func normalizeTag(s string) string {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "", "dev", "unknown", "none", "snapshot":
		return ""
	}
	s = strings.TrimPrefix(s, "release-")
	if !strings.HasPrefix(s, "v") {
		s = "v" + s
	}
	return s
}

// clientKey is the context key for an injected *http.Client. Tests use
// WithClient to plug in a server-backed transport; production code
// falls back to http.DefaultClient with the context-bound timeout.
type clientKey struct{}

// WithClient stashes a custom http.Client in ctx. Test-only — production
// code paths leave the context untouched and get http.DefaultClient.
func WithClient(ctx context.Context, c *http.Client) context.Context {
	return context.WithValue(ctx, clientKey{}, c)
}

func clientFromContext(ctx context.Context) *http.Client {
	if c, ok := ctx.Value(clientKey{}).(*http.Client); ok && c != nil {
		return c
	}
	return http.DefaultClient
}
