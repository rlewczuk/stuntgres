package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestJSONBExistsBinding(t *testing.T) {
	for _, name := range []string{"?", "?|", "?&"} {
		t.Run(name, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
			pos := parser.Position{Offset: 10, Character: 11}
			raw := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name, Pos: pos}}}, Pos: pos, Args: []parser.Expr{parser.ParameterExpr{Number: 1, Pos: pos}, parser.ParameterExpr{Number: 2, Pos: pos}}}
			bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if name == "?|" {
				d, ok := err.(*sqlstate.Error)
				if !ok || d.Code != "42725" || d.Position != "11" || d.Message != "operator is not unique: unknown ?| unknown" || bound != nil {
					t.Fatal(bound, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expr, ok := bound.(functions.Binary[BoundExpr, ExprContext, ExprProperties])
			if !ok || expr.Plan.Name() != name || !expr.Plan.Immutable() || bound.ResultUsage() != b.typ(sqltype.Bool).Usage() || !bound.Properties().ParameterDependent || bound.Properties().Volatility != Immutable || b.parameters[1].Type.ID() != sqltype.JSONB {
				t.Fatal(bound, b.parameters)
			}
			right := sqltype.Text
			if name == "?&" {
				right = 1009
			}
			if b.parameters[2].Type.ID() != right {
				t.Fatal(b.parameters)
			}
			next, err := expr.Reconstruct(expr.Info, expr.Args)
			if err != nil || next.Plan != expr.Plan || next.Syntax != expr.Syntax || next.Property != expr.Property {
				t.Fatal(next, err)
			}
			if pgIndexFunctionExpression(expr) {
				t.Fatal("operator classified as function")
			}
		})
	}
}

func TestJSONBExistsRetagAndDeparse(t *testing.T) {
	engine := New()
	b := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
	for _, name := range []string{"?", "?|", "?&"} {
		right := b.typ(sqltype.Text)
		request := "a"
		if name != "?" {
			right = b.typ(1009)
			request = "{a}"
		}
		doc, err := b.typ(sqltype.JSONB).Input(context.Background(), `{"a":1}`, -1)
		if err != nil {
			t.Fatal(err)
		}
		key, err := right.Input(context.Background(), request, -1)
		if err != nil {
			t.Fatal(err)
		}
		args := []BoundExpr{
			literalExpression{expressionInfo: expressionInfo{Usage: b.typ(sqltype.JSONB).Usage()}, value: doc},
			literalExpression{expressionInfo: expressionInfo{Usage: right.Usage()}, value: key},
		}
		expr, err := functions.NewBinary[BoundExpr, ExprContext](functionInfo(b.info(b.typ(sqltype.Bool).Usage(), parser.Position{}, ExprProperties{})), name, args, parser.OrdinaryFunctionSyntax)
		if err != nil {
			t.Fatal(err)
		}
		renderer := pgRenderer{view: engine.CatalogRead()}
		text, err := renderer.sql(context.Background(), expr, pgRenderParent{})
		if err != nil || text == "" {
			t.Fatal(text, err)
		}
		// Actual operator spelling, not a quoted function call, must survive.
		if !strings.Contains(text, " "+name+" ") {
			t.Fatal(text)
		}
		replacement := map[sqltype.ID]*sqltype.Descriptor{}
		for _, d := range sqltype.Builtins() {
			replacement[d.ID()] = d
		}
		rebound, changed, err := retagBoundTypes(engine.CatalogRead(), expr, func(old *sqltype.Descriptor) *sqltype.Descriptor { return replacement[old.ID()] })
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		got, err := rebound.Eval(&EvalContext{Context: context.Background()})
		if err != nil || got != sqltype.Boolean(replacement[sqltype.Bool], true) {
			t.Fatal(got, err)
		}
	}
}

func TestJSONBExistsOneSidedInference(t *testing.T) {
	for _, side := range []int{0, 1} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		pos := parser.Position{Character: 11}
		args := []parser.Expr{parser.ParameterExpr{Number: 1, Pos: pos}, parser.ParameterExpr{Number: 2, Pos: pos}}
		typeName := parser.TypeName{Name: "jsonb", Pos: pos}
		if side == 1 {
			typeName = parser.TypeName{Name: "text", Pos: pos, ArrayBounds: []int32{-1}}
		}
		args[side] = parser.CastExpr{Child: args[side], TypeName: typeName, Syntax: parser.PostfixCast, Pos: pos}
		raw := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "?|", Pos: pos}}}, Args: args, Pos: pos}
		bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil || bound == nil || b.parameters[1].Type.ID() != sqltype.JSONB || b.parameters[2].Type.ID() != 1009 {
			t.Fatal(side, bound, err, b.parameters)
		}
	}
}

func TestJSONBExistsViewRoundtrip(t *testing.T) {
	ctx := context.Background()
	e := New()
	run(t, e, "CREATE DOMAIN jbe_roundtrip_keys AS text[]")
	run(t, e, `CREATE VIEW jbe_roundtrip AS SELECT
		'{"a":1}'::jsonb ?| '{missing,a}'::text[] AS any_key,
		'{"a":1}'::jsonb ?& '{a,missing}'::text[] AS all_keys,
		'{"a":1}'::jsonb ?| NULL::text[] AS null_keys,
		'{"a":1}'::jsonb ?| CAST(CAST('{ab}' AS varchar[]) AS varchar(1)[]) AS narrowed,
		'{"a":1}'::jsonb ?| CAST('{a}'::text[] AS jbe_roundtrip_keys) AS domain_keys,
		'{"a":1}'::jsonb ?| CAST(CAST('{a}' AS text[]) AS text[]) AS nested_identity`)
	query := "SELECT any_key,all_keys,null_keys,narrowed,domain_keys,nested_identity FROM jbe_roundtrip"
	want := [][]driver.Value{{true, false, nil, true, true, true}}
	if got := tuples(run(t, e, query)); !reflect.DeepEqual(got, want) {
		t.Fatalf("original rows=%v want=%v", got, want)
	}
	var original bytes.Buffer
	if err := e.Dump(ctx, &original); err != nil {
		t.Fatal(err)
	}
	for cycle := 1; cycle <= 3; cycle++ {
		if err := e.Restore(ctx, bytes.NewReader(original.Bytes())); err != nil {
			t.Fatalf("restore cycle %d: %v", cycle, err)
		}
		if got := tuples(run(t, e, query)); !reflect.DeepEqual(got, want) {
			t.Fatalf("cycle %d rows=%v want=%v", cycle, got, want)
		}
		var again bytes.Buffer
		if err := e.Dump(ctx, &again); err != nil {
			t.Fatal(err)
		}
		if again.String() != original.String() {
			t.Fatalf("cycle %d changed canonical dump:\nbefore:\n%s\nafter:\n%s", cycle, original.String(), again.String())
		}
	}
}
