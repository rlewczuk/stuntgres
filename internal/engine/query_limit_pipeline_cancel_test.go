package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryLimitPipelineCancellation(t *testing.T) {
	t.Run("nonproductive join", func(t *testing.T) {
		e, query, source := queryCoreFixture(t, "SELECT id FROM t")
		project := query.Root.(*projectNode)
		bt, _ := e.CatalogRead().LookupType(sqltype.Bool)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		join := &joinNode{querySchema: project.Child.Schema(), Left: project.Child, Right: &sourceNode{Occurrence: 1}, LeftWidth: len(project.Child.Schema()), Predicate: queryProbe(bt.Usage(), func(*EvalContext) (sqltype.Value, error) { calls++; cancel(); return sqltype.Boolean(bt, false), nil })}
		project.Child = &limitNode{querySchema: join.Schema(), Child: join, Count: queryLimitBound(t, e, 1)}
		inputs := &querySource{sources: []*querySource{source, {anonymous: [][]sqltype.Value{{}}}}}
		rows, n, err := finalizeQuery(ctx, project, inputs, nil)
		if rows != nil || n != 0 || err != context.Canceled || calls != 1 || source.view != nil {
			t.Fatal(rows, n, err, calls, source.view)
		}
	})
	for _, mode := range []string{"NULL", "tie"} {
		t.Run("sort comparator "+mode, func(t *testing.T) {
			e, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY v")
			project := query.Root.(*projectNode)
			sort := project.Child.(*sortNode)
			typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
			value := sqltype.Null(typ)
			if mode == "tie" {
				value, _ = sqltype.Integer(typ, 1)
			}
			ctx := &queryCountingContext{Context: context.Background(), remaining: 1000}
			calls := 0
			sort.CacheWidth = 1
			sort.Keys[0].Cache = 0
			sort.Keys[0].Expression = queryProbe(typ.Usage(), func(*EvalContext) (sqltype.Value, error) {
				calls++
				if calls == len(source.committed) {
					ctx.remaining = 8
				}
				return value, nil
			})
			project.Child = &limitNode{querySchema: sort.Schema(), Child: sort, Count: queryLimitBound(t, e, 1)}
			// Cancel sorting NULL/tie keys without pinning an incidental
			// expression invocation count. No prefix may be published.
			rows, n, err := finalizeQuery(ctx, project, source, nil)
			if rows != nil || n != 0 || err != context.Canceled || calls == 0 || source.view != nil {
				t.Fatal(rows, n, err, calls, source.view)
			}
		})
	}
}
