package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestQueryDistinctOnRequiredCloseAndPrimaryError(t *testing.T) {
	e := engine.New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	usage := integer.Usage()
	one, _ := sqltype.Integer(integer, 1)
	schema := plan.QuerySchema{{Name: "id", Usage: usage}}
	family, err := sqltype.BindGrouping(usage)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"close", "key", "sort"} {
		closeFailure := errors.New("ON child close")
		primary := errors.New("ON key or sort primary")
		closed := 0
		inner := &exec.Stage{Source: &exec.Source{Anonymous: [][]sqltype.Value{{one}}}, Columns: schema, Expressions: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: one}}, Evaluation: exec.Context{}, Finish: func() error { closed++; return closeFailure }}
		bridge := &exec.Source{Materialized: true, FlatWidth: 1, NestedRoot: &plan.ProjectNode{QuerySchema: schema, Child: &plan.SingletonNode{}}, NestedStage: inner}
		source := &exec.Source{Sources: []*exec.Source{bridge}}
		key := enginetest.QueryProbe(usage, func(c *exec.Context) (sqltype.Value, error) {
			if phase == "key" {
				return sqltype.Value{}, primary
			}
			return c.Row[0], nil
		})
		var child plan.Node = &plan.SourceNode{QuerySchema: schema, Occurrence: 0}
		if phase == "sort" {
			comparison, err := sqltype.BindOrdering(usage)
			if err != nil {
				t.Fatal(err)
			}
			child = &plan.SortNode{QuerySchema: schema, Child: child, CacheWidth: 1, Keys: []plan.SortKey{{Cache: 0, Slot: -1, Comparison: comparison, Expression: enginetest.QueryProbe(usage, func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, primary })}}}
		}
		node := &plan.DistinctOnNode{QuerySchema: schema, Child: child, Expressions: []expr.Expr{key}, Grouping: []plan.Grouping{{Grouping: family}}}
		root := &plan.ProjectNode{QuerySchema: schema, Child: node, Expressions: []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: usage}, Scope: 1, Frame: 1, Slot: 0}}}
		rows, count, err := exec.Finalize(context.Background(), root, source, nil)
		want := error(primary)
		if phase == "close" {
			want = closeFailure
		}
		if rows != nil || count != 0 || !errors.Is(err, want) || closed != 1 {
			t.Fatal(phase, rows, count, err, want, closed)
		}
	}
}
