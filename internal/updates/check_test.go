package updates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mi-subbotin/lazyagent/internal/state"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.0", "v0.2.0", true},
		{"0.1.0", "0.1.1", true},
		{"v1.2.3", "v1.2.3", false},
		{"v1.2.3", "v1.2.2", false},
		{"dev", "v0.1.0", true},     // bare-build always wants the banner
		{"unknown", "v0.0.1", true}, // ditto
		{"v0.1.0", "garbage", false},
		{"v1.0.0-beta", "v1.0.0", true},
		{"v1.0.0", "v1.0.0-beta", false},
	}
	for _, tc := range cases {
		got := IsNewer(tc.current, tc.latest)
		if got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestShouldCheck(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		s        state.State
		interval int
		want     bool
	}{
		{"zero interval disables", state.State{LastUpdateCheckAt: now.Unix()}, 0, false},
		{"never checked", state.State{}, 7, true},
		{"checked yesterday under 7d", state.State{LastUpdateCheckAt: now.Add(-24 * time.Hour).Unix()}, 7, false},
		{"checked 7 days ago", state.State{LastUpdateCheckAt: now.Add(-7 * 24 * time.Hour).Unix()}, 7, true},
		{"checked 8 days ago", state.State{LastUpdateCheckAt: now.Add(-8 * 24 * time.Hour).Unix()}, 7, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldCheck(tc.s, tc.interval, now); got != tc.want {
				t.Errorf("ShouldCheck = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFetchLatestSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/releases/latest" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"v1.2.3","html_url":"https://example.com/rel","published_at":"2026-04-30T12:00:00Z","draft":false,"prerelease":false}`))
	}))
	defer srv.Close()
	client := srv.Client()
	client.Transport = &rewriteHost{base: srv.URL, inner: client.Transport}

	ctx, cancel := context.WithTimeout(WithClient(context.Background(), client), 3*time.Second)
	defer cancel()
	rel, err := FetchLatest(ctx, "owner", "repo")
	if err != nil {
		t.Fatalf("FetchLatest: %v", err)
	}
	if rel.Version != "v1.2.3" {
		t.Errorf("Version = %q, want v1.2.3", rel.Version)
	}
	if rel.URL != "https://example.com/rel" {
		t.Errorf("URL = %q", rel.URL)
	}
}

func TestFetchLatestNoReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer srv.Close()
	client := srv.Client()
	client.Transport = &rewriteHost{base: srv.URL, inner: client.Transport}

	_, err := FetchLatest(WithClient(context.Background(), client), "owner", "repo")
	if err == nil || err != ErrNoReleases {
		t.Errorf("err = %v, want ErrNoReleases", err)
	}
}

func TestFetchLatestPrereleaseFiltered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v0.2.0-beta","prerelease":true}`))
	}))
	defer srv.Close()
	client := srv.Client()
	client.Transport = &rewriteHost{base: srv.URL, inner: client.Transport}

	_, err := FetchLatest(WithClient(context.Background(), client), "owner", "repo")
	if err != ErrNoReleases {
		t.Errorf("err = %v, want ErrNoReleases", err)
	}
}

func TestRecordCheckPersists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC)
	s := state.State{}
	s, err := RecordCheck(s, "v0.1.0", now)
	if err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if s.LastUpdateCheckAt != now.Unix() {
		t.Errorf("LastUpdateCheckAt not stamped")
	}
	if s.LatestKnownVersion != "v0.1.0" {
		t.Errorf("LatestKnownVersion = %q", s.LatestKnownVersion)
	}
	loaded, err := state.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.LastUpdateCheckAt != now.Unix() {
		t.Errorf("save did not round-trip LastUpdateCheckAt")
	}
}

func TestDismissBanner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Date(2026, 4, 30, 9, 0, 0, 0, time.UTC)
	s := state.State{}
	s, err := DismissBannerForToday(s, "v0.2.0", now)
	if err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if !IsBannerDismissed(s, "v0.2.0", now) {
		t.Error("expected dismissal to take effect today")
	}
	tomorrow := now.Add(24 * time.Hour)
	if IsBannerDismissed(s, "v0.2.0", tomorrow) {
		t.Error("dismissal must not carry into next day")
	}
	if IsBannerDismissed(s, "v0.3.0", now) {
		t.Error("dismissal is per-version — newer release must surface")
	}
}

// rewriteHost rewrites api.github.com requests onto the test server.
// FetchLatest hardcodes the api.github.com host; this transport is the
// least invasive way to redirect without exposing the URL builder in
// the public API.
type rewriteHost struct {
	base  string
	inner http.RoundTripper
}

func (r *rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "api.github.com" {
		newURL := r.base + req.URL.Path
		newReq, err := http.NewRequestWithContext(req.Context(), req.Method, newURL, req.Body)
		if err != nil {
			return nil, err
		}
		newReq.Header = req.Header.Clone()
		req = newReq
	}
	inner := r.inner
	if inner == nil {
		inner = http.DefaultTransport
	}
	return inner.RoundTrip(req)
}
