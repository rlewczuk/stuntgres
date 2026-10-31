package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQuerySubqueryDemand(t *testing.T) {
	for _, tc := range []struct {
		sql     string
		ignored bool
	}{
		{"SELECT abs(k) FROM a LIMIT 1", true},
		{"SELECT abs(k) FROM a ORDER BY k LIMIT 1", true},
		{"SELECT abs(k) AS needed FROM a ORDER BY needed LIMIT 1", true},
		{"SELECT abs(k) FROM a LIMIT 1 OFFSET 0", false},
		{"SELECT abs(k) FROM a LIMIT 1 OFFSET 1", false},
		{"SELECT abs(k) FROM a ORDER BY k LIMIT 1 OFFSET 1", false},
		{"SELECT count(*) FROM a", false},
		{"SELECT 1 FROM a HAVING TRUE", false},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			e, q, source := queryJoinsFixture(t, tc.sql)
			project := q.Root.(*plan.ProjectNode)
			sentinel := errors.New("final target")
			calls := 0
			probe := enginetest.QueryProbe(project.Schema()[0].Usage, func(*exec.Context) (sqltype.Value, error) { calls++; return sqltype.Value{}, sentinel })
			// An ORDER output owns its cache; replace that key instead of assuming
			// a target ordinal is also an evaluation cache ordinal.
			ordered := false
			for node := project.Child; node != nil; {
				if n, ok := node.(*plan.SortNode); ok {
					for i, key := range n.Keys {
						if key.Output {
							n.Keys[i].Expression = probe
							ordered = true
						}
					}
				}
				node, _ = plan.QueryInputs(node)
			}
			if !ordered {
				subqueryTestTarget(t, project, probe)
			}
			if analyze.CanSimplifyExists(q) != tc.ignored {
				t.Fatal("wrong demand eligibility")
			}
			plan := q
			if tc.ignored {
				var err error
				plan, err = analyze.PresenceQueryPlan(q)
				if err != nil {
					t.Fatal(err)
				}
			}
			parent := exec.State{Source: source}
			c := parent.Evaluation(context.Background())
			c.Frame = 99
			exists := subqueryTestExpression(t, e, plan, parser.SubqueryExists)
			result, err := exists.Eval(&c)
			truth, _ := result.Boolean()
			if tc.ignored {
				if err != nil || !truth || calls != 0 {
					t.Fatal(truth, err, calls)
				}
			} else if err != sentinel || calls == 0 {
				t.Fatal("retained target not demanded", err, calls)
			}
			normal := subqueryTestExpression(t, e, q, parser.SubqueryScalar)
			if _, err = normal.Eval(&c); err != sentinel || calls == 0 {
				t.Fatal("normal target demand lost", err, calls)
			}
		})
	}
	t.Run("rowmark eligibility", func(t *testing.T) {
		_, q, _ := queryJoinsFixture(t, "SELECT abs(k) FROM a FOR UPDATE")
		if analyze.CanSimplifyExists(q) {
			t.Fatal("rowmark target demand stripped")
		}
	})
	t.Run("required derived projection", func(t *testing.T) {
		e, q, source := queryJoinsFixture(t, "SELECT 1 FROM (SELECT abs(k) AS x FROM a) d WHERE x=1 LIMIT 1")
		frames, _ := analyze.CollectQueryFrames(q.Root)
		root := q.Root.(*plan.ProjectNode)
		sentinel := errors.New("required derived target")
		calls := 0
		found := false
		for frame, project := range frames {
			if frame != root.Frame {
				subqueryTestTarget(t, project, enginetest.QueryProbe(project.Schema()[0].Usage, func(*exec.Context) (sqltype.Value, error) { calls++; return sqltype.Value{}, sentinel }))
				found = true
			}
		}
		if !found {
			t.Fatal("missing derived frame")
		}
		presence, err := analyze.PresenceQueryPlan(q)
		if err != nil {
			t.Fatal(err)
		}
		parent := exec.State{Source: source}
		c := parent.Evaluation(context.Background())
		exists := subqueryTestExpression(t, e, presence, parser.SubqueryExists)
		if _, err = exists.Eval(&c); err != sentinel || calls == 0 {
			t.Fatal(err, calls)
		}
	})
}

func TestQuerySubqueryCancellation(t *testing.T) {
	e, q, source := queryJoinsFixture(t, "SELECT abs(k) FROM a LIMIT 1 OFFSET 1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sentinelCalls := 0
	subqueryTestTarget(t, q.Root.(*plan.ProjectNode), enginetest.QueryProbe(q.Root.Schema()[0].Usage, func(*exec.Context) (sqltype.Value, error) {
		sentinelCalls++
		cancel()
		return sqltype.Value{}, context.Canceled
	}))
	parent := exec.State{Source: source}
	c := parent.Evaluation(ctx)
	scalar := subqueryTestExpression(t, e, q, parser.SubqueryScalar)
	if _, err := scalar.Eval(&c); err != context.Canceled || sentinelCalls != 1 {
		t.Fatal(err, sentinelCalls)
	}
	if _, err := scalar.Eval(&c); err != context.Canceled || sentinelCalls != 1 {
		t.Fatal("cancelled reopen demanded data", err, sentinelCalls)
	}
}

func TestQuerySubqueryRowComparison(t *testing.T) {
	e, q, _ := queryJoinsFixture(t, "SELECT k FROM a")
	integer := q.Root.Schema()[0].Usage.Type
	boolean, ok := e.CatalogRead().LookupType(sqltype.Bool)
	if !ok {
		t.Fatal("missing BOOL")
	}
	one, two := subqueryTestInteger(t, integer, 1), subqueryTestInteger(t, integer, 2)
	null := sqltype.Null(integer)
	for _, tc := range []struct {
		operator    sqltype.ComparisonOperator
		text        string
		left, right []sqltype.Value
		want        bool
		unknown     bool
	}{
		{sqltype.CompareEqual, "=", []sqltype.Value{null, one}, []sqltype.Value{one, two}, false, false},
		{sqltype.CompareEqual, "=", []sqltype.Value{null, one}, []sqltype.Value{one, one}, false, true},
		{sqltype.CompareNotEqual, "<>", []sqltype.Value{null, one}, []sqltype.Value{one, two}, true, false},
		{sqltype.CompareLess, "<", []sqltype.Value{one, null}, []sqltype.Value{two, one}, true, false},
		{sqltype.CompareLess, "<", []sqltype.Value{one, null}, []sqltype.Value{one, two}, false, true},
		{sqltype.CompareGreaterEqual, ">=", []sqltype.Value{one, one}, []sqltype.Value{one, one}, true, false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			predicate, err := sqltype.BindComparisonPredicate(tc.operator, integer, integer, boolean, sqltype.CollationRef{}, false)
			if err != nil {
				t.Fatal(err)
			}
			comparison, err := sqltype.BindComparison(integer, integer, sqltype.CollationRef{}, false)
			if err != nil {
				t.Fatal(err)
			}
			bound := plan.SubqueryExpr{Info: expr.Info{Usage: boolean.Usage()}, Operator: tc.text}
			for i := range tc.right {
				bound.Comparisons = append(bound.Comparisons, plan.ComparisonExpr{Right: plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Scope: 1, Frame: 9, Slot: i}, Predicate: predicate})
				if tc.operator > sqltype.CompareNotEqual {
					bound.Ordering = append(bound.Ordering, comparison)
				}
			}
			c := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 9, Row: tc.right}}
			answer, err := bound.Compare(&c, tc.left)
			truth, _ := answer.Boolean()
			if err != nil || answer.IsNull() != tc.unknown || (!tc.unknown && truth != tc.want) {
				t.Fatal(answer, err)
			}
		})
	}
}

func TestQuerySubqueryOperandDemand(t *testing.T) {
	e, q, source := queryJoinsFixture(t, "SELECT k FROM a")
	project := q.Root.(*plan.ProjectNode)
	integer := project.Schema()[0].Usage.Type
	bound := subqueryTestExpression(t, e, q, parser.SubqueryAny)
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, integer, integer, bound.Usage.Type, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	leftCalls, targetCalls := 0, 0
	project.Expressions[0] = enginetest.QueryProbe(integer.Usage(), func(*exec.Context) (sqltype.Value, error) { targetCalls++; return sqltype.Integer(integer, 1) })
	bound.Comparisons = []plan.ComparisonExpr{{Left: enginetest.QueryProbe(integer.Usage(), func(*exec.Context) (sqltype.Value, error) { leftCalls++; return sqltype.Integer(integer, 1) }), Right: plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Scope: 1, Frame: project.Frame, Slot: 0}, Predicate: predicate}}
	parent := exec.State{Source: source}
	c := parent.Evaluation(context.Background())
	for demand := 1; demand <= 2; demand++ {
		answer, err := bound.Eval(&c)
		truth, _ := answer.Boolean()
		if err != nil || !truth || leftCalls != demand || targetCalls != demand {
			t.Fatal("operand repeated, speculative child row, or cached correlated demand", err, leftCalls, targetCalls)
		}
	}
}
