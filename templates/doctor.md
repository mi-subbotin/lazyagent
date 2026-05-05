You are auditing my lazyagent setup. Find duplicates, unused items, and anything else worth flagging.

Respond with valid JSON only — no preamble, no fenced code block, no commentary. Schema:

{
  "duplicates": [{"names": ["a","b"], "reason": "..."}],
  "unused":     [{"name":"x", "kind":"Skills", "reason":"..."}],
  "other":      [{"title":"...", "body":"..."}]
}

Items in my setup:

{{.ItemsYAML}}
