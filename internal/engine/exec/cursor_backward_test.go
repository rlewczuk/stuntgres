package exec

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"testing"
)

func TestPLpgSQLCursorBackwardCapability(t *testing.T) {
	scan := &plan.SourceNode{}
	singleton := &plan.SingletonNode{}
	literal := plan.LiteralExpr{}
	cases := []struct {
		name   string
		root   plan.Node
		source *Source
		want   bool
	}{
		{"scan", scan, &Source{}, true},
		{"singleton", singleton, nil, false},
		{"one_values", &plan.ValuesNode{Rows: [][]expr.Expr{{literal}}}, nil, false},
		{"many_values", &plan.ValuesNode{Rows: [][]expr.Expr{{literal}, {literal}}}, nil, true},
		{"from_srf", &plan.FunctionFromNode{Calls: []expr.Expr{plan.ParameterExpr{}}}, nil, true},
		{"folded_from_scalar", &plan.FunctionFromNode{Calls: []expr.Expr{literal}}, nil, false},
		{"target_srf", &plan.SetProjectNode{}, nil, false},
		{"join", &plan.JoinNode{}, nil, false},
		{"aggregate", &plan.AggregateNode{Child: scan}, &Source{}, false},
		{"presence_aggregate", &plan.AggregateNode{Child: scan, PresenceOnly: true}, &Source{}, true},
		{"window", &plan.WindowNode{}, nil, false},
		{"distinct", &plan.DistinctNode{Child: scan, Expressions: []expr.Expr{plan.ColumnExpr{}}}, &Source{}, false},
		{"constant_distinct", &plan.DistinctNode{Child: scan, Expressions: []expr.Expr{literal}}, &Source{}, true},
		{"constant_distinct_result", &plan.DistinctNode{Child: singleton, Expressions: []expr.Expr{literal}}, nil, false},
		{"distinct_on", &plan.DistinctOnNode{Child: scan}, &Source{}, false},
		{"recursive_union", &plan.RecursiveUnionNode{}, nil, false},
		{"worktable", &plan.WorktableScanNode{}, nil, false},
		{"derived_scan", &plan.DerivedNode{Query: &plan.Query{Root: scan}}, &Source{}, true},
		{"derived_aggregate", &plan.DerivedNode{Query: &plan.Query{Root: &plan.AggregateNode{}}}, nil, false},
		{"materialized_cte", &plan.DerivedNode{CTE: &plan.CTEDefinition{Hint: parser.CTEMaterialized}, Query: &plan.Query{Root: &plan.AggregateNode{}}}, nil, true},
		{"append_scans", &plan.SetNode{Operation: "union", All: true, Left: &plan.ProjectNode{Child: scan}, Right: &plan.ProjectNode{Child: scan}}, &Source{}, true},
		{"append_mixed", &plan.SetNode{Operation: "union", All: true, Left: &plan.ProjectNode{Child: scan}, Right: &plan.ProjectNode{Child: singleton}}, &Source{}, false},
		{"union_distinct", &plan.SetNode{Operation: "union"}, nil, false},
		{"view_scan", scan, &Source{Materialized: true, NestedRoot: scan, NestedInputs: &Source{}}, true},
		{"view_aggregate", scan, &Source{Materialized: true, NestedRoot: &plan.AggregateNode{}, NestedInputs: &Source{}}, false},
		{"input_bridge", scan, &Source{Input: &Source{NestedRoot: singleton}}, false},
		{"foreign_cached", scan, &Source{Foreign: &ForeignQuerySource{}, Materialized: true}, false},
		{"unknown", plan.QuerySchema{}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SupportsBackwardCursor(tc.root, tc.source); got != tc.want {
				t.Fatalf("capability=%t want %t", got, tc.want)
			}
			if got := SupportsBackwardCursor(&plan.SortNode{Child: tc.root}, tc.source); !got {
				t.Fatal("active full sort must supply capability")
			}
			if got := SupportsBackwardCursor(&plan.SortNode{Child: tc.root, PresenceOnly: true}, tc.source); got != tc.want {
				t.Fatal("ignored sort changed capability")
			}
			for _, wrapped := range []plan.Node{&plan.ProjectNode{Child: tc.root}, &plan.FilterNode{Child: tc.root}, &plan.EvaluationNode{Child: tc.root}, &plan.LimitNode{Child: tc.root}, &plan.RowLockNode{Child: tc.root}} {
				if got := SupportsBackwardCursor(wrapped, tc.source); got != tc.want {
					t.Fatalf("wrapper %T changed capability", wrapped)
				}
			}
		})
	}
	bundle := &Source{Sources: []*Source{{}, {NestedRoot: &plan.AggregateNode{}, NestedInputs: &Source{}}}}
	if SupportsBackwardCursor(&plan.SourceNode{Occurrence: 1}, bundle) {
		t.Fatal("nested occurrence classified through wrong input bundle")
	}
	root := &plan.ProjectNode{Child: &plan.SortNode{Child: &plan.JoinNode{}}}
	if allocations := testing.AllocsPerRun(10, func() {
		if !SupportsBackwardCursor(root, nil) {
			panic("unexpected capability")
		}
	}); allocations != 0 {
		t.Fatal("classifier allocates", allocations)
	}
}
