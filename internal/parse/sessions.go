package parse

import (
	"fmt"
	"strings"
	"time"
)

// SessionPreview shortens a multi-line first-user-message to a single
// row suitable as a tree label. Returns the original (with newlines
// flattened) when shorter than n runes; otherwise truncates and appends
// an ellipsis.
func SessionPreview(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// SessionFriendlyTime formats t as a relative duration ("3m ago",
// "2h ago", "5d ago") for short ranges and a calendar date past 30
// days. Used in session list rows so the user can scan recency at a
// glance without parsing ISO timestamps.
func SessionFriendlyTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

// SessionBody renders the session detail header — id, project, last
// updated and the first user message — as markdown that the existing
// glamour pipeline in renderDetail will style. Body is built once at
// adapter-list time and stored on Item.Body; lazyloading the full
// transcript is deferred to a later slice.
func SessionBody(firstUserMsg, project, sessionID string, lastUpdated time.Time) string {
	var b strings.Builder
	if sessionID != "" {
		fmt.Fprintf(&b, "**Session** `%s`\n\n", sessionID)
	}
	if project != "" {
		fmt.Fprintf(&b, "- **Project:** %s\n", project)
	}
	if !lastUpdated.IsZero() {
		fmt.Fprintf(&b, "- **Last updated:** %s\n", lastUpdated.Format("2006-01-02 15:04"))
	}
	b.WriteString("\n")
	if firstUserMsg != "" {
		b.WriteString("---\n\n### First user message\n\n")
		b.WriteString(firstUserMsg)
		b.WriteString("\n\n")
	}
	b.WriteString("---\n_Press `R` to resume this session._\n")
	return b.String()
}
