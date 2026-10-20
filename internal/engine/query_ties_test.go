package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryTiesPeerCacheIdentity(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
	one, _ := sqltype.Integer(typ, 1)
	two, _ := sqltype.Integer(typ, 2)
	comparison, err := sqltype.BindOrdering(typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	keys := []plan.SortKey{{Cache: 0, Comparison: comparison}, {Cache: 1, Comparison: comparison}}
	child := exec.Stage{CacheWidth: 2, OrderValues: []sqltype.Value{one, one, two, two, one, one}, CacheOccurrences: []int{2, 1, 0}}
	boundary := make([]sqltype.Value, 2)
	exec.CaptureQueryTiesBoundary(&child, 0, keys, boundary)
	for _, tc := range []struct {
		i    int
		want bool
	}{{0, true}, {1, false}, {2, true}} {
		peer, err := exec.QueryTiesPeer(context.Background(), &child, tc.i, keys, boundary, sqltype.InputEnvironment{})
		if err != nil || peer != tc.want {
			t.Fatal(tc.i, peer, err)
		}
	}
	child.OrderValues[5] = two
	peer, err := exec.QueryTiesPeer(context.Background(), &child, 0, keys, boundary, sqltype.InputEnvironment{})
	if err != nil || peer {
		t.Fatal("boundary retained mutable row header", peer, err)
	}
}

func TestQueryTiesNullAndCancellation(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
	one, _ := sqltype.Integer(typ, 1)
	null := sqltype.Null(typ)
	comparison, err := sqltype.BindOrdering(typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	keys := []plan.SortKey{{Cache: 0, Comparison: comparison}, {Cache: 1, Comparison: comparison}}
	child := exec.Stage{CacheWidth: 2, OrderValues: []sqltype.Value{null, one, null, one, null, null, one, one}}
	boundary := []sqltype.Value{null, one}
	for _, tc := range []struct {
		i    int
		want bool
	}{{0, true}, {1, true}, {2, false}, {3, false}} {
		peer, err := exec.QueryTiesPeer(context.Background(), &child, tc.i, keys, boundary, sqltype.InputEnvironment{})
		if err != nil || peer != tc.want {
			t.Fatal(tc.i, peer, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	peer, err := exec.QueryTiesPeer(ctx, &child, 0, keys, boundary, sqltype.InputEnvironment{})
	if peer || !errors.Is(err, context.Canceled) {
		t.Fatal(peer, err)
	}
}

func TestQueryTiesLookaheadFailureAtomic(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "cancel"}[cancelled], func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY id FETCH FIRST ROW WITH TIES")
			project := query.Root.(*plan.ProjectNode)
			limit := project.Child.(*plan.LimitNode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := project.Expressions[0]
			calls := 0
			sentinel := errors.New("lookahead projection")
			probe := queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
				calls++
				if calls == 2 {
					if cancelled {
						cancel()
						return sqltype.Value{}, context.Canceled
					}
					return sqltype.Value{}, sentinel
				}
				return c.Row[0], nil
			})
			limit.Child = &plan.EvaluationNode{QuerySchema: limit.Child.Schema(), Child: limit.Child, Expressions: []expr.Expr{probe}}
			rows, count, err := exec.Finalize(ctx, query.Root, source, nil)
			want := sentinel
			if cancelled {
				want = context.Canceled
			}
			if rows != nil || count != 0 || !errors.Is(err, want) || calls != 2 {
				t.Fatal(rows, count, err, calls)
			}
		})
	}
}

func TestQueryTiesLockFrontier(t *testing.T) {
	for _, tc := range []struct {
		suffix string
		want   []int64
		holds  []int
	}{
		{"FETCH FIRST 2 ROWS WITH TIES FOR UPDATE", []int64{10, 20, 20}, []int{1, 2, 3, 4}},
		{"OFFSET 1 FETCH FIRST ROW WITH TIES FOR UPDATE", []int64{20, 20}, []int{1, 2, 3, 4}},
		{"FETCH FIRST 0 ROWS WITH TIES FOR UPDATE", nil, nil},
	} {
		t.Run(tc.suffix, func(t *testing.T) {
			e, _, b, c := rowLockEngineFixture(t)
			sessionSQL(t, c, "INSERT INTO t VALUES(3,20),(4,30),(5,40)")
			br := transactionBegin(t, b)
			rowLockViewRows(t, e, b, "SELECT v FROM t ORDER BY v "+tc.suffix, false, tc.want...)
			rowLockViewGrants(t, e, b.Transactions.Ticket, "t", tc.holds...)
			transactionEnd(t, b, br, false)
			lockEmpty(t, e)
		})
	}
}

func TestQueryTiesBorrowedBoundaryAndComparisonFailure(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
	one, _ := sqltype.Integer(typ, 1)
	two, _ := sqltype.Integer(typ, 2)
	comparison, err := sqltype.BindOrdering(typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	source := &exec.Source{Input: &exec.Source{Anonymous: [][]sqltype.Value{{one}, {two}, {one}}}, Layout: []int{0}, Scratch: make([]sqltype.Value, 1)}
	child := exec.Stage{Source: source, Selection: []int{0, 1, 2}}
	keys := []plan.SortKey{{Slot: 0, Cache: -1, Comparison: comparison}}
	boundary := make([]sqltype.Value, 1)
	exec.CaptureQueryTiesBoundary(&child, 0, keys, boundary)
	for _, tc := range []struct {
		i    int
		want bool
	}{{1, false}, {2, true}} {
		peer, err := exec.QueryTiesPeer(context.Background(), &child, tc.i, keys, boundary, sqltype.InputEnvironment{})
		if err != nil || peer != tc.want {
			t.Fatal("borrowed boundary", tc.i, peer, err)
		}
	}
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	boundary[0] = sqltype.Boolean(boolean, true)
	peer, err := exec.QueryTiesPeer(context.Background(), &child, 2, keys, boundary, sqltype.InputEnvironment{})
	if peer || err == nil {
		t.Fatal("provider failure lost", peer, err)
	}
}
