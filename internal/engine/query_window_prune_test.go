package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowSurvivingOccurrences(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		inactive  []bool
	}{
		{"dead-lag", "SELECT CASE WHEN FALSE THEN lag(v) OVER(ORDER BY id) ELSE 7 END FROM t ORDER BY id", []bool{true}},
		{"dead-lead-two", "SELECT CASE WHEN FALSE THEN lead(v,1) OVER(ORDER BY id) ELSE 7 END FROM t ORDER BY id", []bool{true}},
		{"dead-lag-three", "SELECT CASE WHEN FALSE THEN lag(v,1,-1) OVER(ORDER BY id) ELSE 7 END FROM t ORDER BY id", []bool{true}},
		{"dead-lead", "SELECT CASE WHEN FALSE THEN lead(v) OVER(ORDER BY id) ELSE 7 END FROM t ORDER BY id", []bool{true}},
		{"dead-lag-two", "SELECT CASE WHEN FALSE THEN lag(v,1) OVER(ORDER BY id) ELSE 7 END FROM t ORDER BY id", []bool{true}},
		{"dead-lead-three", "SELECT CASE WHEN FALSE THEN lead(v,1,-1) OVER(ORDER BY id) ELSE 7 END FROM t ORDER BY id", []bool{true}},
		{"unknown-condition", "SELECT CASE WHEN f THEN lag(v) OVER(ORDER BY id) ELSE 7 END FROM t ORDER BY id", []bool{false}},
		{"bounded-lowered", "SELECT CASE WHEN FALSE THEN lag(v) OVER(ORDER BY id) ELSE 7 END FROM t LIMIT 2", []bool{true}},
		{"empty-surviving", "SELECT lag(v) OVER(ORDER BY id) FROM t WHERE FALSE", []bool{false}},
		{"mixed", "SELECT CASE WHEN FALSE THEN lag(v) OVER(ORDER BY id) ELSE 7 END,lead(v) OVER(ORDER BY id) FROM t", []bool{true, false}},
		{"same-slot-survives", "SELECT CASE WHEN FALSE THEN row_number() OVER() ELSE 7 END,row_number() OVER() FROM t", []bool{false}},
		{"order-only", "SELECT id FROM t ORDER BY lag(v) OVER(ORDER BY id)", []bool{false}},
		{"aggregate-retained", "SELECT CASE WHEN FALSE THEN lag(sum(v)) OVER() ELSE 7 END FROM t", []bool{true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, query, source := queryCoreFixture(t, tc.sql)
			defer source.Close()
			original := pruningTestWindow(t, query.Root)
			calls := append([]plan.WindowCall(nil), original.Calls...)
			for range 2 {
				prepared, err := preflightQuery(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.CatalogRead()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: query.Root.(*plan.ProjectNode).Frame}}, query.Root)
				if err != nil {
					t.Fatal(err)
				}
				window := pruningTestWindow(t, prepared)
				if len(window.Calls) != len(tc.inactive) {
					t.Fatalf("call count %d, want %d", len(window.Calls), len(tc.inactive))
				}
				for i, call := range window.Calls {
					if call.AnalysisOnly != tc.inactive[i] {
						t.Fatalf("call %d inactive=%v, want %v", i, call.AnalysisOnly, tc.inactive[i])
					}
					if call.Expression != calls[i].Expression || call.Routine != calls[i].Routine {
						t.Fatal("pruning changed original slot or routine identity")
					}
				}
				if !reflect.DeepEqual(original.Calls, calls) {
					t.Fatal("execution mutated analyzed calls")
				}
			}
		})
	}
}

func TestQueryWindowPrunedSharedSort(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT CASE WHEN FALSE THEN lag(v) OVER(ORDER BY id) ELSE 7 END,lead(v) OVER(ORDER BY id ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING) FROM t ORDER BY id")
	original := pruningTestWindow(t, query.Root)
	if len(original.Specs) != 2 || original.SortIndices[1] != 0 {
		t.Fatal("fixture does not have distinct frames sharing the first sort")
	}
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || rows == nil || count != 4 {
		t.Fatal("surviving window lost its pruned sibling's shared sort", count, err)
	}
	defer rows.Close()
	var values [2]sqltype.Value
	for _, want := range [][]any{{int64(7), nil}, {int64(7), int64(2)}, {int64(7), int64(1)}, {int64(7), nil}} {
		present, err := rows.Next(context.Background(), values[:])
		got := []any{testExport(values[0]), testExport(values[1])}
		if !present || err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("shared-sort row=%v, present=%v, err=%v; want %v", got, present, err, want)
		}
	}
	if present, err := rows.Next(context.Background(), values[:]); present || err != nil {
		t.Fatalf("shared-sort end present=%v, err=%v", present, err)
	}
	if original.Calls[0].AnalysisOnly || original.Calls[1].AnalysisOnly {
		t.Fatal("execution mutated the analyzed shared-sort definition")
	}
}

func TestQueryWindowGraftedDemand(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		inactive  bool
	}{
		{"surviving", "SELECT row_number() OVER() FROM t", false},
		{"unknown", "SELECT CASE WHEN f THEN row_number() OVER() ELSE 7 END FROM t", false},
		{"dead", "SELECT CASE WHEN FALSE THEN row_number() OVER() ELSE 7 END FROM t", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, query, source := queryCoreFixture(t, tc.sql)
			defer source.Close()
			original := pruningTestWindow(t, query.Root)
			originalCall := original.Calls[0]
			project := query.Root.(*plan.ProjectNode)
			frame := project.Frame + 7
			// Exercise the same physical-layout graft as writable rules while
			// leaving original window call metadata untouched.
			schema := append([]catalog.Column(nil), original.Child.Schema()...)
			schema = append(schema, schema[0])
			input := &plan.EvaluationNode{QuerySchema: plan.QuerySchema(schema), Child: original.Child}
			slots := make(map[int]int)
			grafted, err := ruleGraftSchemas(query.Root, original.Child, input, slots)
			if err != nil {
				t.Fatal(err)
			}
			grafted, err = plan.RewriteQueryExpressions(grafted, func(bound expr.Expr) (expr.Expr, bool, error) {
				return rewriteViewExpression(e.CatalogRead(), bound, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
					if occurrence, ok := expr.(*plan.WindowExpr); ok && occurrence.Frame == project.Frame {
						copy := *occurrence
						copy.Slot = slots[occurrence.Slot]
						copy.Frame = frame
						return &copy, true, nil
					}
					return expr, false, nil
				})
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			copy := *grafted.(*plan.ProjectNode)
			copy.Frame = frame
			pruned, err := analyze.PruneProjectWindows(&copy)
			if err != nil {
				t.Fatal(err)
			}
			call := pruningTestWindow(t, pruned).Calls[0]
			if call.AnalysisOnly != tc.inactive {
				t.Fatalf("grafted window inactive=%v, want %v", call.AnalysisOnly, tc.inactive)
			}
			if call.Expression != originalCall.Expression || call.Routine != originalCall.Routine ||
				!reflect.DeepEqual(original.Calls[0], originalCall) {
				t.Fatal("grafted demand changed original window identity")
			}
		})
	}
}

func pruningTestWindow(t *testing.T, root plan.Node) *plan.WindowNode {
	t.Helper()
	for root != nil {
		if window, ok := root.(*plan.WindowNode); ok {
			return window
		}
		root, _ = plan.QueryInputs(root)
	}
	t.Fatal("missing window stage")
	return nil
}
