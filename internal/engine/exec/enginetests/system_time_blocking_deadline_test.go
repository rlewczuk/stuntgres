package enginetests_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Native cooperative scheduling proof, not PostgreSQL timing/count parity.
// Every case enters a real bound blocking executor through an RLS subquery.
func TestSystemTimeNestedBlockingDeadline(t *testing.T) {
	for _, mode := range []string{"sort", "distinct", "window"} {
		timeRunDeadline(t, mode, func(t *testing.T) {
			scalar := "SELECT f FROM t ORDER BY NOT f, id + 0 LIMIT 1"
			if mode == "distinct" {
				scalar = "SELECT DISTINCT NOT f FROM t"
			} else if mode == "window" {
				scalar = "SELECT first_value(f) OVER (ORDER BY id) AND last_value(f) OVER (ORDER BY id) FROM t LIMIT 1"
			}
			e := engine.New()
			enginetest.Run(t, e, "CREATE EXTENSION tsm_system_time")
			query, captured := enginetest.QueryFixture(t, e, "SELECT *, ("+scalar+") FROM t TABLESAMPLE system_time(100)")
			project := query.Root.(*plan.ProjectNode)
			leaf := project.Child.(*plan.SourceNode)
			subquery := project.Expressions[3].(plan.SubqueryExpr)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			firstCalls, secondCalls, installations := 0, 0, 0
			first := func(original expr.Expr) expr.Expr {
				return enginetest.QueryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
					value, err := original.Eval(c)
					if err != nil {
						return sqltype.Value{}, err
					}
					firstCalls++
					if firstCalls == 1 {
						if err := timeWorkPastBudget(c.Context); err != nil {
							return sqltype.Value{}, err
						}
					}
					// Success after real expiry must stop before the next input.
					return value, nil
				})
			}
			second := func(original expr.Expr) expr.Expr {
				return enginetest.QueryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
					secondCalls++
					return original.Eval(c)
				})
			}
			plan.VisitExecutableQuery(subquery.QueryPlan.Root, func(node plan.Node) {
				switch n := node.(type) {
				case *plan.SortNode:
					if mode == "sort" {
						if len(n.Keys) != 2 || n.Keys[0].Cache < 0 || n.Keys[1].Cache < 0 || n.CacheWidth == 0 {
							t.Fatal("sort fixture did not bind two computed keys")
						}
						n.Keys[0].Expression = first(n.Keys[0].Expression)
						n.Keys[1].Expression = second(n.Keys[1].Expression)
						installations++
					}
				case *plan.DistinctNode:
					if mode == "distinct" {
						n.Expressions[0] = first(n.Expressions[0])
						installations++
					}
				case *plan.WindowNode:
					if mode == "window" {
						if len(n.Calls) != 2 {
							t.Fatal("window fixture did not bind two value calls")
						}
						n.Calls[0].Argument = first(n.Calls[0].Argument)
						n.Calls[1].Argument = second(n.Calls[1].Argument)
						installations++
					}
				}
			}, func(expr.Expr) {})
			if installations != 1 {
				t.Fatal("fixture missed its bound blocking executor", installations)
			}
			rows := make([][]sqltype.Value, 64)
			for i := range rows {
				rows[i] = captured.Row(i % captured.Count())
			}
			plainLeaf := *leaf
			plainLeaf.Sample = nil
			plainProject := *project
			plainProject.Child, plainProject.QuerySchema = &plainLeaf, leaf.Schema()
			plainProject.Expressions = project.Expressions[:3]
			inputRaw := &exec.Source{View: captured.View, Ref: captured.Ref, Anonymous: rows}
			producer, err := exec.OpenNode(ctx, &exec.State{Source: inputRaw}, &plainProject)
			if err != nil {
				t.Fatal(err)
			}
			inputCloses := 0
			finish := producer.Finish
			producer.Finish = func() error {
				inputCloses++
				if finish != nil {
					return finish()
				}
				return nil
			}
			input := &exec.Source{View: captured.View, Ref: captured.Ref, NestedRoot: &plainProject, NestedInputs: inputRaw,
				NestedStage: &producer, Materialized: true, FlatWidth: len(leaf.Schema())}
			defer input.Close()
			policyCalls := 0
			policy := enginetest.QueryProbe(subquery.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
				policyCalls++
				if policyCalls == 1 {
					return sqltype.Boolean(subquery.ResultUsage().Type, true), nil
				}
				return subquery.Eval(c)
			})
			program := &exec.RLSProgram{Groups: []exec.RLSRuntimeGroup{{Permissive: []exec.RLSRuntimeClause{{
				Expression: policy, State: exec.State{Source: input, SourceMode: exec.SourceBorrowed},
			}}}}}
			defer program.Close()
			raw := &exec.Source{View: captured.View, Ref: captured.Ref, Anonymous: rows, RLS: program}
			stage, err := exec.OpenNode(ctx, &exec.State{Source: raw}, leaf)
			defer stage.Close()
			if err != nil || firstCalls != 1 || secondCalls != 0 || policyCalls != 2 {
				t.Fatal("blocking SQL continued after successful expired input", err, firstCalls, secondCalls, policyCalls)
			}
			if stage.Count() != 1 || stage.Closed || stage.Terminal != nil || stage.Source != raw || !reflect.DeepEqual(stage.Source.Row(stage.Index(0)), rows[0]) {
				t.Fatal("blocking unwind lost the accepted source occurrence")
			}
			clause := &program.Groups[0].Permissive[0]
			if clause.Evaluation.Context != nil || clause.Evaluation.Query != nil || clause.State.Source != input {
				t.Fatal("blocking unwind retained invocation scratch or lost capture ownership")
			}
			if producer.Closed || producer.Terminal != nil || producer.Source != inputRaw || inputCloses != 0 || input.NestedStage != &producer || input.FlatCount == 0 {
				t.Fatal("blocking unwind released its borrowed producing capture", inputCloses)
			}
			for i := range input.FlatCount {
				if !reflect.DeepEqual(input.Row(i), rows[i]) {
					t.Fatal("blocking unwind damaged retained captured values", i)
				}
			}
			if err := stage.Close(); err != nil || stage.Source != nil || stage.Selection != nil {
				t.Fatal("sample owner did not release its local stage", err)
			}
			program.Close()
			program.Close()
			if inputCloses != 1 || !producer.Closed || producer.Source != nil || input.NestedStage != nil {
				t.Fatal("capture owner did not close its producer exactly once", inputCloses)
			}
		})
	}
}
