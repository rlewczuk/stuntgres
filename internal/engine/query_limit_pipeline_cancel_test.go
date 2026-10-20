package engine

import (
	"context"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryLimitPipelineCancellation(t *testing.T) {
	t.Run("nonproductive join", func(t *testing.T) {
		e, query, source := queryCoreFixture(t, "SELECT id FROM t")
		project := query.Root.(*plan.ProjectNode)
		bt, _ := e.CatalogRead().LookupType(sqltype.Bool)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		join := &plan.JoinNode{QuerySchema: project.Child.Schema(), Left: project.Child, Right: &plan.SourceNode{Occurrence: 1}, LeftWidth: len(project.Child.Schema()), Predicate: queryProbe(bt.Usage(), func(*exec.Context) (sqltype.Value, error) { calls++; cancel(); return sqltype.Boolean(bt, false), nil })}
		project.Child = &plan.LimitNode{QuerySchema: join.Schema(), Child: join, Count: queryLimitBound(t, e, 1)}
		inputs := &exec.Source{Sources: []*exec.Source{source, {Anonymous: [][]sqltype.Value{{}}}}}
		rows, n, err := exec.Finalize(ctx, project, inputs, nil)
		if rows != nil || n != 0 || err != context.Canceled || calls != 1 || source.View != nil {
			t.Fatal(rows, n, err, calls, source.View)
		}
	})
	for _, mode := range []string{"NULL", "tie"} {
		t.Run("sort comparator "+mode, func(t *testing.T) {
			e, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY v")
			project := query.Root.(*plan.ProjectNode)
			sort := project.Child.(*plan.SortNode)
			typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
			value := sqltype.Null(typ)
			if mode == "tie" {
				value, _ = sqltype.Integer(typ, 1)
			}
			ctx := &queryCountingContext{Context: context.Background(), remaining: 1000}
			calls := 0
			sort.CacheWidth = 1
			sort.Keys[0].Cache = 0
			sort.Keys[0].Expression = queryProbe(typ.Usage(), func(*exec.Context) (sqltype.Value, error) {
				calls++
				if calls == len(source.Committed) {
					ctx.remaining = 8
				}
				return value, nil
			})
			project.Child = &plan.LimitNode{QuerySchema: sort.Schema(), Child: sort, Count: queryLimitBound(t, e, 1)}
			// Cancel sorting NULL/tie keys without pinning an incidental
			// expression invocation count. No prefix may be published.
			rows, n, err := exec.Finalize(ctx, project, source, nil)
			if rows != nil || n != 0 || err != context.Canceled || calls == 0 || source.View != nil {
				t.Fatal(rows, n, err, calls, source.View)
			}
		})
	}
}
