package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceMaterializedRefreshConcurrentSnapshots(t *testing.T) {
	for _, unrelated := range []bool{false, true} {
		t.Run(map[bool]string{false: "materialized_first", true: "database_snapshot_first"}[unrelated], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					reader, writer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, writer, b.Recorder, s) }
					concurrentSeed(run)
					concurrentIndex(run)
					run(fixture.Exec("CREATE TABLE concurrent_unrelated(i INT)", 0))
					run(fixture.Exec("INSERT INTO concurrent_unrelated VALUES(9)", 1))
					tx, err := reader.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
					if err != nil {
						t.Fatal(err)
					}
					fixture.QueryLockRollbackCleanup(t, tx)
					if unrelated {
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT i FROM concurrent_unrelated", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
					} else {
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, concurrentOld())
					}
					concurrentChange(run)
					ctx, stop := context.WithTimeout(p.Ctx, 5*time.Second)
					defer stop()
					fixture.RunStep(t, ctx, writer, b.Recorder, fixture.Exec(concurrentRefreshSQL, 0))
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, concurrentOld())
					run(concurrentNew())
					run(fixture.Exec("UPDATE concurrent_base SET s='newest' WHERE i=1", 1))
					fixture.RunStep(t, ctx, writer, b.Recorder, fixture.Exec(concurrentRefreshSQL, 0))
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, concurrentOld())
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Prepared(concurrentOld()))
					newest := refreshBag("concurrent_mv", [][]any{{int64(1), "newest"}, {int64(3), "three"}})
					run(newest)
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, reader, b.Recorder, newest)
				})
			}
		})
	}
}

func TestReferenceMaterializedRefreshConcurrentTransactions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, observer, b.Recorder, s) }
			concurrentSeed(run)
			concurrentIndex(run)
			concurrentChange(run)
			tx, err := conn.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			fixture.QueryLockRollbackCleanup(t, tx)
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic(concurrentRefreshSQL, fixture.ControlsReferenceDiagnostic("25006", "cannot execute REFRESH MATERIALIZED VIEW in a read-only transaction")))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			for _, commit := range []bool{false, true} {
				tx, err := conn.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				fixture.QueryLockRollbackCleanup(t, tx)
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(concurrentRefreshSQL, 0))
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, concurrentNew())
				run(concurrentOld())
				// Transaction-owned EXCLUSIVE remains until commit/rollback, not statement end.
				worker, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer worker.Close()
				pid, blocker := 0, 0
				var oid int64
				if b.PG {
					pid = fixture.LockPID(t, p.Ctx, worker)
					blocker = fixture.LockPID(t, p.Ctx, conn)
					oid = fixture.LockOID(t, p.Ctx, "concurrent_mv")
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				finished := make(chan struct{})
				join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					result, err := worker.ExecContext(ctx, concurrentRefreshSQL)
					return fixture.RelationOutcome{Result: result, Err: err}
				})
				fixture.RelationLockPending(t, p, b, pid, blocker, oid, "ExclusiveLock", finished)
				if commit {
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					outcome := join()
					if outcome.Err != nil {
						t.Fatal(outcome.Err)
					}
					n, err := outcome.Result.RowsAffected()
					if err != nil || n != 0 {
						t.Fatal(n, err)
					}
				} else {
					if b.PG {
						var signaled bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
							t.Fatal("cancel queued transaction refresh", signaled, err)
						}
					} else {
						cancel()
					}
					outcome := join()
					if b.PG {
						d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						fixture.AssertDiagnostic(t, outcome.Err, &d)
					} else if !errors.Is(outcome.Err, context.Canceled) {
						t.Fatal("native queued cancellation", outcome.Err)
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					run(concurrentOld())
					fixture.RunStep(t, p.Ctx, worker, b.Recorder, fixture.Select("SELECT 42 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(42)}}))
				}
				if commit {
					run(concurrentNew())
				} else {
					run(concurrentOld())
				}
			}
		})
	}
}

func TestReferenceMaterializedRefreshConcurrentControls(t *testing.T) {
	p := fixture.NewScenario(t)
	var dump []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			concurrentSeed(run)
			run(fixture.Exec("CREATE UNIQUE INDEX concurrent_u ON concurrent_mv(i) INCLUDE(s) NULLS NOT DISTINCT", 0))
			concurrentChange(run)
			run(fixture.Exec(concurrentRefreshSQL, 0))
			run(fixture.Exec("INSERT INTO concurrent_base VALUES(4,'diverged')", 1))
			check := func() {
				run(concurrentNew())
				run(fixture.Prepared(concurrentNew()))
				fixture.MaterializedViewPopulation(run, "concurrent_mv", true)
				run(fixture.Select("SELECT count(*) AS n FROM concurrent_base", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
				run(fixture.Select("SELECT indexdef FROM pg_indexes WHERE indexname='concurrent_u'", []string{"indexdef"}, []string{"TEXT"}, [][]any{{"CREATE UNIQUE INDEX concurrent_u ON public.concurrent_mv USING btree (i) INCLUDE (s) NULLS NOT DISTINCT"}}))
				run(fixture.Select("SELECT pg_get_viewdef('concurrent_mv'::regclass,true) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{" SELECT i,\n    s\n   FROM concurrent_base;"}}))
			}
			check()
			if !b.PG {
				dump = concurrentDump(t, p)
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				check()
				fresh := fixture.NewFixture(t, false, true)
				if err := stuntgres.Restore(p.Ctx, fresh.Native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, fresh.Memory, fresh.MemoryRecord, concurrentNew())
				fixture.RunStep(t, p.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Exec(concurrentRefreshSQL, 0))
				fixture.RunStep(t, p.Ctx, fresh.Memory, fresh.MemoryRecord, refreshBag("concurrent_mv", [][]any{{int64(1), "new"}, {int64(3), "three"}, {int64(4), "diverged"}}))
			} else if dump != nil {
				run(fixture.Exec("DROP MATERIALIZED VIEW concurrent_mv", 0))
				run(fixture.Exec("DROP TABLE concurrent_base", 0))
				if _, err := conn.ExecContext(p.Ctx, string(dump)); err != nil {
					t.Fatal("PG native dump replay", err)
				}
				run(fixture.MaterializedViewUnpopulated("concurrent_mv"))
				run(fixture.Exec("REFRESH MATERIALIZED VIEW concurrent_mv", 0))
			}
			run(fixture.Exec(concurrentRefreshSQL, 0))
			run(refreshBag("concurrent_mv", [][]any{{int64(1), "new"}, {int64(3), "three"}, {int64(4), "diverged"}}))
		})
	}
}
