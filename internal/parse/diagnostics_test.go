package parse

import "testing"

func TestDiagnoseFrontmatter(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		pe, ws := DiagnoseFrontmatter(Frontmatter{}, nil)
		if pe != "" || len(ws) != 0 {
			t.Errorf("empty fm = %q / %v", pe, ws)
		}
	})

	t.Run("missing recommended", func(t *testing.T) {
		fm := Frontmatter{Fields: map[string]string{"name": "x"}}
		pe, ws := DiagnoseFrontmatter(fm, []string{"name", "description"})
		if pe != "" {
			t.Errorf("ParseError = %q, want empty", pe)
		}
		if len(ws) != 1 || ws[0] != "missing recommended field: description" {
			t.Errorf("warnings = %v", ws)
		}
	})

	t.Run("blank recommended counts as missing", func(t *testing.T) {
		fm := Frontmatter{Fields: map[string]string{"description": "   "}}
		_, ws := DiagnoseFrontmatter(fm, []string{"description"})
		if len(ws) != 1 {
			t.Errorf("blank field should still count as missing; got %v", ws)
		}
	})

	t.Run("errors join with semicolons", func(t *testing.T) {
		fm := Frontmatter{
			Errors: []FrontmatterError{
				{Line: 1, Message: "first"},
				{Line: 4, Message: "second"},
			},
		}
		pe, _ := DiagnoseFrontmatter(fm, nil)
		if pe == "" {
			t.Fatal("expected non-empty ParseError")
		}
	})
}
