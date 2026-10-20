package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestLogicalLongBindingPositions(t *testing.T) {
	e := New()
	for _, tc := range []struct {
		expr              string
		character, offset int
	}{
		{strings.Repeat("TRUE AND ", 256) + "NULL", 14, 14},
		{strings.Repeat("FALSE OR ", 256) + "TRUE", 14, 14},
		{strings.Repeat("NOT ", 256) + "NULL", 14, 14},
		{"(" + strings.Repeat("TRUE AND ", 256) + "NULL)", 15, 15},
	} {
		stmts, err := parser.Parse("SELECT /*é*/ " + tc.expr + " FROM t")
		if err != nil {
			t.Fatal(err)
		}
		raw := stmts[0].(*parser.Select).Targets[0].(parser.LogicalExpr)
		operatorPosition := raw.Pos
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		want := parser.Position{Character: tc.character, Offset: tc.offset}
		if node.SourcePosition() != want || node.ResultUsage() != b.typ(sqltype.Bool).Usage() {
			t.Fatal(node.SourcePosition(), want, node.ResultUsage())
		}
		if raw.Pos != operatorPosition {
			t.Fatal("binding changed raw operator location")
		}
		// Every left-deep chain parent shares the first operand's location;
		// nested NOT parents instead retain their own earlier operator location.
		current := node
		for {
			logical, ok := current.(plan.LogicalExpr)
			if !ok {
				break
			}
			pos := logical.SourcePosition()
			if logical.Op == parser.LogicalNot {
				if pos.Character < tc.character || pos.Character >= logical.Left.SourcePosition().Character {
					t.Fatal("NOT earliest position", pos, logical.Left.SourcePosition())
				}
			} else if pos != want {
				t.Fatal("chain earliest position", pos, want)
			}
			current = logical.Left
		}
	}
}
