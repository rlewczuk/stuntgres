package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Exercise the captured-scalar serializer's output as parser/binder input, not
// a source-text/wiring golden. A bare NULL would lose the interval overload.
func TestUUIDGenerationCapturedNullTyping(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, child := range []string{"NULL", "NULL::interval", "CASE WHEN true THEN NULL::interval ELSE interval '0' END", "coalesce(NULL::interval,NULL::interval)"} {
		t.Run(child, func(t *testing.T) {
			statements, err := parser.Parse("SELECT uuidv7(" + child + ")")
			if err != nil {
				t.Fatal(err)
			}
			node, err := b.Bind(statements[0].(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			// Repeated serialization exercises re-inference without relying on
			// incidental printer wording or requiring a provider session.
			for round := range 3 {
				emitted, err := stored.DefaultSQL(ctx, e.CatalogRead(), node)
				if err != nil {
					t.Fatal(err)
				}
				statements, err = parser.Parse("SELECT " + emitted)
				if err != nil {
					t.Fatal(err)
				}
				node, err = b.Bind(statements[0].(*parser.Select).Targets[0], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
				if err != nil {
					t.Fatal(err)
				}
				provider, ok := node.(functions.UUIDGeneration)
				if !ok || provider.ResultUsage().Type != b.Type(sqltype.UUID) || len(provider.Args) != 1 || provider.Args[0].ResultUsage().Type != b.Type(sqltype.Interval) {
					t.Fatalf("round%d lost selected UUID interval signature: %T", round, node)
				}
				value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
				if err != nil || !value.IsNull() || value.Type() != b.Type(sqltype.UUID) {
					t.Fatal("serialized strict NULL demand", value, err)
				}
			}
		})
	}
}
