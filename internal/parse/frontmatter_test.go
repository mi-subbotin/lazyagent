package parse

import (
	"strings"
	"testing"
)

func TestParse_NoFrontmatter(t *testing.T) {
	fm := Parse("just body, no frontmatter")
	if len(fm.Fields) != 0 {
		t.Errorf("Fields = %v, want empty", fm.Fields)
	}
	if fm.Body == "" {
		t.Error("Body lost when there is no frontmatter")
	}
	if len(fm.Errors) != 0 {
		t.Errorf("Errors = %v, want none for plain markdown", fm.Errors)
	}
}

func TestParse_Clean(t *testing.T) {
	src := "---\nname: foo\ndescription: bar\n---\n# heading\nbody body\n"
	fm := Parse(src)
	if fm.Fields["name"] != "foo" {
		t.Errorf("name = %q, want foo", fm.Fields["name"])
	}
	if fm.Fields["description"] != "bar" {
		t.Errorf("description = %q, want bar", fm.Fields["description"])
	}
	if !strings.HasPrefix(fm.Body, "# heading") {
		t.Errorf("Body lost; got %q", fm.Body)
	}
	if len(fm.Errors) != 0 {
		t.Errorf("Errors = %v, want none on clean input", fm.Errors)
	}
}

func TestParse_Unterminated(t *testing.T) {
	src := "---\nname: foo\nbody body without closing marker\n"
	fm := Parse(src)
	if len(fm.Errors) == 0 {
		t.Fatal("expected an unterminated-block error, got none")
	}
	if fm.Errors[0].Kind != "unterminated" {
		t.Errorf("error.Kind = %q, want unterminated", fm.Errors[0].Kind)
	}
	if fm.Errors[0].Line != 0 {
		t.Errorf("error.Line = %d, want 0 (file-level)", fm.Errors[0].Line)
	}
}

func TestParse_MissingColon(t *testing.T) {
	src := "---\nname: foo\nbroken line without colon\ndescription: ok\n---\nbody\n"
	fm := Parse(src)
	if len(fm.Errors) != 1 {
		t.Fatalf("Errors count = %d, want 1; got %v", len(fm.Errors), fm.Errors)
	}
	if fm.Errors[0].Kind != "missing-colon" {
		t.Errorf("error.Kind = %q, want missing-colon", fm.Errors[0].Kind)
	}
	if fm.Errors[0].Line != 3 {
		t.Errorf("error.Line = %d, want 3", fm.Errors[0].Line)
	}
	if fm.Fields["name"] != "foo" {
		t.Errorf("good fields lost: name = %q", fm.Fields["name"])
	}
	if fm.Fields["description"] != "ok" {
		t.Errorf("good fields lost: description = %q", fm.Fields["description"])
	}
}

func TestParse_EmptyKey(t *testing.T) {
	src := "---\n: value-without-key\nname: foo\n---\n"
	fm := Parse(src)
	if len(fm.Errors) != 1 {
		t.Fatalf("Errors count = %d, want 1; got %v", len(fm.Errors), fm.Errors)
	}
	if fm.Errors[0].Kind != "empty-key" {
		t.Errorf("error.Kind = %q, want empty-key", fm.Errors[0].Kind)
	}
	if fm.Errors[0].Line != 2 {
		t.Errorf("error.Line = %d, want 2", fm.Errors[0].Line)
	}
}

func TestParse_CommentsAndEmptyLinesIgnored(t *testing.T) {
	src := "---\n# this is a comment\n\nname: foo\n---\n"
	fm := Parse(src)
	if len(fm.Errors) != 0 {
		t.Errorf("Errors = %v, want none for comments and blanks", fm.Errors)
	}
	if fm.Fields["name"] != "foo" {
		t.Errorf("name = %q, want foo", fm.Fields["name"])
	}
}

func TestFrontmatterError_String(t *testing.T) {
	if got := (FrontmatterError{Line: 5, Message: "oops"}).String(); got != "line 5: oops" {
		t.Errorf("got %q", got)
	}
	if got := (FrontmatterError{Line: 0, Message: "file-level"}).String(); got != "file-level" {
		t.Errorf("got %q", got)
	}
}
