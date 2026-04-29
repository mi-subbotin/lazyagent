package parse

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// transcriptMessageCap caps how many user/assistant messages a built
// transcript holds. Sessions in the wild reach 1000+ turns and rendering
// every line through glamour would freeze the TUI on cursor movement.
// We keep the most recent N — the user is almost always interested in
// the tail anyway. The cap can be raised once virtualised rendering
// lands.
const transcriptMessageCap = 500

// BuildClaudeTranscript reads a Claude session .jsonl from disk and
// returns a markdown transcript suitable for the detail panel. Only
// user and assistant turns appear; tool calls render as one-line
// placeholders so context remains readable. Errors collapse to an
// inline note rather than aborting — a partially written active
// session should still display whatever was flushed so far.
func BuildClaudeTranscript(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Sprintf("_transcript unavailable: %s_\n", err)
	}
	defer f.Close()

	type entry struct {
		role string
		ts   time.Time
		body string
	}
	var msgs []entry

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Type      string          `json:"type"`
			Timestamp string          `json:"timestamp"`
			Message   json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Type != "user" && rec.Type != "assistant" {
			continue
		}
		body := claudeMessageBody(rec.Message)
		if strings.TrimSpace(body) == "" {
			continue
		}
		ts, _ := time.Parse(time.RFC3339Nano, rec.Timestamp)
		msgs = append(msgs, entry{role: rec.Type, ts: ts, body: body})
	}

	truncated := false
	if len(msgs) > transcriptMessageCap {
		msgs = msgs[len(msgs)-transcriptMessageCap:]
		truncated = true
	}

	var b strings.Builder
	if truncated {
		fmt.Fprintf(&b, "_Showing the last %d messages of a longer transcript._\n\n---\n\n", transcriptMessageCap)
	}
	for _, m := range msgs {
		writeMessage(&b, m.role, m.ts, m.body)
	}
	if b.Len() == 0 {
		return "_no user/assistant messages in this transcript_\n"
	}
	return b.String()
}

// claudeMessageBody collapses the various Claude content shapes into
// plain markdown. Bare strings come through as-is; content-block
// arrays are flattened keeping text and rendering tool_use /
// tool_result as compact placeholders so the user sees the shape of
// the conversation without drowning in JSON.
func claudeMessageBody(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return ""
	}
	if len(msg.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(msg.Content, &s); err == nil {
		return s
	}
	var blocks []map[string]any
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return ""
	}
	var out []string
	for _, blk := range blocks {
		switch blk["type"] {
		case "text":
			if s, ok := blk["text"].(string); ok && s != "" {
				out = append(out, s)
			}
		case "tool_use":
			name, _ := blk["name"].(string)
			out = append(out, fmt.Sprintf("_[tool_use: %s]_", name))
		case "tool_result":
			out = append(out, "_[tool_result]_")
		}
	}
	return strings.Join(out, "\n\n")
}

// BuildGeminiTranscript reads a Gemini session .json and renders the
// messages as markdown. Format is simpler than Claude's: a flat
// {type, content} array. We keep the same role headers and time
// stamps for visual parity in the detail panel.
func BuildGeminiTranscript(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("_transcript unavailable: %s_\n", err)
	}
	var s struct {
		Messages []struct {
			Type      string `json:"type"`
			Content   string `json:"content"`
			Timestamp string `json:"timestamp"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Sprintf("_transcript parse error: %s_\n", err)
	}
	// Defensive: ensure chronological order — the file typically is
	// already, but a strict sort is cheap and removes a class of "looks
	// scrambled" bugs if Gemini ever changes the writer.
	sort.SliceStable(s.Messages, func(i, j int) bool {
		return s.Messages[i].Timestamp < s.Messages[j].Timestamp
	})
	msgs := s.Messages
	truncated := false
	if len(msgs) > transcriptMessageCap {
		msgs = msgs[len(msgs)-transcriptMessageCap:]
		truncated = true
	}
	var b strings.Builder
	if truncated {
		fmt.Fprintf(&b, "_Showing the last %d messages of a longer transcript._\n\n---\n\n", transcriptMessageCap)
	}
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		ts, _ := time.Parse(time.RFC3339Nano, m.Timestamp)
		writeMessage(&b, m.Type, ts, m.Content)
	}
	if b.Len() == 0 {
		return "_no messages in this transcript_\n"
	}
	return b.String()
}

func writeMessage(b *strings.Builder, role string, ts time.Time, body string) {
	header := role
	if header != "" {
		header = strings.ToUpper(header[:1]) + header[1:]
	}
	if !ts.IsZero() {
		fmt.Fprintf(b, "### %s · %s\n\n", header, ts.Format("2006-01-02 15:04"))
	} else {
		fmt.Fprintf(b, "### %s\n\n", header)
	}
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n\n---\n\n")
}
