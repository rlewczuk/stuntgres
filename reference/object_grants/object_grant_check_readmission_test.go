package reference_test

import (
	"context"
	"regexp"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantCheckReadmissionAfterUniqueWait(t *testing.T) {
	for _, folded := range []bool{false, true} {
		for _, route := range []string{"direct", "prepared", "held"} {
			t.Run(map[bool]string{false: "row", true: "folded"}[folded]+"/"+route, func(t *testing.T) {
				objectGrantCheckWaitScenario(t, folded, route, false, false)
			})
		}
	}
}

func TestReferenceObjectGrantCheckReadmissionCancellation(t *testing.T) {
	for _, folded := range []bool{false, true} {
		for _, route := range []string{"direct", "prepared", "held"} {
			t.Run(map[bool]string{false: "row", true: "folded"}[folded]+"/"+route, func(t *testing.T) {
				objectGrantCheckWaitScenario(t, folded, route, true, false)
			})
		}
	}
}

// A primitive JSON column, ordinary CHECKs and a plain UNIQUE key isolate the
// consumed CHECK footprint. No callable domain or expression index can supply it.
func objectGrantCheckWaitScenario(t *testing.T, folded bool, route string, canceled, smoke bool) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"check_wait_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close()
			holder, worker := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE check_wait_reader", 0))
			defer func() {
				for _, query := range []string{
					"GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO PUBLIC",
					"REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM check_wait_reader CASCADE",
				} {
					if _, err := admin.ExecContext(p.Ctx, query); err != nil {
						t.Error("CHECK routine ACL cleanup", query, err)
					}
				}
			}()
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO check_wait_reader", 0))
			run(fixture.Exec("CREATE SEQUENCE public.check_wait_steps START 1", 0))
			run(fixture.Exec("CREATE TABLE public.check_wait_rows(k INT CONSTRAINT check_wait_unique UNIQUE,j JSON,CONSTRAINT a_call CHECK("+objectGrantCheckExpression(folded)+"),CONSTRAINT z_truth CHECK(nextval('public.check_wait_steps'::regclass)>0))", 0))
			run(fixture.Exec("INSERT INTO public.check_wait_rows VALUES(99,'[9]')", 1))
			run(fixture.Exec("GRANT INSERT,SELECT ON public.check_wait_rows TO check_wait_reader", 0))
			run(fixture.Exec("GRANT USAGE ON SEQUENCE public.check_wait_steps TO check_wait_reader", 0))
			fixture.RunStep(t, p.Ctx, worker, b.Recorder, fixture.Exec("SET ROLE check_wait_reader", 0))
			query := "INSERT INTO public.check_wait_rows VALUES(8,'[1,2]'),(7,'[1,2]') RETURNING k AS v"
			if smoke {
				query = "INSERT INTO public.check_wait_rows VALUES(7,'[1,2]') RETURNING k AS v"
			}
			var executor fixture.Executor = worker
			if route == "held" {
				stmt, err := worker.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { stmt.Close() })
				executor = fixture.HeldStatement{Executor: worker, Statement: stmt, SQL: query}
				warm := fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(7)}})
				warm.Bag = true
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, warm)
				run(fixture.Exec("DELETE FROM public.check_wait_rows WHERE k=8", 1))
				run(fixture.Exec("DELETE FROM public.check_wait_rows WHERE k=7", 1))
			}
			run(fixture.Exec("ALTER SEQUENCE public.check_wait_steps RESTART WITH 1", 0))
			var holderPID, workerPID int
			if b.PG {
				holderPID, workerPID = fixture.RowPIDs(t, p.Ctx, holder, worker)
			}
			fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("INSERT INTO public.check_wait_rows VALUES(7,'[1]')", 1))
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			completed := make(chan struct{})
			join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(completed)
				return fixture.RowQuery(ctx, executor, query, route == "prepared")
			})
			if b.PG {
				fixture.RowWait(t, p.Ctx, workerPID, holderPID)
			} else {
				fixture.NativeUniqueKeyWait(t, p.Ctx, completed)
			}
			steps := int64(3)
			if smoke {
				steps = 2
			}
			sequence := func(want int64) {
				run(fixture.Select("SELECT last_value,is_called FROM public.check_wait_steps", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{want, true}}))
			}
			sequence(steps)
			if canceled {
				cancel()
			} else {
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC", 0))
			}
			// Cancellation joins before holder release; revocation resumes only after
			// rollback removes the conflict, so 23505 cannot mask freshness denial.
			if !canceled {
				fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("ROLLBACK", 0))
			}
			outcome := join()
			if outcome.Stmt != nil {
				defer outcome.Stmt.Close()
			}
			if outcome.Rows != nil {
				defer outcome.Rows.Close()
			}
			if b.PG && !canceled {
				if smoke {
					objectGrantCheckReturned(t, outcome, 7)
				} else {
					objectGrantCheckReturned(t, outcome, 8, 7)
				}
			} else {
				if outcome.Rows != nil || outcome.Result != nil {
					t.Fatal("failed execute exposed a cursor/result prefix", outcome)
				}
				if canceled && !b.PG {
					fixture.LexicalOrdinary(t, outcome.Err, context.Canceled)
				} else {
					want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function json_array_length")
					if canceled {
						want = fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						actual := fixture.ExtractDiagnostic(t, outcome.Err)
						if !regexp.MustCompile(`^while inserting index tuple \([0-9]+,[0-9]+\) in relation "check_wait_unique"$`).MatchString(actual.Context) {
							t.Fatal("unexpected PostgreSQL unique-wait cancellation Context", actual.Context)
						}
						want.Context = actual.Context
					}
					// Prepare succeeded (including fresh prepared); RowQuery's terminal
					// error is the execute-stage QueryContext diagnostic, in full.
					fixture.AssertDiagnostic(t, outcome.Err, &want)
				}
			}
			sequence(steps)
			if canceled {
				fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Select("SELECT k FROM public.check_wait_rows ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(99)}}))
				fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("ROLLBACK", 0))
			}
			stored := [][]any{{int64(99)}}
			if b.PG && !canceled {
				stored = [][]any{{int64(7)}, {int64(8)}, {int64(99)}}
				if smoke {
					stored = [][]any{{int64(7)}, {int64(99)}}
				}
			}
			run(fixture.Select("SELECT k FROM public.check_wait_rows ORDER BY k", []string{"k"}, []string{"INT4"}, stored))
			// Use a healthy fresh borrower after pq context cancellation.
			fresh, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			freshRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, fresh, b.Recorder, s) }
			freshRun(fixture.Exec("SET ROLE check_wait_reader", 0))
			if !canceled {
				freshRun(objectGrantCheckDenied("INSERT INTO public.check_wait_rows VALUES(10,'[1]') RETURNING k"))
			}
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO PUBLIC", 0))
			if b.PG && !canceled {
				run(fixture.Exec("DELETE FROM public.check_wait_rows WHERE k=7", 1))
				if !smoke {
					run(fixture.Exec("DELETE FROM public.check_wait_rows WHERE k=8", 1))
				}
			}
			freshRun(fixture.Select("INSERT INTO public.check_wait_rows VALUES(7,'[1]') RETURNING k", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
			if !smoke {
				freshRun(fixture.Exec("INSERT INTO public.check_wait_rows VALUES(8,'[1]')", 1))
				sequence(steps + 2)
			} else {
				sequence(steps + 1)
				freshRun(fixture.Exec("INSERT INTO public.check_wait_rows VALUES(11,'[1]')", 1))
				run(fixture.Exec("DELETE FROM public.check_wait_rows WHERE k=11", 1))
				freshRun(fixture.Select("SELECT k FROM public.check_wait_rows ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(99)}}))
			}
		})
	}
}

// INSERT RETURNING has no portable ORDER BY promise. Check its complete typed
// bag, including multiplicity, rather than imposing the native producer order.
func objectGrantCheckReturned(t *testing.T, outcome fixture.RelationOutcome, want ...int64) {
	t.Helper()
	if outcome.Err != nil {
		t.Fatal(outcome.Err)
	}
	if outcome.Rows == nil || outcome.Result != nil {
		t.Fatal("missing RETURNING cursor", outcome)
	}
	defer outcome.Rows.Close()
	columns, err := outcome.Rows.Columns()
	if err != nil || len(columns) != 1 || columns[0] != "v" {
		t.Fatal(columns, err)
	}
	types, err := outcome.Rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
		t.Fatal(types, err)
	}
	bag := make(map[int64]int)
	for _, k := range want {
		bag[k]++
	}
	count := 0
	for outcome.Rows.Next() {
		var cell any
		if err := outcome.Rows.Scan(&cell); err != nil {
			t.Fatal(err)
		}
		k, ok := fixture.Normalize(cell).(int64)
		if !ok || bag[k] == 0 {
			t.Fatal("unexpected RETURNING value", cell, bag)
		}
		bag[k]--
		count++
	}
	if err := outcome.Rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != len(want) {
		t.Fatal("RETURNING row count", count, len(want))
	}
	for k, remaining := range bag {
		if remaining != 0 {
			t.Fatal("missing RETURNING value", k, remaining)
		}
	}
	if err := outcome.Rows.Close(); err != nil {
		t.Fatal(err)
	}
}
