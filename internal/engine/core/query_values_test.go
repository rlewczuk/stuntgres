package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestQueryFromValuesMixedDemandAndCancellation(t *testing.T) {
	for _, mode := range []string{"complete", "limit0", "cancel", "error"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			query, source := enginetest.QueryFixture(t, e, "SELECT id FROM t")
			project := query.Root.(*plan.ProjectNode)
			usage := project.Schema()[0].Usage
			calls := []int{0, 0, 0}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("VALUES cell failure")
			cells := make([][]expr.Expr, 3)
			for i := range cells {
				index := i
				cells[i] = []expr.Expr{enginetest.QueryProbe(usage, func(c *exec.Context) (sqltype.Value, error) {
					calls[index]++
					if index == 1 && mode == "cancel" {
						cancel()
					}
					if index == 1 && mode == "error" {
						return sqltype.Value{}, failure
					}
					return sqltype.Integer(usage.Type, 7)
				})}
			}
			values := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Name: "column1", Usage: usage}}, Rows: cells}
			width := len(project.Child.Schema())
			schema := append(append(plan.QuerySchema(nil), project.Child.Schema()...), values.Schema()...)
			join := &plan.JoinNode{QuerySchema: schema, Left: project.Child, Right: values, LeftWidth: width, RightWidth: 1}
			project.Child = join
			project.QuerySchema = plan.QuerySchema{{Name: "n", Usage: usage}}
			project.Expressions = []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: width}}
			if mode == "limit0" {
				typ, _ := e.CatalogRead().LookupType(sqltype.Int8)
				zero, err := sqltype.Integer(typ, 0)
				if err != nil {
					t.Fatal(err)
				}
				project.Child = &plan.LimitNode{QuerySchema: schema, Child: join, Count: plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: zero}}
			}
			rows, count, err := exec.Finalize(ctx, project, source, nil)
			if mode == "cancel" || mode == "error" {
				want := failure
				if mode == "cancel" {
					want = context.Canceled
				}
				if rows != nil || count != 0 || !errors.Is(err, want) || source.View != nil || !reflect.DeepEqual(calls, []int{1, 1, 0}) {
					t.Fatal(rows, count, err, source.View, calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			wantCount, wantCalls := 12, []int{1, 1, 1}
			if mode == "limit0" {
				wantCount, wantCalls = 0, []int{0, 0, 0}
			}
			if count != wantCount || source.View != nil || !reflect.DeepEqual(calls, wantCalls) {
				t.Fatal(count, source.View, calls)
			}
		})
	}
}

func TestQueryFromValuesLockRecheckKeepsSyntheticOccurrence(t *testing.T) {
	e := engine.New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
	usage := typ.Usage()
	calls := 0
	values := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Name: "column1", Usage: usage}}, Rows: [][]expr.Expr{{enginetest.QueryProbe(usage, func(*exec.Context) (sqltype.Value, error) { calls++; return sqltype.Integer(typ, 99) })}}}
	physical := &plan.SourceNode{QuerySchema: plan.QuerySchema{{Name: "n", Usage: usage}}, Occurrence: 0}
	join := &plan.JoinNode{QuerySchema: append(append(plan.QuerySchema(nil), physical.Schema()...), values.Schema()...), Left: physical, Right: values, LeftWidth: 1, RightWidth: 1}
	integer := func(n int64) sqltype.Value {
		value, err := sqltype.Integer(typ, n)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	frozen := []sqltype.Value{integer(1), integer(7)}
	current := [][]sqltype.Value{{integer(2)}}
	inputs := &exec.Source{Occurrence: 0}
	scope := &core.StatementScope{QueryLockContext: &exec.LockContext{Leaves: []exec.QueryLockLeaf{{}}}}
	scratch := make([]sqltype.Value, exec.QueryLockRecheckWidth(join, inputs))
	offset := 0
	row, present, err := exec.RecheckLockedQuery(join, inputs, current, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}, scratch, &offset, frozen)
	if err != nil || !present || calls != 0 {
		t.Fatal(row, present, err, calls)
	}
	left, _ := row[0].Integer()
	right, _ := row[1].Integer()
	if left != 2 || right != 7 {
		t.Fatal("synthetic cell reevaluated during physical recheck", row)
	}
}
