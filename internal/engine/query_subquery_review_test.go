package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQuerySubquerySecondProjectedTuple(t *testing.T) {
	for _, kind := range []parser.SubqueryKind{parser.SubqueryScalar, parser.SubqueryRow} {
		name := "scalar"
		if kind == parser.SubqueryRow {
			name = "row"
		}
		for _, failure := range []string{"valid", "projection", "validation", "cancel"} {
			t.Run(name+"/"+failure, func(t *testing.T) {
				e, q, source := queryJoinsFixture(t, "SELECT k FROM a")
				project := q.Root.(*plan.ProjectNode)
				usage := project.Schema()[0].Usage
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				primary := errors.New("second projection")
				calls := 0
				project.Expressions[0] = queryProbe(usage, func(*EvalContext) (sqltype.Value, error) {
					calls++
					if calls == 2 {
						switch failure {
						case "projection":
							return sqltype.Value{}, primary
						case "validation":
							return sqltype.Value{}, nil
						case "cancel":
							cancel()
							return sqltype.Value{}, context.Canceled
						}
					}
					return sqltype.Integer(usage.Type, 1)
				})
				bound := subqueryTestExpression(t, e, q, kind)
				if kind == parser.SubqueryRow {
					predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, usage.Type, usage.Type, bound.Usage.Type, usage.Collation, false)
					if err != nil {
						t.Fatal(err)
					}
					bound.Comparisons = []plan.ComparisonExpr{{Left: plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: subqueryTestInteger(t, usage.Type, 1)}, Right: plan.ColumnExpr{Info: expr.Info{Usage: usage}, Scope: 1, Frame: project.Frame, Slot: 0}, Predicate: predicate}}
				}
				parent := queryState{source: source}
				c := parent.evaluation(ctx)
				_, err := bound.Eval(&c)
				if calls != 2 {
					t.Fatal("must project exactly two tuples", calls, err)
				}
				switch failure {
				case "projection":
					if err != primary {
						t.Fatal("projection error lost", err)
					}
				case "cancel":
					if !errors.Is(err, context.Canceled) {
						t.Fatal("cancellation lost", err)
					}
				case "validation":
					var diagnostic *sqlstate.Error
					if err == nil || errors.As(err, &diagnostic) && diagnostic.Code == "21000" {
						t.Fatal("second validation skipped", err)
					}
				case "valid":
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "21000" {
						t.Fatal("missing cardinality error", err)
					}
				}
			})
		}
	}
}

func TestQuerySubqueryProjectionScheduling(t *testing.T) {
	_, q, _ := queryJoinsFixture(t, "SELECT k FROM a")
	usage := q.Root.Schema()[0].Usage
	for _, sorting := range []bool{false, true} {
		for _, bounded := range []bool{false, true} {
			var root plan.Node = &plan.SingletonNode{}
			if sorting {
				root = &plan.SortNode{Child: root}
			}
			immutable := queryProbe(usage, func(*EvalContext) (sqltype.Value, error) { return sqltype.Integer(usage.Type, 1) })
			volatile := immutable.(queryProbeExpression)
			volatile.Property.Volatility = expr.Volatile
			projection := []expr.Expr{immutable, volatile}
			b := expressionBinder{}
			next := b.scheduleQueryProjection(root, projection, bounded)
			if sorting {
				sort := root.(*plan.SortNode)
				if len(sort.Outputs) != 1 || sort.CacheWidth != 1 {
					t.Fatal("sort must cache only nonvolatile output", sorting, bounded)
				}
				if _, ok := projection[0].(plan.CachedOrderExpr); !ok {
					t.Fatal("sort projection not reading existing cache")
				}
			}
			if bounded {
				evaluation, ok := next.(*plan.EvaluationNode)
				want := 2
				if sorting {
					want = 1
				}
				if !ok || len(evaluation.Expressions) != want {
					t.Fatal("bounded late evaluation placement changed", sorting)
				}
			} else if next != root {
				t.Fatal("unbounded projection acquired an eager evaluation stage", sorting)
			}
			if !bounded {
				if _, ok := projection[1].(queryProbeExpression); !ok {
					t.Fatal("unbounded volatile target was scheduled early")
				}
			}
		}
	}
}

func TestQuerySubqueryPresenceGraphRetention(t *testing.T) {
	for _, sql := range []string{"SELECT abs(k) FROM a GROUP BY k ORDER BY a.k", "SELECT DISTINCT abs(k) FROM a"} {
		_, q, source := queryJoinsFixture(t, sql)
		project := q.Root.(*plan.ProjectNode)
		if !canSimplifyExists(q) {
			t.Fatal("plain grouping or DISTINCT became a fence", sql)
		}
		calls := 0
		var owner plan.Node
		for node := project.Child; node != nil; node, _ = queryInputs(node) {
			switch n := node.(type) {
			case *plan.AggregateNode:
				owner = n
				n.Keys[0] = queryProbe(n.Keys[0].ResultUsage(), func(*EvalContext) (sqltype.Value, error) {
					calls++
					return sqltype.Value{}, errors.New("ignored group key")
				})
			case *plan.DistinctNode:
				owner = n
				n.Expressions[0] = queryProbe(n.Expressions[0].ResultUsage(), func(*EvalContext) (sqltype.Value, error) {
					calls++
					return sqltype.Value{}, errors.New("ignored distinct target")
				})
			}
		}
		queryPlan, err := presenceQueryPlan(q)
		if err != nil {
			t.Fatal(err)
		}
		seen := false
		for node := queryPlan.Root; node != nil; node, _ = queryInputs(node) {
			switch n := node.(type) {
			case *plan.AggregateNode:
				seen = n.PresenceOnly && len(n.Keys) != 0 && !owner.(*plan.AggregateNode).PresenceOnly
			case *plan.DistinctNode:
				seen = n.PresenceOnly && len(n.Expressions) != 0 && !owner.(*plan.DistinctNode).PresenceOnly
			}
		}
		if !seen {
			t.Fatal("ignored analyzed graph lost or original mutated", sql)
		}
		state := queryState{source: source}
		stage, err := openQueryProjectStage(context.Background(), &state, queryPlan.Root.(*plan.ProjectNode))
		if err != nil {
			t.Fatal(err)
		}
		_, present, err := stage.nextOccurrence(context.Background())
		if err != nil || !present || calls != 0 {
			t.Fatal("ignored execution retained", present, err, calls)
		}
		if err := stage.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQuerySubquerySortedProjectionCacheOnce(t *testing.T) {
	_, q, source := queryJoinsFixture(t, "SELECT abs(k) FROM a ORDER BY a.k")
	project := q.Root.(*plan.ProjectNode)
	calls := 0
	subqueryTestTarget(t, project, queryProbe(project.Schema()[0].Usage, func(c *EvalContext) (sqltype.Value, error) {
		calls++
		return sqltype.Integer(project.Schema()[0].Usage.Type, int64(calls))
	}))
	state := queryState{source: source}
	stage, err := openQueryProjectStage(context.Background(), &state, project)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	for range 2 {
		present, err := stage.nextProjected(context.Background(), nil)
		if err != nil || !present || calls != 4 {
			t.Fatal("sort input projection was skipped or reevaluated", present, err, calls)
		}
	}
}

func TestQuerySubqueryPresenceProperties(t *testing.T) {
	for _, distinct := range []bool{false, true} {
		_, q, _ := queryJoinsFixture(t, "SELECT k FROM a")
		project := q.Root.(*plan.ProjectNode)
		probe := queryProbe(project.Schema()[0].Usage, func(*EvalContext) (sqltype.Value, error) {
			t.Fatal("property visitor evaluated ignored expression")
			return sqltype.Value{}, nil
		}).(queryProbeExpression)
		probe.Property.Volatility = expr.Volatile
		probe.Property.RowDependent = true
		if distinct {
			project.Child = &plan.DistinctNode{Child: project.Child, Expressions: []expr.Expr{probe}}
		} else {
			project.Child = &plan.AggregateNode{Child: project.Child, Keys: []expr.Expr{probe}}
		}
		project.Expressions[0] = probe
		queryPlan, err := presenceQueryPlan(q)
		if err != nil {
			t.Fatal(err)
		}
		properties := queryDemandProperties(queryPlan.Root.(*plan.ProjectNode), true)
		if properties.Volatility == expr.Volatile || properties.RowDependent {
			t.Fatal("ignored graph polluted demand properties", properties)
		}
		nested := plan.SubqueryExpr{Kind: parser.SubqueryExists, QueryPlan: queryPlan, PresenceOnly: true}
		outer := &plan.ProjectNode{Frame: 99, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{nested}}
		if properties := queryDemandProperties(outer, false); properties.Volatility == expr.Volatile || properties.RowDependent {
			t.Fatal("nested ignored target polluted demand properties", properties)
		}
	}
}

func TestQuerySubqueryPresenceRemap(t *testing.T) {
	for _, sql := range []string{"SELECT abs(k) FROM a GROUP BY k", "SELECT DISTINCT k FROM a"} {
		_, q, _ := queryJoinsFixture(t, sql)
		queryPlan, err := presenceQueryPlan(q)
		if err != nil {
			t.Fatal(err)
		}
		original := q.Root.(*plan.ProjectNode).Child
		typ := q.Root.Schema()[0].Usage.Type
		replacement := *typ
		mapped, err := remapViewQuery(New().CatalogRead(), queryPlan.Root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			if old == typ {
				return &replacement
			}
			return old
		})
		if err != nil {
			t.Fatal(err)
		}
		switch n := mapped.(*plan.ProjectNode).Child.(type) {
		case *plan.AggregateNode:
			if !n.PresenceOnly || original.(*plan.AggregateNode).PresenceOnly || n.Keys[0].ResultUsage().Type != &replacement {
				t.Fatal("grouping remap lost demand flag or original graph")
			}
		case *plan.DistinctNode:
			if !n.PresenceOnly || original.(*plan.DistinctNode).PresenceOnly || n.Expressions[0].ResultUsage().Type != &replacement {
				t.Fatal("DISTINCT remap lost demand flag or original graph")
			}
		default:
			t.Fatal("missing remapped presence owner", sql)
		}
	}
}

func TestQuerySubqueryLiteralDemandBoundary(t *testing.T) {
	e, query, source := queryJoinsFixture(t, "SELECT TRUE FROM a")
	child := subqueryTestExpression(t, e, query, parser.SubqueryScalar)
	typ := child.Usage.Type
	info := expr.Info{Usage: typ.Usage()}
	falseLiteral := plan.LiteralExpr{Info: info, Value: sqltype.Boolean(typ, false)}
	trueLiteral := plan.LiteralExpr{Info: info, Value: sqltype.Boolean(typ, true)}
	nullLiteral := plan.LiteralExpr{Info: info, Value: sqltype.Null(typ)}
	parameter := plan.ParameterExpr{Info: info, Number: 1}
	for _, tc := range []struct {
		name        string
		op          parser.LogicalOperator
		left, right expr.Expr
		want        bool
		state       string
	}{
		{"false-left", parser.LogicalAnd, falseLiteral, child, false, ""},
		{"true-left", parser.LogicalOr, trueLiteral, child, true, ""},
		{"null-left", parser.LogicalAnd, nullLiteral, child, false, "21000"},
		{"parameter-left", parser.LogicalAnd, parameter, child, false, "21000"},
		{"false-right", parser.LogicalAnd, child, falseLiteral, false, "21000"},
		{"not", parser.LogicalNot, child, nil, false, "21000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := queryState{source: source}
			c := parent.evaluation(context.Background())
			c.Parameters = []sqltype.Value{sqltype.Boolean(typ, false)}
			expression := plan.LogicalExpr{Info: info, Op: tc.op, Left: tc.left, Right: tc.right, QueryRight: tc.right != nil}
			value, err := expression.Eval(&c)
			if tc.state != "" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != tc.state {
					t.Fatal("nondecisive operand lost child cardinality", err)
				}
				return
			}
			actual, valueErr := value.Boolean()
			if err != nil || valueErr != nil || value.Type() != typ || actual != tc.want {
				t.Fatal(value, err, valueErr)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			c.Context = ctx
			if _, err := expression.Eval(&c); !errors.Is(err, context.Canceled) {
				t.Fatal("decisive literal bypassed cancellation", err)
			}
			c.Context = context.Background()
			expression.Right = nil
			if _, err := expression.Eval(&c); err == nil {
				t.Fatal("decisive literal bypassed logical shape validation")
			}
		})
	}
}

func TestQuerySubqueryLiteralDemandPreflight(t *testing.T) {
	e, query, source := queryJoinsFixture(t, "SELECT FALSE AND ((SELECT TRUE) AND ($1::TEXT)::BOOL)")
	parent := queryState{source: source}
	c := parent.evaluation(context.Background())
	typ, _ := e.CatalogRead().LookupType(sqltype.Text)
	c.Parameters = []sqltype.Value{sqltype.String(typ, "invalid")}
	_, _, err := preflightCoercions(&c, query.Root.(*plan.ProjectNode).Expressions[0])
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" ||
		diagnostic.Message != `invalid input syntax for type boolean: "invalid"` {
		t.Fatal("ordinary coercion preflight bypassed a decisive peer", err)
	}
}

func TestQuerySubqueryLogicalDemandFrames(t *testing.T) {
	for _, tc := range []struct {
		sql        string
		queryRight bool
		want       bool
		state      string
	}{
		{"SELECT FALSE AND (SELECT k FROM a)=1", true, false, ""},
		{"SELECT TRUE OR (SELECT k FROM a)=1", true, true, ""},
		{"SELECT (SELECT TRUE FROM a) AND FALSE", false, false, "21000"},
		{"SELECT FALSE AND CAST(l AS INTEGER)=1 FROM a", false, false, "22P02"},
		{"SELECT TRUE OR CAST(l AS INTEGER)=1 FROM a", false, false, "22P02"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			_, query, source := queryJoinsFixture(t, tc.sql)
			expression := query.Root.(*plan.ProjectNode).Expressions[0].(plan.LogicalExpr)
			if expression.QueryRight != tc.queryRight {
				t.Fatal("logical demand does not match allocated child frames", expression.QueryRight)
			}
			rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
			if tc.state != "" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != tc.state {
					t.Fatal("query-only demand rule changed an eager operand", err)
				}
				return
			}
			if err != nil || count != 1 {
				t.Fatal(count, err)
			}
			defer rows.Close()
			scratch := make([]sqltype.Value, 1)
			present, err := rows.Next(context.Background(), scratch)
			actual, valueErr := scratch[0].Boolean()
			if !present || err != nil || valueErr != nil || actual != tc.want {
				t.Fatal(scratch, present, err, valueErr)
			}
			if present, err := rows.Next(context.Background(), scratch); present || err != nil {
				t.Fatal("logical unit did not end", present, err)
			}
		})
	}
}
