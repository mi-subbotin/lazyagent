package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/mi-subbotin/lazyagent/internal/state"
)

// updateBannerStyle paints the "↑ vX.Y.Z available" footer line in the
// same warning amber as drift markers — the user already associates
// that colour with "the canonical version is somewhere else", so we
// reuse it here for "your binary is older than the canonical release".
var updateBannerStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#e0af68"))

// renderUpdateBanner builds the one-line banner shown above the status
// bar when a newer release is available. It's a function rather than a
// styled string so the upgrade hint can vary by install source — brew
// users see "brew upgrade", go-install users see "go install ...@latest"
// and any other origin (manual download, source build) gets a generic
// "see <release URL>" note.
func renderUpdateBanner(version, url, installSource string, w int) string {
	if version == "" {
		return ""
	}
	hint := upgradeHintFor(installSource, url)
	line := fmt.Sprintf("↑ %s available — %s", version, hint)
	return updateBannerStyle.Render(truncRunes(line, w))
}

func upgradeHintFor(installSource, url string) string {
	switch installSource {
	case "brew":
		return "brew upgrade lazyagent"
	case "go-install":
		return "go install github.com/mi-subbotin/lazyagent/cmd/lazyagent@latest"
	}
	if url != "" {
		return url
	}
	return "github.com/mi-subbotin/lazyagent/releases"
}

// isUpdateBannerDismissed mirrors updates.IsBannerDismissed without the
// import cycle: the updates package depends on internal/state, and the
// tui package already pulls in state. Keeping the predicate inline here
// avoids a one-call dependency on internal/updates from the TUI.
func isUpdateBannerDismissed(s state.State, version string, now time.Time) bool {
	if s.UpdateBannerDismissedFor == "" || s.UpdateBannerDismissedDate == "" {
		return false
	}
	if normalizeVersion(s.UpdateBannerDismissedFor) != normalizeVersion(version) {
		return false
	}
	return s.UpdateBannerDismissedDate == now.Format("2006-01-02")
}

func normalizeVersion(s string) string {
	if s == "" {
		return ""
	}
	if s[0] != 'v' {
		return "v" + s
	}
	return s
}
