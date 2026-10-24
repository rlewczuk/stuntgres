package functions

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestJSONBPathOpsReconstruct(t *testing.T) {
	ctx := context.Background()
	d, fresh := temporalTestTypes(), temporalTestTypes()
	for _, op := range []string{"@?", "@@"} {
		makeArgs := func(types map[sqltype.ID]*sqltype.Descriptor) []expr.Expr {
			t.Helper()
			var args []expr.Expr
			for i, id := range []sqltype.ID{sqltype.JSONB, sqltype.JSONPath} {
				v, err := types[id].Input(ctx, []string{`false`, `$`}[i], -1)
				if err != nil {
					t.Fatal(err)
				}
				args = append(args, temporalTestLiteral(v))
			}
			return args
		}
		old, err := NewBinary(expr.Info{Usage: d[sqltype.Bool].Usage()}, op, makeArgs(d), parser.OrdinaryFunctionSyntax)
		if err != nil {
			t.Fatal(err)
		}
		info := expr.Info{Usage: fresh[sqltype.Bool].Usage(), Property: expr.Properties{Volatility: expr.Volatile, ParameterDependent: true}, RoutineReference: jsonpathArrayUnitReference(4010)}
		got, err := old.Reconstruct(info, makeArgs(fresh))
		if err != nil {
			t.Fatal(err)
		}
		if got.Info != info || got.Plan == old.Plan || got.Validate() != nil || !got.Operator() || got.LooksLikeFunction() {
			t.Fatal("reconstruction lost provider/presentation", got)
		}
		// Non-NULL evaluation validates hidden vars/silent formals after remint.
		value, err := got.Eval(&temporalTestContext{Context: ctx})
		if err != nil || value.Type() != fresh[sqltype.Bool] || value.IsNull() {
			t.Fatal(value, err)
		}
		text, err := value.Type().Output(ctx, value)
		want := "f"
		if op == "@?" {
			want = "t"
		}
		if err != nil || text != want {
			t.Fatal(text, err)
		}
		if old.Args[0].ResultUsage().Type != d[sqltype.JSONB] {
			t.Fatal("reconstruct mutated source")
		}
		values := makeArgs(d)
		foreign, err := values[0].Eval(&temporalTestContext{Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		path, err := got.Args[1].Eval(&temporalTestContext{Context: ctx})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := got.Plan.EvaluateJSONPath(ctx, []sqltype.Value{foreign, path}, sqltype.JSONPathEnvironment{}); err == nil {
			t.Fatal("reminted plan admitted old value")
		}
		if _, err := got.Render(); err != nil {
			t.Fatal("replay rendering", err)
		}
		// Written child errors remain ordinary even with a NULL first operand.
		var order []int
		failure := errors.New("ordinary child failure")
		args := []expr.Expr{queryOrderArgument{value: sqltype.Null(d[sqltype.JSONB]), slot: 0, order: &order}, queryOrderArgument{value: sqltype.Null(d[sqltype.JSONPath]), slot: 1, order: &order, err: failure}}
		node, err := NewBinary(expr.Info{Usage: d[sqltype.Bool].Usage()}, op, args, parser.OrdinaryFunctionSyntax)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := node.Eval(&temporalTestContext{Context: ctx}); !errors.Is(err, failure) {
			t.Fatal("NULL hid child error", err)
		}
		if len(order) != 2 || order[0] != 0 || order[1] != 1 {
			t.Fatal("written child evaluation", order)
		}
	}
}
