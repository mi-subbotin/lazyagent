package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

func writeJSON(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNewFormOverlayMCPStdio(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg := dir + "/.claude.json"
	writeJSON(t, cfg, `{"mcpServers":{"linear":{"command":"npx","args":["@linear/mcp"],"env":{"API_KEY":"x"},"type":"stdio"}}}`)
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal,
		Name: "linear", Path: cfg, Storage: model.StorageEntry,
		ConfigKey: "mcpServers/linear",
	}
	f, ok := newFormOverlay(it)
	if !ok {
		t.Fatal("expected form overlay for stdio MCP entry")
	}
	if f.schema.Title != "MCP server" {
		t.Errorf("schema title=%q; want 'MCP server'", f.schema.Title)
	}
	// Type field should be 'stdio' selected.
	typeField := findField(f, "type")
	if typeField == nil || typeField.spec.Choices[typeField.enumIndex] != "stdio" {
		t.Errorf("type field not initialized to stdio: %+v", typeField)
	}
	// command field visible and populated.
	cmdField := findField(f, "command")
	if cmdField == nil || !cmdField.visible || cmdField.input.Value() != "npx" {
		t.Errorf("command field wrong: %+v", cmdField)
	}
	// url field hidden (transport is stdio).
	urlField := findField(f, "url")
	if urlField == nil || urlField.visible {
		t.Errorf("url field should be hidden in stdio mode")
	}
}

func TestNewFormOverlayMCPHTTP(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg := dir + "/.claude.json"
	writeJSON(t, cfg, `{"mcpServers":{"sentry":{"url":"https://mcp.sentry.io/sse","type":"sse","headers":{"Authorization":"Bearer x"}}}}`)
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal,
		Name: "sentry", Path: cfg, Storage: model.StorageEntry,
		ConfigKey: "mcpServers/sentry",
	}
	f, ok := newFormOverlay(it)
	if !ok {
		t.Fatal("expected form overlay for sse MCP entry")
	}
	cmdField := findField(f, "command")
	if cmdField == nil || cmdField.visible {
		t.Errorf("command should be hidden in sse mode")
	}
	urlField := findField(f, "url")
	if urlField == nil || !urlField.visible || urlField.input.Value() != "https://mcp.sentry.io/sse" {
		t.Errorf("url field wrong: %+v", urlField)
	}
}

func TestNewFormOverlayUnknownShapeFallsBack(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg := dir + "/.claude.json"
	// No command and no url — neither stdio nor sse/http shape.
	writeJSON(t, cfg, `{"mcpServers":{"weird":{"foo":"bar"}}}`)
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal,
		Name: "weird", Path: cfg, Storage: model.StorageEntry,
		ConfigKey: "mcpServers/weird",
	}
	if _, ok := newFormOverlay(it); ok {
		t.Error("expected form overlay to refuse a non-matching MCP entry")
	}
}

func TestFormOverlaySaveRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg := dir + "/.claude.json"
	writeJSON(t, cfg, `{"mcpServers":{"linear":{"command":"npx","args":["@linear/mcp"],"env":{},"type":"stdio"}},"otherKey":"keepMe"}`)
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal,
		Name: "linear", Path: cfg, Storage: model.StorageEntry,
		ConfigKey: "mcpServers/linear",
	}
	f, ok := newFormOverlay(it)
	if !ok {
		t.Fatal("form expected")
	}
	cmdField := findField(f, "command")
	cmdField.input.SetValue("uvx")
	if err := f.saveEntry(); err != nil {
		t.Fatalf("saveEntry: %v", err)
	}
	got, _ := os.ReadFile(cfg)
	s := string(got)
	if !strings.Contains(s, "\"command\": \"uvx\"") {
		t.Errorf("expected command updated, got:\n%s", s)
	}
	if !strings.Contains(s, "otherKey") {
		t.Errorf("unrelated keys clobbered:\n%s", s)
	}
}

func TestFormHookSchema(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg := dir + "/settings.json"
	writeJSON(t, cfg, `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo hi","timeout":5}]}]}}`)
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindHook, Scope: model.ScopeGlobal,
		Name: "PreToolUse:Bash", Path: cfg, Storage: model.StorageEntry,
		ConfigKey: "hooks/PreToolUse/0/hooks/0",
	}
	f, ok := newFormOverlay(it)
	if !ok {
		t.Fatal("expected hook form")
	}
	cmdField := findField(f, "command")
	if cmdField == nil || cmdField.input.Value() != "echo hi" {
		t.Errorf("command field wrong: %+v", cmdField)
	}
	timeoutField := findField(f, "timeout")
	if timeoutField == nil || timeoutField.input.Value() != "5" {
		t.Errorf("timeout field wrong: %+v", timeoutField)
	}
}

func TestFormListModeToggleRebuilds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg := dir + "/.claude.json"
	writeJSON(t, cfg, `{"mcpServers":{"linear":{"command":"npx","args":["a","b","c"],"env":{},"type":"stdio"}}}`)
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal,
		Name: "linear", Path: cfg, Storage: model.StorageEntry,
		ConfigKey: "mcpServers/linear",
	}
	f, ok := newFormOverlay(it)
	if !ok {
		t.Fatal("form expected")
	}
	if f.listMode != "lines" {
		t.Errorf("default mode=%q; want lines", f.listMode)
	}
	argsField := findField(f, "args")
	if argsField.textarea.Value() != "a\nb\nc" {
		t.Errorf("textarea content=%q; want a\\nb\\nc", argsField.textarea.Value())
	}
	// Simulate ctrl+m toggle by rebuilding with fields mode.
	f.fields = buildFields(f.schema, f.collectValues(), "fields")
	f.recomputeVisibility()
	f.listMode = "fields"
	argsField = findField(f, "args")
	if len(argsField.rowsValues) != 3 {
		t.Errorf("rows after toggle: %d; want 3", len(argsField.rowsValues))
	}
	if argsField.rowsValues[0].Value() != "a" {
		t.Errorf("row[0]=%q; want a", argsField.rowsValues[0].Value())
	}
}

func TestReadStringMapLines(t *testing.T) {
	fld := formField{textarea: newTextarea("FOO=1\nBAR=2\n\nbaz=  three  \nbroken_no_eq\n=novalue")}
	got := readStringMap(fld, "lines")
	if got["FOO"] != "1" || got["BAR"] != "2" || got["baz"] != "three" {
		t.Errorf("unexpected map: %+v", got)
	}
	if _, ok := got["broken_no_eq"]; ok {
		t.Error("lines without = should be dropped")
	}
}

func TestReadStringListLines(t *testing.T) {
	fld := formField{textarea: newTextarea("a\n  b  \n\nc\n")}
	got := readStringList(fld, "lines")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("unexpected list: %+v", got)
	}
}

func TestValidateAllSurfacesEnvKeyWarning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg := dir + "/.claude.json"
	writeJSON(t, cfg, `{"mcpServers":{"linear":{"command":"npx","args":[],"env":{"lower-case-key":"v"},"type":"stdio"}}}`)
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal,
		Name: "linear", Path: cfg, Storage: model.StorageEntry,
		ConfigKey: "mcpServers/linear",
	}
	f, ok := newFormOverlay(it)
	if !ok {
		t.Fatal("form expected")
	}
	envField := findField(f, "env")
	if envField == nil {
		t.Fatal("env field missing")
	}
	if !strings.Contains(envField.warning, "non-conforming env keys") {
		t.Errorf("expected env-key warning, got %q", envField.warning)
	}
}

func TestValidateAllSurfacesURLWarning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg := dir + "/.claude.json"
	writeJSON(t, cfg, `{"mcpServers":{"x":{"url":"not-a-url","type":"http"}}}`)
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal,
		Name: "x", Path: cfg, Storage: model.StorageEntry,
		ConfigKey: "mcpServers/x",
	}
	f, ok := newFormOverlay(it)
	if !ok {
		t.Fatal("form expected")
	}
	urlField := findField(f, "url")
	if urlField == nil || urlField.warning == "" {
		t.Errorf("expected url warning, got %+v", urlField)
	}
}

// PRI-77: advanceFocus called textarea.Focus() unconditionally, but
// textarea is a zero-value struct on non-list fields and its inner
// cursor.Model has nil channels — BlinkCmd dereferenced them and
// crashed. The focus() helper now picks the right widget; this test
// guards the regression by tab-cycling through a stdio MCP form,
// which has both string and stringList fields.
func TestFormAdvanceFocusDoesNotPanic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg := dir + "/.claude.json"
	writeJSON(t, cfg, `{"mcpServers":{"linear":{"command":"npx","args":["@linear/mcp"],"env":{"FOO":"1"},"type":"stdio"}}}`)
	it := model.Item{
		Origin: model.OriginClaude, Kind: model.KindMCP, Scope: model.ScopeGlobal,
		Name: "linear", Path: cfg, Storage: model.StorageEntry,
		ConfigKey: "mcpServers/linear",
	}
	f, ok := newFormOverlay(it)
	if !ok {
		t.Fatal("form expected")
	}
	// Tab through every field forward, then back; if either calls
	// Focus on a zero-value bubbles widget, the cursor blink path
	// will panic.
	for i := 0; i < len(f.fields)*2; i++ {
		f.advanceFocus(+1)
	}
	for i := 0; i < len(f.fields)*2; i++ {
		f.advanceFocus(-1)
	}
}

func TestMCPSchemaShapeMatches(t *testing.T) {
	sch := mcpSchema()
	if !sch.shapeMatches(map[string]any{"command": "npx", "type": "stdio"}) {
		t.Error("stdio MCP should match")
	}
	if !sch.shapeMatches(map[string]any{"command": "npx"}) {
		t.Error("MCP with command but missing type defaults to stdio shape")
	}
	if !sch.shapeMatches(map[string]any{"url": "https://x", "type": "sse"}) {
		t.Error("sse MCP with url should match")
	}
	if sch.shapeMatches(map[string]any{"foo": "bar"}) {
		t.Error("MCP without command/url should not match")
	}
}

func findField(f *formOverlay, name string) *formField {
	for i := range f.fields {
		if f.fields[i].spec.Name == name {
			return &f.fields[i]
		}
	}
	return nil
}
