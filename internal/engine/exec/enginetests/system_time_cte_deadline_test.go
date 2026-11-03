package enginetests_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// The CTE belongs to the real enclosing bound project, not the scalar reader.
// A borrowed scalar's budget unwind must not poison that producer's owner.
func TestSystemTimeNestedCTEDeadline(t *testing.T) {
	for _, phase := range []string{"projection", "child"} {
		t.Run(phase, func(t *testing.T) {
			modes := []string{"accepted_prefix", "expired_cancel", "argument_failure", "infinity", "infinity_cancel"}
			if phase == "projection" {
				modes = append(modes, "pending_close", "pending_error", "pending_cancel")
			}
			for _, mode := range modes {
				timeRunDeadline(t, mode, func(t *testing.T) {
					base, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					body := "SELECT f, f FROM t"
					if phase == "child" {
						body += " ORDER BY id + 0, v + 0"
					}
					f := timeNewLoopFixture(t, base, "WITH x(a,b) AS MATERIALIZED ("+body+") SELECT *, (SELECT bool_and(a) AND bool_or(b) FROM x) FROM t TABLESAMPLE system_time(100)")
					if f.owner == nil || !f.project.CTEOwner {
						t.Fatal("bound outer project did not create the genuine CTE owner")
					}
					if phase == "projection" {
						timeAssertNativeQuartets(t, f.rows)
					}
					f.owner.State.Source, f.owner.State.SourceMode = f.input, exec.SourceBorrowed
					policy := f.project.Expressions[3].(plan.SubqueryExpr)
					f.install(policy, true)
					defer f.program.Close()
					f.program.Groups[0].Permissive[0].State.Ctes = f.owner
					var definition *plan.CTEDefinition
					plan.VisitExecutableQuery(policy.QueryPlan.Root, func(node plan.Node) {
						if n, ok := node.(*plan.DerivedNode); ok && n.CTE != nil {
							definition = n.CTE
						}
					}, func(expr.Expr) {})
					if definition == nil || definition.Folded() {
						t.Fatal("fixture lost its shared materialized CTE")
					}
					project := definition.Query.Root.(*plan.ProjectNode)
					firstCalls, secondCalls, cteCloses := 0, 0, 0
					primary := errors.New("genuine CTE expression failure after expiry")
					cleanup := errors.New("secondary CTE pending cleanup failure")
					resuming := false
					var resumeCancel context.CancelFunc
					first, second := project.Expressions[0], project.Expressions[1]
					if phase == "child" {
						sort := project.Child.(*plan.SortNode)
						first, second = sort.Keys[0].Expression, sort.Keys[1].Expression
					}
					gateAt := 2 // Keep one complete CTE tuple before partial projection.
					if phase == "child" {
						gateAt = 1
					}
					probe := enginetest.QueryProbe(first.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
						value, err := first.Eval(c)
						if err != nil {
							return sqltype.Value{}, err
						}
						firstCalls++
						if phase == "projection" && firstCalls == 1 {
							producer := f.owner.Producers[definition]
							finish := producer.Inner.Finish
							producer.Inner.Finish = func() error {
								cteCloses++
								if resuming && (mode == "pending_error" || mode == "pending_cancel") {
									if finish != nil {
										_ = finish()
									}
									return cleanup
								}
								if finish != nil {
									return finish()
								}
								return nil
							}
						}
						if firstCalls == gateAt {
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
					next := enginetest.QueryProbe(second.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
						secondCalls++
						value, err := second.Eval(c)
						if err == nil && resuming {
							if mode == "pending_error" {
								return sqltype.Value{}, primary
							}
							if mode == "pending_cancel" {
								resumeCancel()
							}
						}
						return value, err
					})
					if phase == "projection" {
						project.Expressions[0], project.Expressions[1] = probe, next
					} else {
						sort := project.Child.(*plan.SortNode)
						sort.Keys[0].Expression, sort.Keys[1].Expression = probe, next
					}
					leaf := f.leaf
					if mode == "infinity" || mode == "infinity_cancel" {
						leaf = timeArgument(t, leaf, math.Inf(1))
					}
					stage, err := exec.OpenNode(base, &exec.State{Source: f.raw}, leaf)
					defer stage.Close()
					producer := f.owner.Producers[definition]
					if producer == nil || firstCalls < gateAt {
						t.Fatal("fixture missed actual CTE producer demand", firstCalls)
					}
					var abandoned []sqltype.Value
					abandonedStart := 0
					if mode == "expired_cancel" || mode == "infinity_cancel" || mode == "argument_failure" {
						want := error(context.Canceled)
						if mode == "argument_failure" {
							want = primary
						}
						if !errors.Is(err, want) || stage.Source != nil || !producer.Closed {
							t.Error("CTE genuine error/cancel became EOF or left producer live", err, producer.Closed)
						}
					} else {
						if err != nil {
							t.Fatal("CTE private elapsed unwind escaped as statement error", err)
						}
						if mode == "infinity" {
							if firstCalls != len(f.rows) || secondCalls != len(f.rows) || producer.Source.FlatCount != len(f.rows) || !producer.Done {
								t.Error("Infinity did not exhaust genuine CTE", firstCalls, secondCalls, producer.Source.FlatCount)
							}
						} else {
							if firstCalls != gateAt || secondCalls != gateAt-1 || producer.Closed || producer.Inner.Terminal != nil || cteCloses != 0 {
								t.Error("CTE private unwind consumed next field or poisoned its enclosing owner", firstCalls, secondCalls, producer.Closed, cteCloses)
							}
							if phase == "projection" && (producer.Source.FlatCount != 1 || len(producer.Source.Flat) != 2 || producer.Inner.Evaluation.Context != nil) {
								t.Error("CTE unwind lost complete prefix or retained partial tuple/scratch", producer.Source.FlatCount, len(producer.Source.Flat))
							}
							f.assertRetained(t, &stage, true)
							if phase == "projection" {
								// The public cache contains only row 1. Row 2's
								// successful first field must remain pending, not
								// be replayed or replaced by row 3's FALSE.
								abandonedStart = len(producer.Source.Flat)
								abandoned = producer.Source.Flat[:cap(producer.Source.Flat)]
								if mode != "pending_close" {
									live, stop := context.WithCancel(base)
									defer stop()
									resuming, resumeCancel = true, stop
									if mode == "pending_error" || mode == "pending_cancel" {
										present, resumeErr := producer.Ensure(live, 1)
										want := error(primary)
										if mode == "pending_cancel" {
											want = context.Canceled
										}
										if present || !errors.Is(resumeErr, want) || errors.Is(resumeErr, cleanup) || !producer.Closed || cteCloses != 1 {
											t.Error("pending CTE failure/cancel lost its primary cause or owner cleanup", present, resumeErr, producer.Closed, cteCloses)
										}
										if firstCalls != 2 || secondCalls != 2 || producer.Source.FlatCount != 1 || len(producer.Source.Flat) != 2 {
											t.Error("pending CTE failure replayed completed effects or published a partial tuple", firstCalls, secondCalls, producer.Source.FlatCount, len(producer.Source.Flat))
										}
									} else {
										for i, original := range f.rows {
											present, resumeErr := producer.Ensure(live, i)
											if resumeErr != nil || !present {
												t.Errorf("same CTE owner lost original occurrence %d: present=%v err=%v", i, present, resumeErr)
												break
											}
											want := []sqltype.Value{original[2], original[2]}
											if !reflect.DeepEqual(producer.Source.Row(i), want) {
												t.Errorf("same CTE owner resumed with wrong original tuple %d: got %v want %v", i, producer.Source.Row(i), want)
											}
										}
										present, resumeErr := producer.Ensure(live, len(f.rows))
										if resumeErr != nil || present || producer.Source.FlatCount != len(f.rows) || len(producer.Source.Flat) != 2*len(f.rows) || !producer.Done {
											t.Error("same CTE owner changed remaining multiplicity or EOF", present, resumeErr, producer.Source.FlatCount, len(producer.Source.Flat), producer.Done)
										}
										if firstCalls != len(f.rows) || secondCalls != len(f.rows) {
											t.Error("same CTE owner skipped or replayed completed expression effects", firstCalls, secondCalls, len(f.rows))
										}
										timeAssertNativeQuartets(t, f.rows)
									}
								}
							}
						}
					}
					f.close(t, &stage)
					if !f.owner.Closed || !producer.Closed || phase == "projection" && cteCloses != 1 {
						t.Error("enclosing CTE owner did not release its producer once", f.owner.Closed, producer.Closed, cteCloses)
					}
					if mode == "pending_close" || mode == "pending_error" || mode == "pending_cancel" {
						timeAssertAbandonedTail(t, abandoned, abandonedStart)
						if producer.Inner.Evaluation.Context != nil || producer.Inner.Source != nil || producer.Inner.Expressions != nil {
							t.Error("closed pending CTE retained invocation scratch")
						}
					}
				})
			}
		})
	}
}

// These assertions describe the native fixture's captured occurrence ownership,
// not a portable ordering promise for a SQL query without ORDER BY.
func timeAssertNativeQuartets(t *testing.T, rows [][]sqltype.Value) {
	t.Helper()
	if len(rows) == 0 || len(rows)%4 != 0 {
		t.Fatal("continuation fixture lost complete original quartets", len(rows))
	}
	for i, row := range rows {
		if len(row) != 3 {
			t.Fatal("continuation fixture changed native tuple width", i, len(row))
		}
		id, idErr := row[0].Integer()
		if idErr != nil || id != int64(i%4+1) {
			t.Fatal("continuation fixture changed native occurrence order", i, id, idErr)
		}
		if i%4 == 1 {
			if !row[1].IsNull() || !row[2].IsNull() {
				t.Fatal("original second occurrence must retain NULL v and f", i)
			}
			continue
		}
		wantV := int64(1)
		if i%4 == 2 {
			wantV = 2
		}
		v, vErr := row[1].Integer()
		flag, flagErr := row[2].Boolean()
		if vErr != nil || v != wantV || flagErr != nil || flag != (i%4 != 2) {
			t.Fatal("continuation fixture changed original tuple values", i, v, flag, vErr, flagErr)
		}
	}
}

func timeAssertAbandonedTail(t *testing.T, backing []sqltype.Value, published int) {
	t.Helper()
	for i := published; i < len(backing); i++ {
		if !reflect.DeepEqual(backing[i], sqltype.Value{}) {
			t.Error("owner cleanup retained abandoned projected value backing", i)
		}
	}
}
