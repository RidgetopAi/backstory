package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// docOf returns the doc comment text of the named func or of the const spec
// declaring the named identifier in freshness.go.
func docOf(t *testing.T, name string) string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "freshness.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse freshness.go: %v", err)
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name.Name == name && d.Doc != nil {
				return d.Doc.Text()
			}
		case *ast.GenDecl:
			for _, sp := range d.Specs {
				if vs, ok := sp.(*ast.ValueSpec); ok && vs.Names[0].Name == name && vs.Doc != nil {
					return vs.Doc.Text()
				}
			}
		}
	}
	t.Fatalf("no doc comment found for %s", name)
	return ""
}

func TestHandoffFreshnessDocListsAllReasonsInOrder(t *testing.T) {
	doc := strings.Join(strings.Fields(docOf(t, "HandoffFreshness")), " ")
	want := []FreshnessReasonKind{
		FreshnessContradicted, FreshnessLaterRecord, FreshnessLaterActivity,
		FreshnessLaterFailure, FreshnessLaterUnrecordedWork,
	}
	pos := 0
	for _, k := range want {
		i := strings.Index(doc[pos:], string(k))
		if i < 0 {
			t.Fatalf("doc does not list %q in order after offset %d:\n%s", k, pos, doc)
		}
		pos += i + len(k)
	}
	if strings.Contains(doc, "can only ever be flagged") {
		t.Errorf("doc still claims an empty about[] can only be flagged by contradicted:\n%s", doc)
	}
}

func TestUnrecordedWorkDocsStateAnyRecordClears(t *testing.T) {
	const rule = "record of any kind (handoff, outcome, note or decision)"
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if c := norm(docOf(t, "FreshnessLaterUnrecordedWork")); !strings.Contains(c, rule) {
		t.Errorf("FreshnessLaterUnrecordedWork comment missing %q:\n%s", rule, c)
	}
	b, err := os.ReadFile("../../AGENT-CONTRACT.md")
	if err != nil {
		t.Fatal(err)
	}
	if c := norm(strings.ReplaceAll(string(b), "\n   ", " ")); !strings.Contains(c, rule) {
		t.Errorf("AGENT-CONTRACT.md missing %q", rule)
	}
}
