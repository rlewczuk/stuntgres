package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestJSONBElementsMarkerTraversalRetagAndInvocation(t *testing.T) {
	engine := New()
	view := engine.CatalogRead()
	b := newTestExpressionBinder(t, context.Background(), engine, sqltype.InputEnvironment{}, nil)
	for _, name := range []string{"jsonb_array_elements", "jsonb_array_elements_text"} {
		id := sqltype.JSONB
		if name == "jsonb_array_elements_text" {
			id = sqltype.Text
		}
		value, err := b.Type(sqltype.JSONB).Input(context.Background(), `[1,null,[2]]`, -1)
		if err != nil {
			t.Fatal(err)
		}
		arg := plan.LiteralExpr{Info: expr.Info{Usage: b.Type(sqltype.JSONB).Usage()}, Value: value}
		original, err := functions.NewJSONBElements(expr.Info{Usage: b.Type(id).Usage(), Pos: parser.Position{Offset: 20, Character: 21}, Property: expr.Properties{StageDependent: true}}, name, expr.Expr(arg))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := original.Reconstruct(original.Info, nil); err == nil {
			t.Fatal("zero children accepted")
		}
		if _, err := original.Reconstruct(original.Info, []expr.Expr{arg, arg}); err == nil {
			t.Fatal("two children accepted")
		}
		if _, err := original.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}); err == nil {
			t.Fatal("unlifted evaluation accepted")
		}
		fresh := jsonObjectUnitTypes()
		mapped, changed, err := retagBoundTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		next := mapped.(functions.JSONBElements)
		assertFirst := func(call expr.Expr, result *sqltype.Descriptor, want string) {
			t.Helper()
			cursor, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, call)
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			v, ok, err := cursor.Next(context.Background())
			if err != nil || !ok {
				t.Fatal("missing first element", ok, err)
			}
			got, err := result.Output(context.Background(), v)
			if err != nil || got != want {
				t.Fatalf("element=%q want=%q error=%v", got, want, err)
			}
		}
		assertFirst(next, fresh[id], "1")
		root := &plan.FunctionFromNode{Calls: []expr.Expr{original}, QuerySchema: plan.QuerySchema{{Usage: original.ResultUsage()}}, Frame: 1}
		replacementValue, err := b.Type(sqltype.JSONB).Input(context.Background(), `[9]`, -1)
		if err != nil {
			t.Fatal(err)
		}
		replacement := queryProbe(arg.ResultUsage(), func(*exec.Context) (sqltype.Value, error) { return replacementValue, nil })
		rewritten, err := plan.RewriteQueryExpressions(root, func(n expr.Expr) (expr.Expr, bool, error) {
			if n == original.Args[0] {
				return replacement, true, nil
			}
			return n, false, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		assertFirst(rewritten.(*plan.FunctionFromNode).Calls[0], b.Type(id), "9")
		assertFirst(root.Calls[0], b.Type(id), "1")
		draws := 0
		// Use the reminted argument captured before replacing its expression.
		input := mapped.(functions.JSONBElements).Args[0]
		next.Args = []expr.Expr{queryProbe(fresh[sqltype.JSONB].Usage(), func(c *exec.Context) (sqltype.Value, error) { draws++; return input.Eval(c) })}
		cursor, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, next)
		if err != nil {
			t.Fatal(err)
		}
		for i, want := range []string{"1", "null", "[2]"} {
			v, ok, err := cursor.Next(context.Background())
			if err != nil || !ok {
				t.Fatal("missing reminted element", i, ok, err)
			}
			if i == 1 && id == sqltype.Text {
				if !v.IsNull() {
					t.Fatal("JSON null did not become SQL NULL")
				}
				continue
			}
			got, err := fresh[id].Output(context.Background(), v)
			if err != nil || got != want {
				t.Fatalf("element%d=%q want=%q error=%v", i, got, want, err)
			}
		}
		cursor.Close()
		if draws != 1 {
			t.Fatal("repeated child evaluation", draws)
		}
		failure := errors.New("selected child failure")
		next.Args = []expr.Expr{queryProbe(fresh[sqltype.JSONB].Usage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, failure })}
		if c, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, next); !errors.Is(err, failure) || c.Type != nil {
			t.Fatal("published failed invocation", c, err)
		}
	}
}
