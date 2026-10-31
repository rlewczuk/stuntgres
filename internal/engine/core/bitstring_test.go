package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestBitstringModifierProvenanceResolution(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	one := int32(1)
	for _, name := range []string{"bit", "varbit", "bit varying"} {
		raw := parser.TypeName{Name: name, Pos: parser.Position{Offset: 17, Character: 18}, Modifiers: []parser.TypeModifier{{Integer: &one, Pos: parser.Position{Offset: 21, Character: 22}}}}
		u, err := core.EngineResolveType(e, raw, binderOperation(b))
		if err != nil || u.Typmod != 1 {
			t.Fatal(u, err)
		}
		for _, modifier := range []parser.TypeModifier{
			{NonSimple: true, Raw: "1/0"},
			{NonSimple: true, Raw: "not an integer"},
			{NonSimple: true, Integer: &one},
			{NonSimple: true, UnaryPlus: true, Integer: &one},
		} {
			raw.Modifiers[0] = modifier
			_, err := core.EngineResolveType(e, raw, binderOperation(b))
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "type modifiers must be simple constants or identifiers", Position: "18"}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(name, modifier, diagnostic, err)
			}
		}
	}
}
