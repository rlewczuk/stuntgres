package enginetests_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"runtime"
	"testing"
	"testing/synctest"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Observe real executor work through ordinary host cancellation checkpoints. Err
// always returns the embedded context's real error; elapsed EOF is never faked.
// Only the private checkpoint's transparent plumbing frame is skipped, as in
// the window phase observers. Input closures are NOT treated as output work.
type timeLoopContext struct {
	context.Context
	caller       string
	ancestor     string
	observe      func(caller string, membership bool)
	observeStack func(stack []runtime.Frame, membership bool)
	stack        [48]runtime.Frame
}

func (c *timeLoopContext) Err() error {
	if c.observe == nil && c.observeStack == nil {
		return c.Context.Err()
	}
	var pcs [48]uintptr
	limit := len(pcs)
	if c.caller != "" && c.ancestor == "" && c.observeStack == nil {
		// Only sampleCheckpoint may precede the logical caller. This
		// observer needs neither an ancestor nor a retained stack.
		limit = 2
	}
	n := runtime.Callers(2, pcs[:limit])
	frames := runtime.CallersFrames(pcs[:n])
	caller, membership := "", false
	// Reuse observer storage; Err samples must not allocate another frame array.
	depth := 0
	for {
		frame, more := frames.Next()
		if caller == "" && frame.Function != "stuntgres/internal/engine/exec.sampleCheckpoint" {
			caller = frame.Function
			if c.caller != "" && caller != c.caller {
				// Do not symbolize unrelated input/support stacks inside the
				// real scan budget. The checkpoint still returns the real error.
				return c.Context.Err()
			}
			if c.caller != "" && c.ancestor == "" && c.observeStack == nil {
				c.observe(caller, false)
				return c.Context.Err()
			}
		}
		if c.observeStack != nil && depth < len(c.stack) && frame.Function != "stuntgres/internal/engine/exec.sampleCheckpoint" {
			c.stack[depth] = frame
			depth++
		}
		if frame.Function == "stuntgres/internal/engine/exec.(*Context).subqueryHashMembership" || c.ancestor != "" && frame.Function == c.ancestor {
			membership = true
		}
		if !more {
			break
		}
	}
	if c.observe != nil {
		c.observe(caller, membership)
	}
	if c.observeStack != nil {
		c.observeStack(c.stack[:depth], membership)
	}
	return c.Context.Err()
}

// Grouping.Hash and Grouping.Equal check the host context both on entry and
// successful return. Their first observed callsite is the genuine admission
// checkpoint; count subsequent visits to that PC, not every Err sample within
// the same indivisible support invocation. No source line numbers are pinned.
func timeSupportAdmission(frame runtime.Frame, function string, entry *uintptr) bool {
	if frame.Function != function {
		return false
	}
	if *entry == 0 {
		*entry = frame.PC
	}
	return frame.PC == *entry
}

// Keep a genuine producing capture separate from the RLS relation. Its owner,
// not a borrowed scalar invocation, is responsible for closing the producer.
type timeLoopFixture struct {
	project              *plan.ProjectNode
	leaf                 *plan.SourceNode
	rows                 [][]sqltype.Value
	raw, input, inputRaw *exec.Source
	producer             exec.Stage
	closes               int
	owner                *exec.CTEInvocation
	program              *exec.RLSProgram
}

func timeNewLoopFixture(t *testing.T, ctx context.Context, sql string) *timeLoopFixture {
	t.Helper()
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION tsm_system_time")
	enginetest.Run(t, e, "CREATE EXTENSION tsm_system_rows")
	query, captured := enginetest.QueryFixture(t, e, sql)
	f := &timeLoopFixture{project: query.Root.(*plan.ProjectNode)}
	f.leaf = f.project.Child.(*plan.SourceNode)
	f.rows = make([][]sqltype.Value, 64)
	for i := range f.rows {
		f.rows[i] = captured.Row(i % captured.Count())
	}
	plainLeaf, plainProject := *f.leaf, *f.project
	plainLeaf.Sample = nil
	plainProject.Child, plainProject.QuerySchema = &plainLeaf, f.leaf.Schema()
	plainProject.Expressions = f.project.Expressions[:3]
	f.inputRaw = &exec.Source{View: captured.View, Ref: captured.Ref, Anonymous: f.rows}
	var err error
	state := &exec.State{Source: f.inputRaw}
	f.producer, err = exec.OpenNode(ctx, state, &plainProject)
	f.owner = state.Ctes
	if err != nil {
		t.Fatal(err)
	}
	finish := f.producer.Finish
	f.producer.Finish = func() error {
		f.closes++
		if finish != nil {
			return finish()
		}
		return nil
	}
	f.input = &exec.Source{View: captured.View, Ref: captured.Ref, NestedRoot: &plainProject, NestedInputs: f.inputRaw,
		NestedStage: &f.producer, Materialized: true, FlatWidth: len(f.leaf.Schema())}
	f.raw = &exec.Source{View: captured.View, Ref: captured.Ref, Anonymous: f.rows}
	t.Cleanup(f.input.Close)
	return f
}

func (f *timeLoopFixture) install(policy expr.Expr, prefix bool) {
	calls := 0
	boolean := f.rows[0][2].Type()
	f.program = &exec.RLSProgram{Volatile: true, Groups: []exec.RLSRuntimeGroup{{Permissive: []exec.RLSRuntimeClause{{
		State: exec.State{Source: f.input, SourceMode: exec.SourceBorrowed},
		Expression: enginetest.QueryProbe(boolean.Usage(), func(c *exec.Context) (sqltype.Value, error) {
			calls++
			if prefix && calls == 1 {
				return sqltype.Boolean(boolean, true), nil
			}
			first := 1
			if prefix {
				first = 2
			}
			if calls == first {
				value, err := policy.Eval(c)
				if err != nil || policy.ResultUsage().Type.ID() == sqltype.Bool {
					return value, err
				}
				return sqltype.Boolean(boolean, false), nil
			}
			return sqltype.Boolean(boolean, false), nil
		}),
	}}}}}
	f.raw.RLS = f.program
}

func (f *timeLoopFixture) assertRetained(t *testing.T, stage *exec.Stage, prefix bool) {
	t.Helper()
	want := 0
	if prefix {
		want = 1
	}
	if stage.Count() != want || stage.Closed || stage.Terminal != nil || stage.Source != f.raw {
		t.Error("loop budget EOF lost retained sample backing", stage.Count(), want)
	}
	if prefix && stage.Count() > 0 && !reflect.DeepEqual(stage.Source.Row(stage.Index(0)), f.rows[0]) {
		t.Error("loop budget EOF damaged the accepted occurrence")
	}
	clause := &f.program.Groups[0].Permissive[0]
	if clause.Evaluation.Context != nil || clause.Evaluation.Query != nil || clause.State.Source != f.input {
		t.Error("loop unwind retained invocation scratch or lost capture owner")
	}
	if f.producer.Closed || f.producer.Terminal != nil || f.producer.Source != f.inputRaw || f.closes != 0 || f.input.NestedStage != &f.producer {
		t.Error("loop unwind terminalized the borrowed producing capture", f.closes)
	}
	for i := range f.input.FlatCount {
		if !reflect.DeepEqual(f.input.Row(i), f.rows[i]) {
			t.Error("loop unwind damaged captured values", i)
		}
	}
}

func (f *timeLoopFixture) close(t *testing.T, stage *exec.Stage) {
	t.Helper()
	if err := stage.Close(); err != nil || stage.Source != nil || stage.Selection != nil || stage.Finish != nil || stage.Reset != nil {
		t.Error("sample close retained local state", err)
	}
	f.program.Close()
	f.program.Close()
	if f.closes != 1 || !f.producer.Closed || f.producer.Source != nil || f.input.NestedStage != nil {
		t.Error("capture owner did not close its producer exactly once", f.closes)
	}
}

func TestSystemTimeNestedMembershipDeadline(t *testing.T) {
	for _, operator := range []string{"in", "any"} {
		t.Run(operator, func(t *testing.T) {
			for _, mode := range []string{"all_rejected", "accepted_prefix", "expired_cancel", "argument_failure", "infinity", "infinity_cancel"} {
				timeRunDeadline(t, mode, func(t *testing.T) {
					base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					ctx := &timeLoopContext{Context: base}
					op := " IN "
					if operator == "any" {
						op = " = ANY "
					}
					f := timeNewLoopFixture(t, ctx, "SELECT *, (NULL::integer, id + 100000)"+op+"(SELECT NULL::integer, id FROM t) FROM t TABLESAMPLE system_time(100)")
					policy := f.project.Expressions[3].(plan.SubqueryExpr)
					if len(policy.Membership) != 2 || len(policy.Comparisons) != 2 {
						t.Fatal("fixture did not bind NULL-sensitive hashed row membership")
					}
					prefix := mode == "accepted_prefix"
					f.install(policy, prefix)
					defer f.program.Close()
					clause := &f.program.Groups[0].Permissive[0]
					warm := clause.State.Evaluation(ctx)
					warm.Row = f.rows[0]
					value, err := policy.Eval(&warm)
					truth, boolErr := value.Boolean()
					if err != nil || boolErr != nil || truth || len(clause.State.Memberships) != 1 || f.input.FlatCount != len(f.rows) {
						t.Fatal("real prewarm did not populate exhaustive cached RHS", err, boolErr, truth, len(clause.State.Memberships), f.input.FlatCount)
					}
					key := exec.SubqueryMembershipKey{Plan: policy.QueryPlan, Source: f.input}
					cache := clause.State.Memberships[key]
					if cache == nil || len(cache.Flat) != 2*len(f.rows) {
						t.Fatal("prewarm did not retain every actual NULL RHS candidate")
					}
					backing := &cache.Flat[0]
					primary := errors.New("genuine membership last-left failure")
					leftCalls, comparisons := 0, 0
					armed := false
					ctx.observe = func(caller string, membership bool) {
						if armed && membership && caller == "stuntgres/internal/sqltype.ComparisonPredicate.Evaluate" {
							comparisons++
						}
					}
					last := policy.Comparisons[1].Left
					policy.Comparisons[1].Left = enginetest.QueryProbe(last.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
						value, err := last.Eval(c)
						if err != nil {
							return sqltype.Value{}, err
						}
						leftCalls++
						if err := timeWorkPastBudget(base); err != nil {
							return sqltype.Value{}, err
						}
						armed = true
						if mode == "expired_cancel" || mode == "infinity_cancel" {
							cancel()
						}
						if mode == "argument_failure" {
							return sqltype.Value{}, primary
						}
						return value, nil
					})
					leaf := f.leaf
					if mode == "infinity" || mode == "infinity_cancel" {
						leaf = timeArgument(t, leaf, math.Inf(1))
					}
					stage, err := exec.OpenNode(ctx, &exec.State{Source: f.raw}, leaf)
					defer stage.Close()
					if leftCalls != 1 || !armed {
						t.Error("cached membership never reached the successful last-left gate", leftCalls, armed)
					}
					if mode == "expired_cancel" || mode == "infinity_cancel" || mode == "argument_failure" {
						want := error(context.Canceled)
						if mode == "argument_failure" {
							want = primary
						}
						if !errors.Is(err, want) || stage.Source != nil || stage.Selection != nil || comparisons != 0 {
							t.Error("membership genuine failure/cancel became EOF or consumed candidates", err, comparisons)
						}
					} else {
						if err != nil {
							t.Fatal("membership elapsed EOF escaped as an error", err)
						}
						if mode == "infinity" {
							if comparisons != 2*len(f.rows) {
								t.Error("Infinity did not exhaust NULL-sensitive cached candidates", comparisons, len(f.rows))
							}
						} else if comparisons != 0 {
							t.Errorf("cached membership consumed candidates after successful last-left expiry: %d field comparisons across %d candidates", comparisons, len(f.rows))
						}
						f.assertRetained(t, &stage, prefix)
						if clause.State.Memberships[key] != cache || len(cache.Flat) != 2*len(f.rows) || &cache.Flat[0] != backing {
							t.Error("membership budget EOF released/replaced the retained RHS cache")
						}
					}
					clause = &f.program.Groups[0].Permissive[0]
					if clause.Evaluation.Context != nil || clause.Evaluation.Query != nil {
						t.Error("membership retained RLS invocation scratch")
					}
					f.close(t, &stage)
					if cache.Flat != nil || cache.Scratch != nil || clause.State.Memberships != nil {
						t.Error("membership capture owner retained RHS scratch after close")
					}
				})
			}
		})
	}
}

func TestSystemTimeNestedSetDeadline(t *testing.T) {
	for _, operation := range []string{"intersect_all", "except_all"} {
		t.Run(operation, func(t *testing.T) {
			for _, mode := range []string{"accepted_prefix", "expired_cancel", "infinity", "infinity_cancel", "argument_failure"} {
				t.Run(mode, func(t *testing.T) {
					// Keep suite scheduling outside the sample's elapsed clock.
					// The ordinary timer below advances virtual Go time only
					// after genuine arm demand or the selected output checkpoint.
					synctest.Test(t, func(t *testing.T) {
						base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						ctx := &timeLoopContext{Context: base, caller: "stuntgres/internal/engine/exec.OpenSetNode"}
						// Bound genuine SQL arm production before the observed output
						// phase. QueryFixture's quartet is TRUE,NULL,FALSE,TRUE:
						// INTERSECT ALL retains all four; EXCEPT ALL removes one TRUE.
						left := "SELECT f FROM (SELECT * FROM t LIMIT 4) l"
						op, right := " INTERSECT ALL ", "SELECT f FROM (SELECT * FROM t LIMIT 4) r"
						wantLeft, wantRight, wantOutput := 4, 4, 4
						wantCount := "4"
						if operation == "except_all" {
							op, right, wantRight, wantOutput = " EXCEPT ALL ", "SELECT f FROM (SELECT * FROM t LIMIT 4) r WHERE id = 1", 1, 3
							wantCount = "3"
						}
						// The genuine scalar is FALSE only for the independently
						// specified output multiplicity (and the known FALSE f).
						// Infinity must therefore retain only the initial prefix.
						f := timeNewLoopFixture(t, ctx, "SELECT *, (SELECT count(*) <> "+wantCount+" OR bool_and(f) FROM ("+left+op+right+") s) FROM t TABLESAMPLE system_time(100)")
						policy := f.project.Expressions[3].(plan.SubqueryExpr)
						f.install(policy, true)
						defer f.program.Close()
						outputChecks, sets, leftCalls, rightCalls := 0, 0, 0, 0
						primary := errors.New("genuine nested set arm failure")
						plan.VisitExecutableQuery(policy.QueryPlan.Root, func(node plan.Node) {
							if n, ok := node.(*plan.SetNode); ok {
								sets++
								if !n.All || n.Operation != map[string]string{"intersect_all": "intersect", "except_all": "except"}[operation] {
									t.Fatal("fixture did not retain blocking ALL set operation")
								}
								original := n.LeftExpressions[0]
								n.LeftExpressions[0] = enginetest.QueryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
									value, err := original.Eval(c)
									if err != nil {
										return sqltype.Value{}, err
									}
									leftCalls++
									if mode == "argument_failure" {
										if err := timeWorkPastBudget(base); err != nil {
											return sqltype.Value{}, err
										}
										return sqltype.Value{}, primary
									}
									return value, nil
								})
								rightExpression := n.RightExpressions[0]
								n.RightExpressions[0] = enginetest.QueryProbe(rightExpression.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
									value, err := rightExpression.Eval(c)
									if err == nil {
										rightCalls++
									}
									return value, err
								})
							}
						}, func(_ expr.Expr) {})
						if sets != 1 {
							t.Fatal("fixture missed the genuine nested set executor", sets)
						}
						ctx.observe = func(caller string, _ bool) {
							if caller != "stuntgres/internal/engine/exec.OpenSetNode" || leftCalls != wantLeft || rightCalls != wantRight {
								return
							}
							outputChecks++
							if outputChecks == 3 {
								// The TRUE bucket checkpoint and first copy checkpoint
								// precede the first append. The third checkpoint is
								// after that append, before the next copy or bucket.
								_ = timeWorkPastBudget(base)
								if mode == "expired_cancel" || mode == "infinity_cancel" {
									cancel()
								}
							}
						}
						leaf := f.leaf
						if mode == "infinity" || mode == "infinity_cancel" {
							leaf = timeArgument(t, leaf, math.Inf(1))
						}
						stage, err := exec.OpenNode(ctx, &exec.State{Source: f.raw}, leaf)
						defer stage.Close()
						if mode == "argument_failure" {
							if !errors.Is(err, primary) || leftCalls != 1 || outputChecks != 0 || stage.Source != nil {
								t.Error("set arm genuine failure lost priority", err, leftCalls, outputChecks)
							}
						} else {
							if leftCalls != wantLeft || rightCalls != wantRight || outputChecks < 3 {
								t.Error("set fixture failed to enter post-input output expansion", leftCalls, rightCalls, outputChecks)
							}
							if mode == "expired_cancel" || mode == "infinity_cancel" {
								if !errors.Is(err, context.Canceled) || outputChecks != 3 || stage.Source != nil || stage.Selection != nil {
									t.Error("set output cancellation became elapsed EOF or consumed tail", err, outputChecks)
								}
							} else {
								if err != nil {
									t.Fatal("nested set elapsed EOF escaped as an error", err)
								}
								if mode == "infinity" {
									// Three distinct buckets, every output copy, then
									// the final checkpoint must all be exhausted.
									if outputChecks != wantOutput+4 {
										t.Error("Infinity did not exhaust genuine set multiplicities", outputChecks, wantOutput+4)
									}
								} else if outputChecks != 3 {
									t.Errorf("nested set advanced past the first appended output after real expiry: %d checkpoints", outputChecks)
								}
								f.assertRetained(t, &stage, true)
							}
						}
						clause := &f.program.Groups[0].Permissive[0]
						if clause.Evaluation.Context != nil || clause.Evaluation.Query != nil {
							t.Error("set unwind retained RLS invocation scratch")
						}
						f.close(t, &stage)
					})
				})
			}
		})
	}
}
