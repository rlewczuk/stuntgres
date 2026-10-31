package parser

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParserStructuralOutput(t *testing.T) {
	statement := &AlterRule{Name: Identifier{Text: "r", Pos: Position{Offset: 1}}, Table: ObjectName{Parts: []Identifier{{Text: "public"}, {Text: "src"}}}, Rename: Identifier{Text: "renamed"}, Pos: Position{Offset: 20}}
	const want = `{parser.AlterRule Name {parser.Identifier Text "r"} Table {parser.ObjectName Parts [{parser.Identifier Text "public"} {parser.Identifier Text "src"}]} Rename {parser.Identifier Text "renamed"}}`
	var out strings.Builder
	if err := AppendStructuralSyntax(t.Context(), &out, statement); err != nil || out.String() != want {
		t.Fatal(out.String(), err)
	}
	statement.Pos.Offset = 500
	statement.Name.Pos.Character = 90
	out.Reset()
	if err := AppendStructuralSyntax(t.Context(), &out, statement); err != nil || out.String() != want {
		t.Fatal("source positions changed identity", out.String(), err)
	}
	var outputs []string
	for _, sql := range []string{"SELECT 1,2", "SELECT 2,1", "SELECT 1,3"} {
		statements, err := ParseRoutineDeclarationBody(sql)
		if err != nil {
			t.Fatal(err)
		}
		out.Reset()
		if err := AppendStructuralSyntax(t.Context(), &out, statements[0]); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "parser.Select") || !strings.Contains(out.String(), "parser.LiteralExpr") {
			t.Fatal(out.String())
		}
		outputs = append(outputs, out.String())
	}
	if outputs[0] == outputs[1] || outputs[0] == outputs[2] || outputs[1] == outputs[2] {
		t.Fatal("literal values or child order collapsed", outputs)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out.Reset()
	if err := AppendStructuralSyntax(ctx, &out, statement); !errors.Is(err, context.Canceled) || out.Len() != 0 {
		t.Fatal(out.String(), err)
	}
}
