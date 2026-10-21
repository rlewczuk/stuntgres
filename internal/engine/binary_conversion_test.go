package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBinaryConversionCarrier(t *testing.T) {
	types := map[sqltype.ID]*sqltype.Descriptor{}
	replacement := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	for _, d := range sqltype.Builtins() {
		replacement[d.ID()] = d
	}
	for _, tc := range []struct {
		name       string
		ids        []sqltype.ID
		result     sqltype.ID
		raw        []string
		want       string
		volatility expr.Volatility
	}{
		{"convert", []sqltype.ID{sqltype.Bytea, sqltype.Name, sqltype.Name}, sqltype.Bytea, []string{"\xe9", "LATIN1", "UTF8"}, "é", expr.Stable},
		{"convert_from", []sqltype.ID{sqltype.Bytea, sqltype.Name}, sqltype.Text, []string{"\xe9", "LATIN1"}, "é", expr.Stable},
		{"convert_to", []sqltype.ID{sqltype.Text, sqltype.Name}, sqltype.Bytea, []string{"é", "LATIN1"}, "\xe9", expr.Stable},
		{"encode", []sqltype.ID{sqltype.Bytea, sqltype.Text}, sqltype.Text, []string{"\xe9", "hex"}, "e9", expr.Immutable},
		{"decode", []sqltype.ID{sqltype.Text, sqltype.Text}, sqltype.Bytea, []string{"e9", "hex"}, "\xe9", expr.Immutable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usages := make([]sqltype.Usage, len(tc.ids))
			args := make([]expr.Expr, len(tc.ids))
			var order []int
			for i, id := range tc.ids {
				d := types[id]
				usages[i] = d.Usage()
				var v sqltype.Value
				if id == sqltype.Bytea {
					var err error
					v, err = sqltype.Binary(d, tc.raw[i])
					if err != nil {
						t.Fatal(err)
					}
				} else {
					v = sqltype.String(d, tc.raw[i])
				}
				args[i] = mathObservedArgument{Info: expr.Info{Usage: d.Usage()}, value: v, order: &order, index: i}
			}
			usage := types[tc.result].Usage()
			binaryPlan, err := sqltype.BindBinary(tc.name, usage, usages...)
			if err != nil {
				t.Fatal(err)
			}
			bound := functions.Binary{Info: expr.Info{Usage: usage}, Plan: binaryPlan, Args: args}
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(context.Canceled)
			e.mu.Lock()
			err = e.captureVisibilitySnapshotLocked(scope)
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: hostOf(scope)}}
			got, err := bound.Eval(eval)
			if err != nil {
				t.Fatal(err)
			}
			var raw string
			if tc.result == sqltype.Bytea {
				raw, err = got.Binary()
			} else {
				raw, err = got.Text()
			}
			if err != nil || raw != tc.want {
				t.Fatal(raw, tc.want, err)
			}
			wantOrder := []int{0, 1}
			if len(args) == 3 {
				wantOrder = append(wantOrder, 2)
			}
			if !reflect.DeepEqual(order, wantOrder) {
				t.Fatal(order)
			}
			if exec.PlannedVolatility(bound) != tc.volatility {
				t.Fatal("intrinsic volatility lost")
			}
			for i, arg := range args {
				observed := arg.(mathObservedArgument)
				args[i] = plan.LiteralExpr{Info: observed.Info, Value: observed.value}
			}
			if plan.GenerationImmutable(bound) != (tc.volatility == expr.Immutable) {
				t.Fatal("generation intrinsic volatility lost")
			}
			rebound, changed, err := retagBoundTypes(New().CatalogRead(), bound, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacement[d.ID()] })
			if err != nil || !changed {
				t.Fatal(changed, err)
			}
			next := rebound.(functions.Binary)
			result, err := next.Eval(eval)
			want, retagErr := got.Retag(replacement[tc.result])
			if err != nil || retagErr != nil || result != want {
				t.Fatal(result, want, err, retagErr)
			}
			if bound.Plan.ResultUsage().Type != types[tc.result] || next.Plan.ResultUsage().Type != replacement[tc.result] {
				t.Fatal("descriptor ownership")
			}
		})
	}
}
