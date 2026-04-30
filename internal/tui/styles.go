package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7DCFFF"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#565f89"))

	selectedStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#3d59a1")).
			Foreground(lipgloss.Color("#c0caf5"))

	groupStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#bb9af7"))

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#414868")).
			Padding(0, 1)

	focusedPanelStyle = panelStyle.
				BorderForeground(lipgloss.Color("#7aa2f7"))

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9ece6a"))

	// driftStyle paints (drift)-tagged rows in a warning amber so the
	// user sees out-of-sync items at a glance. Foreground only — keeps
	// selection background highlight intact when the row is focused.
	driftStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#e0af68"))

	// invalidStyle paints rows with frontmatter parse errors in a red
	// that's still readable on the dark background; selection highlight
	// is preserved by overriding only foreground.
	invalidStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f7768e"))

	// warnStyle paints rows that parsed cleanly but are missing
	// recommended fields. Distinct from invalid (red) so the user knows
	// the item still works — just isn't quite right.
	warnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#e0af68"))
)
