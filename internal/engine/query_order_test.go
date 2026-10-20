package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestQueryOrderCacheAndCancellation(t *testing.T) {
	for _, failure := range []string{"none", "late-key", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT id FROM t WHERE f ORDER BY id")
			project := query.Root.(*plan.ProjectNode)
			sorted := project.Child.(*plan.SortNode)
			usage := project.Schema()[0].Usage
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("late ordering key")
			key := queryProbe(usage, func(c *exec.Context) (sqltype.Value, error) {
				calls++
				if calls == 2 && failure == "late-key" {
					return sqltype.Value{}, sentinel
				}
				if calls == 2 && failure == "cancel" {
					cancel()
				}
				return c.Row[0], nil
			})
			provider, err := sqltype.BindOrdering(usage)
			if err != nil {
				t.Fatal(err)
			}
			sorted.Keys = []plan.SortKey{{Expression: key, Comparison: provider, Slot: -1, Cache: 0, Desc: true}}
			sorted.CacheWidth = 1
			project.Expressions = []expr.Expr{plan.CachedOrderExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{StageDependent: true}}, Slot: 0}}
			rows, count, err := exec.Finalize(ctx, query.Root, source, nil)
			if failure != "none" {
				want := error(sentinel)
				if failure == "cancel" {
					want = context.Canceled
				}
				if err != want || rows != nil || count != 0 || calls != 2 || source.View != nil {
					t.Fatal(rows, count, err, calls, source.View)
				}
				return
			}
			if err != nil || count != 2 || calls != 2 {
				t.Fatal(count, err, calls)
			}
			defer rows.Close()
			dst := make([]sqltype.Value, 1)
			for _, want := range []int64{4, 1} {
				present, err := rows.Next(context.Background(), dst)
				if err != nil || !present {
					t.Fatal(present, err)
				}
				v, err := sqltype.Integer(usage.Type, want)
				if err != nil || !sqltype.SameDatum(dst[0], v) {
					t.Fatal(dst, want, err)
				}
			}
			if calls != 2 {
				t.Fatal("final cursor reevaluated key", calls)
			}
		})
	}
}

func TestQueryOrderValuesProducer(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE d(id INT,a INT)")
	r := run(t, e, "INSERT INTO d VALUES(1,2),(2,1),(3,NULL),(4,2) ORDER BY column2+0 DESC NULLS LAST,column1")
	if r.Count != 4 {
		t.Fatal(r)
	}
	got := tuples(run(t, e, "SELECT id,a FROM d ORDER BY id"))
	// Storage traversal isn't the ordering contract; explicitly sort the read.
	if !reflect.DeepEqual(got, [][]driver.Value{{int64(1), int64(2)}, {int64(2), int64(1)}, {int64(3), nil}, {int64(4), int64(2)}}) {
		t.Fatal(got)
	}
}
