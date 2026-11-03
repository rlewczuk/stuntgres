package enginetests_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestSystemTimeNestedProjectionDeadline(t *testing.T) {
	for _, phase := range []string{"derived", "values", "json_array"} {
		t.Run(phase, func(t *testing.T) {
			for _, mode := range []string{"accepted_prefix", "expired_cancel", "argument_failure", "infinity", "infinity_cancel"} {
				t.Run(mode, func(t *testing.T) {
					base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					ctx := &timeLoopContext{Context: base}
					scalar := "(SELECT bool_and(a) AND bool_or(b) FROM (SELECT f a, NOT f b FROM t OFFSET 0) s)"
					if phase == "values" {
						scalar = "(SELECT bool_and(a) AND bool_or(b) FROM (VALUES (true,false), (false,true)) s(a,b))"
					} else if phase == "json_array" {
						scalar = "JSON_ARRAY(SELECT id FROM t)"
					}
					f := timeNewLoopFixture(t, ctx, "SELECT *, "+scalar+" FROM t TABLESAMPLE system_time(100)")
					policy := f.project.Expressions[3].(plan.SubqueryExpr)
					f.install(policy, true)
					defer f.program.Close()
					firstCalls, nextCalls, installed := 0, 0, 0
					primary := errors.New("genuine nested projection failure after expiry")
					first := func(original expr.Expr) expr.Expr {
						return enginetest.QueryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
							value, err := original.Eval(c)
							if err != nil {
								return sqltype.Value{}, err
							}
							firstCalls++
							if firstCalls == 1 {
								if err := timeWorkPastBudget(base); err != nil {
									return sqltype.Value{}, err
								}
								if mode == "expired_cancel" || mode == "infinity_cancel" {
									cancel()
								}
								if mode == "argument_failure" {
									return sqltype.Value{}, primary
								}
							}
							return value, nil
						})
					}
					second := func(original expr.Expr) expr.Expr {
						return enginetest.QueryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
							nextCalls++
							return original.Eval(c)
						})
					}
					if phase == "json_array" {
						if policy.JSONArray == nil {
							t.Fatal("fixture lost its genuine SQL JSON_ARRAY subquery")
						}
						policy.JSONArray.Input = first(policy.JSONArray.Input)
						ctx.ancestor = "stuntgres/internal/engine/exec.(*Context).subqueryJSONArray"
						ctx.observe = func(caller string, entered bool) {
							if entered && caller == "stuntgres/internal/sqltype.JSONArrayAggregatePlan.Advance" {
								nextCalls++
							}
						}
						installed++
					} else {
						plan.VisitExecutableQuery(policy.QueryPlan.Root, func(node plan.Node) {
							switch n := node.(type) {
							case *plan.DerivedNode:
								if phase == "derived" {
									p := n.Query.Root.(*plan.ProjectNode)
									if len(p.Expressions) != 2 {
										t.Fatal("derived fixture lost its two output expressions")
									}
									p.Expressions[0], p.Expressions[1] = first(p.Expressions[0]), second(p.Expressions[1])
									installed++
								}
							case *plan.ValuesNode:
								if phase == "values" {
									if len(n.Rows) != 2 || len(n.Rows[0]) != 2 {
										t.Fatal("VALUES fixture lost two-field production rows")
									}
									n.Rows[0][0], n.Rows[0][1] = first(n.Rows[0][0]), second(n.Rows[0][1])
									installed++
								}
							}
						}, func(expr.Expr) {})
					}
					if installed != 1 {
						t.Fatal("fixture missed its genuine nested projection", phase, installed)
					}
					leaf := f.leaf
					if mode == "infinity" || mode == "infinity_cancel" {
						leaf = timeArgument(t, leaf, math.Inf(1))
					}
					stage, err := exec.OpenNode(ctx, &exec.State{Source: f.raw}, leaf)
					defer stage.Close()
					if firstCalls == 0 {
						t.Error("nested projection missed its evaluated-value gate")
					}
					if mode == "expired_cancel" || mode == "infinity_cancel" || mode == "argument_failure" {
						want := error(context.Canceled)
						if mode == "argument_failure" {
							want = primary
						}
						if !errors.Is(err, want) || stage.Source != nil {
							t.Error("nested projection genuine failure/cancel became EOF", err)
						}
					} else {
						if err != nil {
							t.Fatal("projection elapsed EOF escaped as statement failure", err)
						}
						if mode == "infinity" {
							if nextCalls == 0 {
								t.Error("Infinity failed to reach subsequent real projection work")
							}
						} else {
							if firstCalls != 1 || nextCalls != 0 {
								t.Error("nested projection advanced after successful expired value", firstCalls, nextCalls)
							}
							f.assertRetained(t, &stage, true)
						}
					}
					clause := &f.program.Groups[0].Permissive[0]
					if clause.Evaluation.Context != nil || clause.Evaluation.Query != nil {
						t.Error("nested projection retained invocation scratch")
					}
					f.close(t, &stage)
				})
			}
		})
	}
}
