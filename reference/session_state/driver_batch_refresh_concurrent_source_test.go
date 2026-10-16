package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

type concurrentBatchSourceOutcome struct {
	sets [][][]any
	err  error
}

// Drain the real public multi-result cursor, including its terminal error.
func concurrentBatchSourceWorker(ctx context.Context, conn *sql.Conn, q string, done chan<- concurrentBatchSourceOutcome) {
	out := concurrentBatchSourceOutcome{}
	rows, err := conn.QueryContext(ctx, q)
	if err != nil {
		out.err = err
		done <- out
		return
	}
	defer rows.Close()
	for {
		columns, err := rows.Columns()
		if err != nil {
			out.err = err
			break
		}
		if len(columns) != 0 {
			types, err := rows.ColumnTypes()
			if err != nil {
				out.err = err
				break
			}
			if !reflect.DeepEqual(columns, []string{"i", "s", "gate"}) || len(types) != 3 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" || types[2].DatabaseTypeName() != "TEXT" {
				out.err = fmt.Errorf("unexpected retained source metadata: %v %v", columns, types)
				break
			}
			var set [][]any
			for rows.Next() {
				var i int64
				var s, gate any
				if err := rows.Scan(&i, &s, &gate); err != nil {
					out.err = err
					break
				}
				set = append(set, []any{i, s, gate})
			}
			out.sets = append(out.sets, set)
		} else {
			for rows.Next() {
			}
		}
		if out.err != nil || !rows.NextResultSet() {
			break
		}
	}
	if out.err == nil {
		out.err = rows.Err()
	}
	done <- out
}

func concurrentBatchSourceWait(t *testing.T, ctx context.Context, done <-chan concurrentBatchSourceOutcome, joined *bool) {
	t.Helper()
	profiles := make([]runtime.StackRecord, 128)
	for {
		select {
		case out := <-done:
			*joined = true
			t.Fatal("retained source completed before wait", out.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
		}
		n, ok := runtime.GoroutineProfile(profiles)
		if !ok {
			profiles = make([]runtime.StackRecord, n)
			continue
		}
		for _, profile := range profiles[:n] {
			frames := runtime.CallersFrames(profile.Stack())
			wait, source, worker := false, false, false
			for {
				frame, more := frames.Next()
				wait = wait || strings.HasSuffix(frame.Function, ".(*relationRequest).wait")
				source = source || strings.HasSuffix(frame.Function, ".acquireBoundViewRelationModes")
				worker = worker || strings.HasSuffix(frame.Function, ".concurrentBatchSourceWorker")
				if !more {
					break
				}
			}
			if wait && source && worker {
				return
			}
		}
		runtime.Gosched()
	}
}
func TestReferenceMaterializedRefreshConcurrentRetainedSourceMaterialized(t *testing.T) {
	for _, lateError := range []bool{false, true} {
		t.Run(map[bool]string{false: "publish", true: "late_error_rollback"}[lateError], func(t *testing.T) {
			concurrentBatchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
				observer, err := f.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer observer.Close()
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, observer, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE retained_source_base(i INT,s TEXT)", 0))
				run(fixture.Exec("INSERT INTO retained_source_base VALUES(1,'old'),(2,NULL)", 2))
				run(fixture.Exec("CREATE MATERIALIZED VIEW retained_source_inner AS SELECT i,s FROM retained_source_base", 2))
				run(fixture.Exec("CREATE UNIQUE INDEX retained_source_inner_u ON retained_source_inner(i)", 0))
				// The MV lock is granted before the later ordinary source lock waits.
				run(fixture.Exec("CREATE TABLE retained_source_gate(s TEXT)", 0))
				run(fixture.Exec("INSERT INTO retained_source_gate VALUES('gate old')", 1))
				run(fixture.Exec("CREATE MATERIALIZED VIEW retained_source_outer AS SELECT a.i,a.s,g.s AS gate FROM retained_source_inner a JOIN retained_source_inner b ON a.i=b.i CROSS JOIN retained_source_gate g", 2))
				run(fixture.Exec("CREATE UNIQUE INDEX retained_source_outer_u ON retained_source_outer(i)", 0))
				run(fixture.Exec("CREATE TABLE retained_source_marker(i INT)", 0))
				old := [][]any{{int64(1), "old", "gate old"}, {int64(2), nil, "gate old"}}
				fresh := [][]any{{int64(1), "new", "gate new"}, {int64(3), "three", "gate new"}}
				selectOuter := "SELECT i,s,gate FROM retained_source_outer ORDER BY i"
				assertOuter := func(want [][]any) {
					run(fixture.Select(selectOuter, []string{"i", "s", "gate"}, []string{"INT4", "TEXT", "TEXT"}, want))
				}
				holder := fixture.QueryLockBegin(t, p.Ctx, f.B)
				fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("LOCK TABLE retained_source_gate IN ACCESS EXCLUSIVE MODE", 0))
				fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("UPDATE retained_source_gate SET s='gate new'", 1))
				ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
				done := make(chan concurrentBatchSourceOutcome, 1)
				joined := false
				defer func() {
					cancel()
					holder.Rollback()
					if !joined {
						cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
						defer stop()
						select {
						case <-done:
						case <-cleanup.Done():
							t.Error("retained source cleanup", cleanup.Err())
						}
					}
				}()
				pid, blocker := 0, 0
				var gateOID, innerOID int64
				if b.PG {
					pid = fixture.LockPID(t, p.Ctx, f.A)
					blocker = fixture.LockPID(t, p.Ctx, f.B)
					gateOID = fixture.LockOID(t, p.Ctx, "retained_source_gate")
					innerOID = fixture.LockOID(t, p.Ctx, "retained_source_inner")
				}
				q := "INSERT INTO retained_source_marker VALUES(1); " + selectOuter + "; REFRESH MATERIALIZED VIEW CONCURRENTLY retained_source_outer; " + selectOuter
				if lateError {
					q += "; SELECT missing FROM retained_source_outer"
				}
				go concurrentBatchSourceWorker(ctx, f.A, q, done)
				if b.PG {
					fixture.LockWaitBehind(t, ctx, pid, blocker, gateOID, "AccessShareLock")
					var granted bool
					if err := fixture.Admin.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation=$2 AND mode='AccessShareLock' AND granted)", pid, innerOID).Scan(&granted); err != nil || !granted {
						t.Fatal("retained inner source lock", granted, err)
					}
				} else {
					concurrentBatchSourceWait(t, ctx, done, &joined)
				}
				assertOuter(old)
				run(fixture.Exec("UPDATE retained_source_base SET s='new' WHERE i=1", 1))
				run(fixture.Exec("DELETE FROM retained_source_base WHERE i=2", 1))
				run(fixture.Exec("INSERT INTO retained_source_base VALUES(3,'three')", 1))
				publish, stop := context.WithTimeout(ctx, 3*time.Second)
				fixture.RunStep(t, publish, observer, b.Recorder, fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY retained_source_inner", 0))
				stop()
				run(fixture.Select("SELECT i,s FROM retained_source_inner ORDER BY i", []string{"i", "s"}, []string{"INT4", "TEXT"}, concurrentBatchNew()))
				if err := holder.Commit(); err != nil {
					t.Fatal(err)
				}
				var out concurrentBatchSourceOutcome
				select {
				case out = <-done:
					joined = true
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if !reflect.DeepEqual(out.sets, [][][]any{old, old}) {
					t.Fatalf("retained old active source outputs: %v, error %v", out.sets, out.err)
				}
				if lateError {
					d := batchDiag("42703", `column "missing" does not exist`)
					d.Position = batchPosition(q, "missing")
					fixture.AssertDiagnostic(t, out.err, &d)
				} else if out.err != nil {
					t.Fatal(out.err)
				}
				assertOuter(old)
				var marker [][]any
				if !lateError {
					marker = [][]any{{int64(1)}}
				}
				run(fixture.Select("SELECT i FROM retained_source_marker", []string{"i"}, []string{"INT4"}, marker))
				fixture.MaterializedViewPopulation(run, "retained_source_outer", true)
				run(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY retained_source_outer", 0))
				assertOuter(fresh)
			})
		})
	}
}
