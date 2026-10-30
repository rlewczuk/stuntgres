package reference_test

import (
	"database/sql"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExplainAnalyzeForeignSelections(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		r.advanced = &fdwAdvancedState{}
		fdwLive(t, p, b, r, run)
		want := [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}}
		defer func() {
			r.mu.Lock()
			r.queryPushdown = false
			r.mu.Unlock()
		}()
		for _, mode := range []string{"base", "chosen", "declined"} {
			r.mu.Lock()
			r.queryPushdown = mode == "chosen"
			r.mu.Unlock()
			query := "SELECT id,label FROM fdw_data WHERE id=$1"
			names, types := []string{"id", "label"}, []string{"INT4", "TEXT"}
			if mode != "base" {
				query = "SELECT a.id FROM fdw_data a JOIN fdw_data b ON a.id=b.id WHERE a.id=$1"
				names, types = []string{"id"}, []string{"INT4"}
			}
			analyzed := "EXPLAIN (ANALYZE,VERBOSE) " + query
			for _, prepared := range []bool{false, true} {
				func() {
					var executor fixture.Executor = c
					beforePrepare := r.snapshot()
					if prepared {
						statement, err := c.PrepareContext(p.Ctx, analyzed)
						if err != nil {
							t.Fatal(err)
						}
						defer func() {
							if err := statement.Close(); err != nil {
								t.Error(err)
							}
						}()
						executor = fixture.HeldStatement{Executor: c, Statement: statement, SQL: analyzed}
						if !b.PG {
							after := r.snapshot()
							if after.begins != beforePrepare.begins || after.iterations != beforePrepare.iterations {
								t.Fatal("Prepare executed source")
							}
						}
					}
					for _, argument := range []int64{1, 2, 99, 1} {
						rows := float64(1)
						if argument == 99 {
							rows = 0
						}
						before := r.snapshot()
						writerBegins, writerEnds := r.writer.begins, r.writer.ends
						r.advanced.mu.Lock()
						qn, bn := len(r.advanced.queryPlans), len(r.advanced.basePlans)
						en, eb := len(r.advanced.executedPlans), len(r.advanced.baseExecutions)
						r.advanced.mu.Unlock()
						lines := foreignAnalyzePlan(t, p, executor, analyzed, false, rows, argument)
						if !b.PG {
							// Never fail while holding the fixture mutex: failure cleanup
							// can itself enter provider callbacks on this connection.
							r.advanced.mu.Lock()
							queries := slices.Clone(r.advanced.queryPlans[qn:])
							plans := slices.Clone(r.advanced.executedPlans[en:])
							requests := slices.Clone(r.advanced.executedRequests[en:])
							bases := slices.Clone(r.advanced.basePlans[bn:])
							executions := slices.Clone(r.advanced.baseExecutions[eb:])
							r.advanced.mu.Unlock()
							if mode == "chosen" {
								if len(queries) != 1 || len(plans) != 1 || len(bases) != 0 || len(executions) != 0 {
									t.Fatal("chosen composite was not executed exactly once without base fallback", lines)
								}
								selected, executed := queries[0], requests[0]
								if !reflect.DeepEqual(selected.Request.Stage, executed.Stage) || !reflect.DeepEqual(selected.Request.Parameters, executed.Parameters) || !reflect.DeepEqual(selected.Path.Plan, plans[0]) || len(executed.Parameters.Values) != 1 || executed.Parameters.Values[0] != argument || len(selected.Request.Sources) != len(executed.Sources) {
									t.Fatal("composite selection/current arguments diverged", selected, executed)
								}
								for i, source := range executed.Sources {
									// Begin adds the enlisted transaction and its current
									// characteristics; all selected source semantics stay exact.
									source.Request.Transaction = selected.Request.Sources[i].Request.Transaction
									source.Request.Characteristics = selected.Request.Sources[i].Request.Characteristics
									if !reflect.DeepEqual(source, selected.Request.Sources[i]) || !reflect.DeepEqual(source.Request.Parameters, executed.Parameters) {
										t.Fatal("composite source selection/current arguments diverged", selected, executed)
									}
								}
							} else {
								if len(queries) != 0 || len(plans) != 0 || len(bases) == 0 || len(executions) == 0 {
									t.Fatal("local fallback did not execute actual base selections", mode, lines)
								}
								for _, execution := range executions {
									if len(execution.request.Parameters.Values) != 1 || execution.request.Parameters.Values[0] != argument {
										t.Fatal("stale base arguments", execution.request.Parameters)
									}
									found := false
									for _, selected := range bases {
										request := execution.request
										// The callback precedes enlistment; only Begin owns
										// the live transaction, not an alternate selection.
										request.Transaction = selected.Transaction
										if reflect.DeepEqual(selected.Plan, execution.plan) && reflect.DeepEqual(selected.ForeignRequest, request) {
											found = true
										}
									}
									if !found {
										t.Fatal("base execution lacks matching actual captured selection", execution, bases)
									}
								}
							}
							after := r.snapshot()
							fdwReleased(t, r, before, after.begins-before.begins)
							if r.writer.begins-writerBegins != r.writer.ends-writerEnds {
								t.Fatal("foreign writer scan was not released")
							}
							if !strings.Contains(strings.Join(lines, "\n"), "Remote SQL:") {
								t.Fatal(lines)
							}
						}
						var expected [][]any
						if argument != 99 {
							row := []any{argument}
							if mode == "base" {
								var label any = "one"
								if argument == 2 {
									label = nil
								}
								row = append(row, label)
							}
							expected = [][]any{row}
						}
						step := fixture.Select(query, names, types, expected, argument)
						if prepared {
							step = fixture.Prepared(step)
						}
						run(step)
						fdwAssertSource(t, p, b, r, want)
					}
				}()
			}
		}
	})
}

func TestReferenceExplainAnalyzeForeignParameters(t *testing.T) {
	for _, returning := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwLive(t, p, b, r, run)
				for _, query := range []string{"UPDATE fdw_data SET label=$1 WHERE id=$2", "DELETE FROM fdw_data WHERE id=$1"} {
					func() {
						analyzed := "EXPLAIN ANALYZE " + query
						if returning {
							analyzed += " RETURNING id"
						}
						var executor fixture.Executor = c
						if prepared {
							before := r.snapshot()
							modifies := r.writer.modifies
							statement, err := c.PrepareContext(p.Ctx, analyzed)
							if err != nil {
								t.Fatal(err)
							}
							defer func() {
								if err := statement.Close(); err != nil {
									t.Error(err)
								}
							}()
							executor = fixture.HeldStatement{Executor: c, Statement: statement, SQL: analyzed}
							after := r.snapshot()
							if !b.PG && (after.begins != before.begins || after.iterations != before.iterations || r.writer.modifies != modifies) {
								t.Fatal("Prepare executed foreign mutation")
							}
						}
						arguments := [][]any{{"two", int64(2)}, {"uno", int64(1)}}
						states := [][][]any{{{int64(1), "one", true}, {int64(2), "two", false}, {int64(3), "three", nil}}, {{int64(1), "uno", true}, {int64(2), "two", false}, {int64(3), "three", nil}}}
						if strings.HasPrefix(query, "DELETE") {
							arguments = [][]any{{int64(99)}, {int64(3)}}
							states = [][][]any{states[1], {{int64(1), "uno", true}, {int64(2), "two", false}}}
						}
						for i, args := range arguments {
							count := float64(0)
							if returning && !(strings.HasPrefix(query, "DELETE") && i == 0) {
								count = 1
							}
							before := r.snapshot()
							writerBegins, writerEnds := r.writer.begins, r.writer.ends
							foreignAnalyzePlan(t, p, executor, analyzed, false, count, args...)
							if !b.PG {
								after := r.snapshot()
								fdwReleased(t, r, before, after.begins-before.begins)
								if r.writer.begins-writerBegins != r.writer.ends-writerEnds {
									t.Fatal("foreign writer identity scan was not released")
								}
							}
							run(fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", states[i]))
							fdwAssertSource(t, p, b, r, states[i])
						}
					}()
				}
			})
		}
	}
}
