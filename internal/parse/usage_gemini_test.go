package parse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadGeminiUsageGroupsByModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	body := `{
		"sessionId": "abc",
		"messages": [
			{"type":"user","content":"hi","model":"gemini-3-pro-preview","tokens":{"input":1000,"output":50,"cached":400,"thoughts":20,"tool":0,"total":1070}},
			{"type":"assistant","content":"hello","model":"gemini-3-pro-preview","tokens":{"input":2000,"output":80,"cached":1500,"thoughts":10,"tool":0,"total":2090}},
			{"type":"user","content":"continue","model":"gemini-2.5-flash","tokens":{"input":500,"output":30,"cached":0,"thoughts":0,"tool":0,"total":530}}
		]
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	usages, err := ReadGeminiUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 2 {
		t.Fatalf("want 2 per-model entries, got %d", len(usages))
	}
	pro := usages[0]
	if pro.Model != "gemini-3-pro-preview" {
		t.Errorf("entry[0] model = %q; want gemini-3-pro-preview", pro.Model)
	}
	// 1000 + 2000 = 3000 total input, 400+1500=1900 cached → uncached=1100
	if pro.InputTokens != 1100 {
		t.Errorf("pro InputTokens = %d; want 1100", pro.InputTokens)
	}
	if pro.CacheReadTokens != 1900 {
		t.Errorf("pro CacheReadTokens = %d; want 1900", pro.CacheReadTokens)
	}
	// Output + thoughts: (50+20) + (80+10) = 160
	if pro.OutputTokens != 160 {
		t.Errorf("pro OutputTokens = %d; want 160", pro.OutputTokens)
	}
	if pro.Messages != 2 {
		t.Errorf("pro Messages = %d; want 2", pro.Messages)
	}

	flash := usages[1]
	if flash.Model != "gemini-2.5-flash" {
		t.Errorf("entry[1] model = %q; want gemini-2.5-flash", flash.Model)
	}
	if flash.InputTokens != 500 || flash.OutputTokens != 30 {
		t.Errorf("flash usage = %+v", flash)
	}
}

func TestReadGeminiUsageNoTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	body := `{"sessionId":"abc","messages":[{"type":"user","content":"hi"}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	usages, err := ReadGeminiUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 0 {
		t.Errorf("want 0 entries, got %d", len(usages))
	}
}

func TestReadGeminiUsageMostRecentLast(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	body := `{
		"sessionId":"x",
		"messages":[
			{"model":"gemini-3-pro-preview","tokens":{"input":1,"output":1,"total":2}},
			{"model":"gemini-2.5-flash","tokens":{"input":1,"output":1,"total":2}}
		]
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	usages, err := ReadGeminiUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	if usages[len(usages)-1].Model != "gemini-2.5-flash" {
		t.Errorf("expected last model gemini-2.5-flash, got %q", usages[len(usages)-1].Model)
	}
}
