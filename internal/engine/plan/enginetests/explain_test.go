package enginetests

import (
	"math"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestExplainEstimates(t *testing.T) {
	var integer, boolean *sqltype.Descriptor
	for _, descriptor := range sqltype.Builtins() {
		switch descriptor.ID() {
		case sqltype.Int8:
			integer = descriptor
		case sqltype.Bool:
			boolean = descriptor
		}
	}
	literal := func(value int64) expr.Expr {
		v, err := sqltype.Integer(integer, value)
		if err != nil {
			t.Fatal(err)
		}
		return plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: v}
	}
	truth := func(value bool) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, value)}
	}
	parameter := plan.ParameterExpr{Info: expr.Info{Usage: integer.Usage()}, Number: 1}
	values := plan.ExplainEstimate{Rows: 4, Cost: 4}
	cases := []struct {
		name               string
		node               plan.Node
		children           []plan.ExplainEstimate
		source, rows, cost float64
	}{
		{"singleton", &plan.SingletonNode{}, nil, 1000, 1, 1},
		{"values", &plan.ValuesNode{Rows: make([][]expr.Expr, 4)}, nil, 1000, 4, 4},
		{"source-fallback", &plan.SourceNode{}, nil, 1000, 1000, 1000},
		{"source-statistic", &plan.SourceNode{}, nil, 37, 37, 37},
		{"source-empty-statistic", &plan.SourceNode{}, nil, 0, 0, 0},
		{"filter-unknown", &plan.FilterNode{Predicate: parameter}, []plan.ExplainEstimate{values}, 0, 2, 4.04},
		{"filter-true", &plan.FilterNode{Predicate: truth(true)}, []plan.ExplainEstimate{values}, 0, 4, 4.04},
		{"filter-false", &plan.FilterNode{Predicate: truth(false)}, []plan.ExplainEstimate{values}, 0, 0, 4.04},
		{"filter-null", &plan.FilterNode{Predicate: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Null(boolean)}}, []plan.ExplainEstimate{values}, 0, 0, 4.04},
		{"project-filter", &plan.ProjectNode{}, []plan.ExplainEstimate{{Rows: 2, Cost: 4.04}}, 0, 2, 4.06},
		{"sort-filter", &plan.SortNode{}, []plan.ExplainEstimate{{Rows: 2, Cost: 4.04}}, 0, 2, 4.06},
		{"sort-source", &plan.SortNode{}, []plan.ExplainEstimate{{Rows: 1000, Cost: 1000}}, 0, 1000, 1000 + 10*math.Log2(1000)},
		{"distinct", &plan.DistinctNode{}, []plan.ExplainEstimate{values}, 0, 2, 4.04},
		{"global-empty", &plan.AggregateNode{}, []plan.ExplainEstimate{{}}, 0, 1, 0},
		{"grouped-empty", &plan.AggregateNode{Keys: []expr.Expr{parameter}}, []plan.ExplainEstimate{{}}, 0, 0, 0},
		{"grouping-sets", &plan.AggregateNode{Keys: []expr.Expr{parameter}, Sets: [][]int{nil, {0}, {0}}}, []plan.ExplainEstimate{values}, 0, 5, 4.04},
		{"cross", &plan.JoinNode{}, []plan.ExplainEstimate{values, values}, 0, 16, 8.16},
		{"join-predicate", &plan.JoinNode{Predicate: parameter}, []plan.ExplainEstimate{values, values}, 0, 8, 8.16},
		{"left-empty-right", &plan.JoinNode{Kind: parser.JoinLeft}, []plan.ExplainEstimate{values, {}}, 0, 4, 4},
		{"right-empty-left", &plan.JoinNode{Kind: parser.JoinRight}, []plan.ExplainEstimate{{}, values}, 0, 4, 4},
		{"full-empty-side", &plan.JoinNode{Kind: parser.JoinFull}, []plan.ExplainEstimate{values, {}}, 0, 4, 4},
		{"union-all", &plan.SetNode{Operation: "UNION", All: true}, []plan.ExplainEstimate{values, values}, 0, 8, 8.08},
		{"union-distinct", &plan.SetNode{Operation: "UNION"}, []plan.ExplainEstimate{values, values}, 0, 4, 8.08},
		{"intersect", &plan.SetNode{Operation: "INTERSECT", All: true}, []plan.ExplainEstimate{values, values}, 0, 2, 8.08},
		{"except", &plan.SetNode{Operation: "EXCEPT"}, []plan.ExplainEstimate{values, values}, 0, 2, 8.08},
		{"literal-limit", &plan.LimitNode{Count: literal(1)}, []plan.ExplainEstimate{{Rows: 3, Cost: 3}}, 0, 1, 3.03},
		{"offset", &plan.LimitNode{Offset: literal(3)}, []plan.ExplainEstimate{values}, 0, 1, 4.04},
		{"offset-overrun", &plan.LimitNode{Offset: literal(9)}, []plan.ExplainEstimate{values}, 0, 0, 4.04},
		{"unknown-limit", &plan.LimitNode{Count: parameter}, []plan.ExplainEstimate{values}, 0, 4, 4.04},
		{"null-limit", &plan.LimitNode{Count: plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: sqltype.Null(integer)}}, []plan.ExplainEstimate{values}, 0, 4, 4.04},
		{"ties", &plan.LimitNode{Count: literal(1), WithTies: true}, []plan.ExplainEstimate{values}, 0, 4, 4.04},
		{"inactive-sort", &plan.SortNode{PresenceOnly: true}, []plan.ExplainEstimate{values}, 0, 4, 4},
		{"inactive-grouping", &plan.AggregateNode{Keys: []expr.Expr{parameter}, PresenceOnly: true}, []plan.ExplainEstimate{values}, 0, 4, 4},
		{"inactive-distinct", &plan.DistinctNode{PresenceOnly: true}, []plan.ExplainEstimate{values}, 0, 4, 4},
		{"inactive-distinct-on", &plan.DistinctOnNode{PresenceOnly: true}, []plan.ExplainEstimate{values}, 0, 4, 4},
		{"inactive-limit", &plan.LimitNode{Count: literal(0), PresenceOnly: true}, []plan.ExplainEstimate{values}, 0, 4, 4},
		{"analysis-only-window", &plan.WindowNode{Calls: []plan.WindowCall{{AnalysisOnly: true}}}, []plan.ExplainEstimate{values}, 0, 4, 4},
		{"recursive", &plan.RecursiveUnionNode{}, []plan.ExplainEstimate{values, values}, 0, 44, 8.4},
		{"recursive-saturation", &plan.RecursiveUnionNode{}, []plan.ExplainEstimate{{Rows: 1e12, Cost: 1e12}, {Rows: 1e12, Cost: 1e12}}, 0, 1e12, 1e12},
		{"srf-lockstep", &plan.SetProjectNode{Calls: []expr.Expr{parameter, parameter}}, []plan.ExplainEstimate{values}, 0, 4000, 4.04},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := plan.EstimateExplainNode(tc.node, tc.children, tc.source)
			if err != nil || got.Rows != tc.rows || math.Abs(got.Cost-tc.cost) > 1e-9 {
				t.Fatalf("estimate = %+v, %v; want rows=%g cost=%g", got, err, tc.rows, tc.cost)
			}
			if got.RoundedRows() != math.Ceil(tc.rows) {
				t.Fatal("display did not round upward", got)
			}
		})
	}
	for _, returning := range []bool{false, true} {
		got := plan.ExplainMutationEstimate(values, returning)
		rows := float64(0)
		if returning {
			rows = 4
		}
		if got.Rows != rows || math.Abs(got.Cost-4.08) > 1e-9 {
			t.Fatal("mutation candidate/output estimate", returning, got)
		}
	}
}

func TestExplainNodeCoverage(t *testing.T) {
	// Enumerate the actual closed node family. SQL-facing coverage separately
	// binds these families through supported queries and DML commands.
	for _, node := range []plan.Node{
		&plan.SourceNode{}, &plan.SingletonNode{}, &plan.ValuesNode{}, &plan.FilterNode{},
		&plan.SortNode{}, &plan.ProjectNode{}, &plan.EvaluationNode{}, &plan.AggregateNode{},
		&plan.DistinctNode{}, &plan.DistinctOnNode{}, &plan.JoinNode{}, &plan.DerivedNode{},
		&plan.SetNode{Operation: "UNION"}, &plan.RecursiveUnionNode{}, &plan.WorktableScanNode{},
		&plan.LimitNode{}, &plan.RowLockNode{}, &plan.WindowNode{}, &plan.FunctionFromNode{}, &plan.SetProjectNode{},
	} {
		got, err := plan.EstimateExplainNode(node, []plan.ExplainEstimate{{Rows: 4, Cost: 4}, {Rows: 2, Cost: 2}}, 1000)
		if err != nil || math.IsNaN(got.Rows) || math.IsInf(got.Cost, 0) || got.Rows < 0 || got.Cost < 0 {
			t.Fatalf("closed node %T: %+v, %v", node, got, err)
		}
	}
	if _, err := plan.EstimateExplainNode(plan.QuerySchema(nil), nil, 1000); err == nil {
		t.Fatal("schema-only value silently treated as executable node")
	}
}
