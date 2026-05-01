package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// emptyLogoLines is the ASCII banner shown on the empty-state screen.
// Sourced from a `figlet -f slant lazyagent` rendering and trimmed to
// 9 columns of vertical whitespace so it composes well with the hint
// block underneath. PRI-7 will eventually own a richer splash; until
// then this lives next to the empty-state code that needs it.
var emptyLogoLines = []string{
	"  __                                              __ ",
	" / /__ _____ __  _____ ____ ____ ____  ___    __/ /_",
	"/ // _ `/_ // / / / _ `/ _ `/ -_) _ \\/ _/ / /_  __/",
	"\\_/\\_,_//__/\\_, /\\_,_/\\_, /\\__/_//_/_/   \\_\\/_/   ",
	"           /___/     /___/                          ",
}

// emptyHintLines is the action prompt shown below the logo. Kept short
// (3 bullets) so it never wraps even on an 80x24 terminal.
var emptyHintLines = []string{
	"No skills, agents, MCPs, or memory found.",
	"",
	"  • Press ? for help",
	"  • Press i to install from GitHub",
	"  • Or open a project directory and run lazyagent there",
}

var (
	emptyLogoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7DCFFF")).
			Bold(true)

	emptyHintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#c0caf5"))

	emptyHintBulletStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#9ece6a"))
)

// renderEmptyState centers the logo + hint inside an area of size w x h.
// We render plain strings and pad with whitespace rather than calling
// lipgloss.Place so the math stays auditable — Place sometimes inserts
// extra newlines on narrow terminals, and we already cap output to h
// in the View() pipeline.
func renderEmptyState(w, h int) string {
	logoW := 0
	for _, ln := range emptyLogoLines {
		if n := lipgloss.Width(ln); n > logoW {
			logoW = n
		}
	}

	// On terminals too narrow for the logo, drop it entirely and lean
	// on the hint block. Better than ASCII art that wraps mid-letter.
	showLogo := w >= logoW+4

	var lines []string
	if showLogo {
		for _, ln := range emptyLogoLines {
			lines = append(lines, emptyLogoStyle.Render(centerLine(ln, w)))
		}
		lines = append(lines, "")
	}
	for i, ln := range emptyHintLines {
		switch {
		case i == 0:
			lines = append(lines, emptyHintStyle.Render(centerLine(ln, w)))
		case strings.HasPrefix(ln, "  •"):
			// Center the bullet block as a unit so the three lines
			// share a left margin and the eye reads them as a list.
			lines = append(lines, emptyHintBulletStyle.Render(centerLine(ln, w)))
		default:
			lines = append(lines, centerLine(ln, w))
		}
	}

	// Vertically center inside h. If the content is taller than the
	// available rows we just render from the top — losing the bottom
	// hints is worse than truncating the leading whitespace.
	pad := (h - len(lines)) / 2
	if pad < 0 {
		pad = 0
	}
	out := make([]string, 0, h)
	for i := 0; i < pad; i++ {
		out = append(out, "")
	}
	out = append(out, lines...)
	for len(out) < h {
		out = append(out, "")
	}
	if len(out) > h {
		out = out[:h]
	}
	return strings.Join(out, "\n")
}

// centerLine adds left-padding so s sits in the middle of width w.
// Right padding is omitted because the surrounding panel border or
// the joinExactly() call in View() handles trailing whitespace.
func centerLine(s string, w int) string {
	pad := (w - lipgloss.Width(s)) / 2
	if pad <= 0 {
		return s
	}
	return strings.Repeat(" ", pad) + s
}
