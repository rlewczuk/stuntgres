package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Exercise ordinary provider binding directly, without catalog-selected call
// completion supplying a second syntax gate. All children and predicates use
// the real typed binder; failed calls must not publish executable expressions.
func TestSelectedFunctionFilterBinding(t *testing.T) {
	for _, tc := range []struct {
		raw    parser.FunctionExpr
		result sqltype.ID
	}{
		{datetimeMakeRaw("pg_size_bytes", datetimeMakeString("1 MB")), sqltype.Int8},
		{datetimeMakeRaw("pg_size_pretty", ageFiniteTyped("bigint", "1024")), sqltype.Text},
		{datetimeMakeRaw("abs", datetimeMakeInteger(1)), sqltype.Int4},
		{datetimeMakeRaw("random"), sqltype.Float8},
		{datetimeMakeRaw("obj_description", ageFiniteTyped("oid", "0")), sqltype.Text},
		{datetimeMakeRaw("pg_table_is_visible", ageFiniteTyped("oid", "0")), sqltype.Bool},
		{datetimeMakeRaw("pg_get_viewdef", ageFiniteTyped("oid", "0")), sqltype.Text},
		{datetimeMakeRaw("overlaps", ageFiniteTyped("time", "01:00"), ageFiniteTyped("time", "02:00"), ageFiniteTyped("time", "01:30"), ageFiniteTyped("time", "03:00")), sqltype.Bool},
	} {
		t.Run(tc.raw.Name.Parts[0].Text, func(t *testing.T) {
			for _, phase := range []string{"valid", "decoration", "predicate", "argument"} {
				t.Run(phase, func(t *testing.T) {
					b := enginetest.ExpressionBinder(t, context.Background(), engine.New(), sqltype.InputEnvironment{}, nil)
					b.Clause.AggregateClause = "SELECT"
					raw := tc.raw
					code, position := "", ""
					if phase != "valid" {
						raw.Filter = parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true, Pos: parser.Position{Character: 60}}}
						code, position = "42809", "8"
					}
					if phase == "predicate" || phase == "argument" {
						raw.Filter = parser.ColumnExpr{Pos: parser.Position{Character: 60}, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "predicate_missing", Pos: parser.Position{Character: 60}}}}}
						code, position = "42703", "60"
					}
					if phase == "argument" && len(raw.Args) != 0 {
						raw.Args = append([]parser.Expr(nil), raw.Args...)
						raw.Args[0] = parser.ColumnExpr{Pos: parser.Position{Character: 30}, Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "argument_missing", Pos: parser.Position{Character: 30}}}}}
						position = "30"
					}
					node, err := b.BindFunctionProvider(raw)
					if code == "" {
						if err != nil || node == nil || node.ResultUsage().Type.ID() != tc.result {
							t.Fatal("unmodified typed provider", node, err)
						}
						return
					}
					var diagnostic *sqlstate.Error
					if node != nil || !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Position != position {
						t.Fatal("failed provider publication or ordering", node, err, code, position)
					}
				})
			}
		})
	}
}
