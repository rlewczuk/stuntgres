package engine

import (
	"context"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Exercise the captured-scalar serializer's output as parser/binder input, not
// a source-text/wiring golden. A bare NULL would lose the interval overload.
func TestUUIDGenerationCapturedNullTyping(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, child := range []string{"NULL", "NULL::interval", "CASE WHEN true THEN NULL::interval ELSE interval '0' END", "coalesce(NULL::interval,NULL::interval)"} {
		t.Run(child, func(t *testing.T) {
			statements, err := parser.Parse("SELECT uuidv7(" + child + ")")
			if err != nil {
				t.Fatal(err)
			}
			node, err := b.bind(statements[0].(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			// Repeated serialization exercises re-inference without relying on
			// incidental printer wording or requiring a provider session.
			for round := range 3 {
				emitted, err := defaultSQL(ctx, e.CatalogRead(), node)
				if err != nil {
					t.Fatal(err)
				}
				statements, err = parser.Parse("SELECT " + emitted)
				if err != nil {
					t.Fatal(err)
				}
				node, err = b.bind(statements[0].(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
				if err != nil {
					t.Fatal(err)
				}
				provider, ok := node.(uuidGenerationExpression)
				if !ok || provider.ResultUsage().Type != b.typ(sqltype.UUID) || len(provider.args) != 1 || provider.args[0].ResultUsage().Type != b.typ(sqltype.Interval) {
					t.Fatalf("round%d lost selected UUID interval signature: %T", round, node)
				}
				value, err := node.Eval(&EvalContext{Context: ctx})
				if err != nil || !value.IsNull() || value.Type() != b.typ(sqltype.UUID) {
					t.Fatal("serialized strict NULL demand", value, err)
				}
			}
		})
	}
}
