package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
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
			bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
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
			node, ok := bound.(functions.Binary)
			if !ok || node.Plan.Name() != name || !node.Plan.Immutable() || bound.ResultUsage() != b.Type(sqltype.Bool).Usage() || !bound.Properties().ParameterDependent || bound.Properties().Volatility != expr.Immutable || b.Params.Parameters[1].Type.ID() != sqltype.JSONB {
				t.Fatal(bound, b.Params.Parameters)
			}
			right := sqltype.Text
			if name == "?&" {
				right = 1009
			}
			if b.Params.Parameters[2].Type.ID() != right {
				t.Fatal(b.Params.Parameters)
			}
			next, err := node.Reconstruct(node.Info, node.Args)
			if err != nil || next.Plan != node.Plan || next.Syntax != node.Syntax || next.Property != node.Property {
				t.Fatal(next, err)
			}
			if pgIndexFunctionExpression(node) {
				t.Fatal("operator classified as function")
			}
		})
	}
}

func TestJSONBExistsRetagAndDeparse(t *testing.T) {
	engine := New()
	b := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
	for _, name := range []string{"?", "?|", "?&"} {
		right := b.Type(sqltype.Text)
		request := "a"
		if name != "?" {
			right = b.Type(1009)
			request = "{a}"
		}
		doc, err := b.Type(sqltype.JSONB).Input(context.Background(), `{"a":1}`, -1)
		if err != nil {
			t.Fatal(err)
		}
		key, err := right.Input(context.Background(), request, -1)
		if err != nil {
			t.Fatal(err)
		}
		args := []expr.Expr{
			plan.LiteralExpr{Info: expr.Info{Usage: b.Type(sqltype.JSONB).Usage()}, Value: doc},
			plan.LiteralExpr{Info: expr.Info{Usage: right.Usage()}, Value: key},
		}
		expr, err := functions.NewBinary(b.Info(b.Type(sqltype.Bool).Usage(), parser.Position{}, expr.Properties{}), name, args, parser.OrdinaryFunctionSyntax)
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
		got, err := rebound.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
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
		bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil || bound == nil || b.Params.Parameters[1].Type.ID() != sqltype.JSONB || b.Params.Parameters[2].Type.ID() != 1009 {
			t.Fatal(side, bound, err, b.Params.Parameters)
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
