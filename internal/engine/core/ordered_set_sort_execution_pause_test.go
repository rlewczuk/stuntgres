package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

func TestOrderedSetSortExecutionPauseOwnership(t *testing.T) {
	for _, mode := range []string{"same_owner", "source", "release", "session_end", "statement_deadline", "initialization_error", "input_error", "child_sort_error", "direct_error", "source_close_error", "input_and_close_error"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := engine.New()
				enginetest.Run(t, e, "CREATE TABLE os_owner(v double precision)")
				enginetest.Run(t, e, "INSERT INTO os_owner VALUES(3),(4),(4),(4),(5),(5),(5),(6)")
				captured := enginetest.Run(t, e, "SELECT * FROM os_owner").Rows.(*exec.FinalRows)
				defer captured.Close()
				view, ref := captured.Source.View, captured.Source.Ref
				sess, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: 101, Defaults: executionPauseTimeoutDefaults{enabled: mode == "statement_deadline"}})
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					sess.RequestEnd(session.CleanClose)
					if err := sess.WaitEnd(context.Background()); err != nil {
						t.Error(err)
					}
				}()
				cycles := []string{mode}
				if mode == "same_owner" {
					cycles = []string{"resume", "cause", "resume"}
				}
				for _, action := range cycles {
					func() {
						pause := util.NewExecutionPause("query.aggregate.sort")
						defer pause.Resume()
						primary := errors.New("real pre-sort " + action)
						request := util.WithExecutionPause(context.Background(), pause)
						if action == "initialization_error" {
							request = workadmission.With(request, func(_ context.Context, class workadmission.Class) error {
								if class == workadmission.WorkAggregateInput {
									return primary
								}
								return nil
							})
						}
						sourceContext, stopSource := context.WithCancelCause(request)
						defer stopSource(context.Canceled)
						op, _, err := sess.AcquireCommand(sourceContext)
						if err != nil {
							t.Fatal(err)
						}
						defer op.Done()
						scope, err := e.StartStatement(op)
						if err != nil {
							t.Fatal(err)
						}
						defer scope.Finish(context.Canceled)
						raw := enginetest.Statement(t, "SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY v) AS v FROM os_owner")
						if err := scope.Gate(raw); err != nil {
							t.Fatal(err)
						}
						if err := scope.AcquireRelation(raw, true); err != nil {
							t.Fatal(err)
						}
						if !scope.ReplacementRead {
							t.Fatal("statement failed to acquire real replacement reservation")
						}
						e.Mu.Lock()
						auth, err := e.AuthorizationLocked(op)
						var binding analyze.Binding
						if err == nil {
							binding, err = core.Describe(op.Context(), e.AnalysisHost(op), raw, auth, ref, scope.Input, nil, nil)
							scope.RoutineAuth, scope.RoutineDependencies = auth, binding.Dependencies
						}
						e.Mu.Unlock()
						if err != nil {
							t.Fatal(err)
						}
						query := binding.Query
						node := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
						call := &node.Calls[0]
						// The probe forwards the accepted typed expression. It observes
						// effective authority, never supplies an aggregate or sort result.
						argument := call.Arguments[0]
						observed := make(chan context.Context, 1)
						seen := false
						call.Arguments[0] = enginetest.QueryProbe(argument.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
							if !seen {
								seen = true
								observed <- c.Context
							}
							if action == "input_error" {
								return sqltype.Value{}, primary
							}
							return argument.Eval(c)
						})
						if action == "child_sort_error" {
							child := node.Child
							node.Child = &plan.SortNode{QuerySchema: child.Schema(), Child: child, CacheWidth: 1, Keys: []plan.SortKey{{Cache: 0, Slot: -1, Comparison: call.Order[0].Comparison, Expression: enginetest.QueryProbe(argument.ResultUsage(), func(*exec.Context) (sqltype.Value, error) {
								return sqltype.Value{}, primary
							})}}}
						}
						if action == "direct_error" {
							direct := call.Direct[0]
							call.Direct[0] = enginetest.QueryProbe(direct.ResultUsage(), func(*exec.Context) (sqltype.Value, error) {
								return sqltype.Value{}, primary
							})
						}
						source, err := exec.CaptureSource(op.Context(), view, ref)
						if err != nil {
							t.Fatal(err)
						}
						defer source.Close()
						source.Host = core.HostOf(scope)
						committed := source.Committed
						if len(committed) != 8 {
							t.Fatal("fixture lost real retained relation input", len(committed))
						}
						values := make([][]sqltype.Value, len(committed))
						owners := make([]*sqltype.Value, len(committed))
						for i, row := range committed {
							values[i] = append([]sqltype.Value(nil), row.Values...)
							owners[i] = &row.Values[0]
						}
						// Use the existing retained-stage source seam to make cleanup
						// fallible. Its rows borrow the real captured relation tuples.
						var producer *exec.Stage
						closed := 0
						if action == "source_close_error" || action == "input_and_close_error" {
							cleanupErr := errors.New("retained input close")
							if action == "source_close_error" {
								primary = cleanupErr
							}
							rows := make([][]sqltype.Value, len(committed))
							for i, row := range committed {
								rows[i] = row.Values
							}
							schema := node.Child.Schema()
							producer = &exec.Stage{
								Source:  &exec.Source{Anonymous: rows},
								Columns: schema,
								Expressions: []expr.Expr{enginetest.QueryProbe(schema[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
									if action == "input_and_close_error" {
										return sqltype.Value{}, primary
									}
									return c.Row[0], nil
								})},
								Finish: func() error {
									closed++
									return cleanupErr
								},
							}
							bridge := &exec.Source{Ref: source.Ref, Materialized: true, FlatWidth: 1, NestedRoot: &plan.ProjectNode{QuerySchema: schema, Child: &plan.SingletonNode{}}, NestedStage: producer}
							source.Sources = []*exec.Source{bridge}
						}
						root, provider := query.Root, call.Provider
						order := append([]plan.SortKey(nil), call.Order...)
						orderOwner := &call.Order[0]
						assertRetained := func() {
							t.Helper()
							if query.Root != root || call.Provider != provider || &call.Order[0] != orderOwner || !reflect.DeepEqual(call.Order, order) || e.CatalogRead().Get(ref.Address) != auth.Catalog.Get(ref.Address) {
								t.Fatal("accepted graph/provider/catalog authority changed")
							}
							for i, row := range committed {
								if &row.Values[0] != owners[i] || !reflect.DeepEqual(row.Values, values[i]) {
									t.Fatal("retained immutable input changed", i)
								}
							}
						}
						type childKey struct{}
						child, cancelChild := context.WithTimeout(context.WithValue(op.Context(), childKey{}, "effective-child"), 3*time.Second)
						defer cancelChild()
						active := scope.ActivateRuntime(child)
						catalog, frontier := e.Catalog.State, e.Visibility.CommitSeq()
						type outcome struct {
							rows  exec.RowResult
							count int
							err   error
						}
						done := make(chan outcome, 1)
						joined := false
						defer func() {
							cancelChild()
							pause.Resume()
							if !joined {
								out := <-done
								if out.rows != nil {
									out.rows.Close()
								}
							}
						}()
						go func() {
							rows, count, err := exec.Finalize(active, query.Root, source, nil)
							done <- outcome{rows, count, err}
						}()
						preSortFailure := action == "initialization_error" || action == "input_error" || action == "child_sort_error" || action == "direct_error" || action == "input_and_close_error"
						var out outcome
						wantErr, wantCause := error(nil), error(nil)
						if preSortFailure {
							out = <-done
							joined = true
							wantErr = primary
							pause.Resume()
							if err := pause.Wait(context.Background()); err == nil || err.Error() != "stuntgres: execution pause resumed before admission" {
								t.Fatal("pre-sort work falsely admitted sort", err)
							}
						} else {
							if err := pause.Wait(child); err != nil {
								t.Fatal("real aggregate comparison hold", err)
							}
							select {
							case out = <-done:
								joined = true
								t.Fatal("held comparison published an outcome", out)
							default:
							}
							if op.SourceContext != sourceContext || sourceContext.Err() != nil || scope.ReplacementRead {
								t.Fatal("runtime replaced source authority or retained reservation")
							}
							effective := <-observed
							deadline, hasDeadline := effective.Deadline()
							wantDeadline, _ := child.Deadline()
							if effective.Done() != child.Done() || effective.Value(childKey{}) != "effective-child" || context.Cause(effective) != nil || !hasDeadline || deadline != wantDeadline {
								t.Fatal("sort lost selected effective child")
							}
							if !e.Mu.TryLock() {
								t.Fatal("sort holds engine mutex")
							}
							e.Mu.Unlock()
							if !sess.Mu.TryLock() {
								t.Fatal("sort holds session mutex")
							}
							sess.Mu.Unlock()
							if !e.ReplacementMu.TryLock() {
								t.Fatal("sort holds replacement reservation")
							}
							e.ReplacementMu.Unlock()
							if !scope.Owner.Mu.TryLock() {
								t.Fatal("sort holds transaction mutex")
							}
							scope.Owner.Mu.Unlock()
							assertRetained()
							if action == "source_close_error" {
								// Required input close follows aggregate sorting and
								// finalization, but still precedes row publication.
								if closed != 0 || producer.Closed {
									t.Fatal("required input close ran before held sort")
								}
								wantErr = primary
							} else if action != "resume" {
								wantErr, wantCause = context.Canceled, errors.New("original sort owner cause")
							}
							switch action {
							case "resume", "source_close_error":
								pause.Resume()
							case "cause":
								op.Cancel(wantCause)
							case "source":
								stopSource(wantCause)
							case "release":
								op.Done()
								wantCause = context.Canceled
							case "session_end":
								sess.RequestEnd(session.CleanClose)
								wantCause = context.Canceled
							case "statement_deadline":
								wantErr, wantCause = context.DeadlineExceeded, session.StatementTimeoutCause
								<-op.Context().Done()
							}
							out = <-done
							joined = true
							if action == "source_close_error" && (effective.Err() != nil || context.Cause(effective) != nil || sourceContext.Err() != nil) {
								t.Fatal("required close veto manufactured context cancellation")
							}
							if action != "resume" && action != "source_close_error" {
								if context.Cause(effective) != wantCause || effective.Err() != wantErr {
									t.Fatal("effective child lost original error/cause", effective.Err(), context.Cause(effective), wantCause)
								}
								if action != "source" && sourceContext.Err() != nil {
									t.Fatal("operation canceled original source")
								}
							}
						}
						if out.err != wantErr {
							t.Fatal("originating execution error", out.err, wantErr)
						}
						if wantErr != nil {
							if out.rows != nil || out.count != 0 {
								t.Fatal("failed sort published partial rows", out)
							}
							var diagnostic interface{ SQLState() string }
							if errors.As(out.err, &diagnostic) {
								t.Fatal("host error gained SQLSTATE", out.err)
							}
						} else {
							if out.rows == nil || out.count != 1 {
								t.Fatal("release did not finish actual aggregate", out)
							}
							row := make([]sqltype.Value, 1)
							present, err := out.rows.Next(context.Background(), row)
							value, valueErr := row[0].Float()
							if err != nil || !present || row[0].Type() != query.Root.Schema()[0].Usage.Type || row[0].Type().ID() != sqltype.Float8 || valueErr != nil || value != 4.5 {
								t.Fatal("independent percentile result", row, err, valueErr)
							}
							if present, err := out.rows.Next(context.Background(), row); present || err != nil {
								t.Fatal("aggregate EOF", present, err)
							}
							out.rows.Close()
						}
						assertRetained()
						if producer != nil && (closed != 1 || !producer.Closed || producer.Source != nil || producer.Expressions != nil || producer.Evaluation.Context != nil || producer.Evaluation.Host != nil || producer.Finish != nil) {
							t.Fatal("fallible input cleanup retained producer/evaluator graph", closed, producer)
						}
						if source.View != nil || source.Host != nil || source.Authority != nil || source.NestedStage != nil || source.NestedRoot != nil || source.Committed != nil || source.Scratch != nil {
							t.Fatal("terminal source retained live graph/input authority")
						}
						if e.Catalog.State != catalog || e.Visibility.CommitSeq() != frontier {
							t.Fatal("executor published catalog or transaction state")
						}
						if err := scope.Finish(out.err); err != wantErr {
							t.Fatal("Finish replaced primary error", err, wantErr)
						}
						if !scope.Finished || scope.ReplacementRead || scope.RoutineAuth.Catalog.State() != nil || scope.RoutineDependencies != nil || scope.View != nil || scope.QuerySources != nil || scope.Stage.Rows != nil || scope.Stage.Deletes != nil || scope.LockTicket != nil || scope.QueryLockContext != nil {
							t.Fatal("terminal scope retained admission/source/publication authority")
						}
						op.Done()
						sess.Mu.Lock()
						_, member := sess.Operations[op]
						leases, released := sess.Leases, op.Released
						sess.Mu.Unlock()
						if member || leases != 0 || !released {
							t.Fatal("terminal operation retained membership/lease", member, leases, released)
						}
						if wantErr != nil && (e.Catalog.State != catalog || e.Visibility.CommitSeq() != frontier) {
							t.Fatal("failed aggregate published catalog or transaction state")
						}
					}()
				}
			})
		})
	}
}
