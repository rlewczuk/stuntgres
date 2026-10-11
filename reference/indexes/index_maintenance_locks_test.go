package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIndexMaintenanceLocks(t *testing.T) {
	for _, q := range []string{"REINDEX INDEX ix", "REINDEX TABLE t", "DROP INDEX ix"} {
		for _, timer := range []struct{ setting, code, message string }{
			{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
			{"statement_timeout", "57014", "canceling statement due to statement timeout"},
		} {
			t.Run(q+"/"+timer.setting, func(t *testing.T) {
				indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(n INT)", 0), fixture.Exec("INSERT INTO t VALUES(1)", 1), fixture.Exec("CREATE INDEX ix ON t(n)", 0)} {
						fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
					}
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					fixture.LockExec(t, p.Ctx, a, "LOCK TABLE t IN ROW EXCLUSIVE MODE", 0)
					fixture.LockExec(t, p.Ctx, c, "SET "+timer.setting+"='50ms'", 0)
					s := fixture.Prepared(fixture.ImplicitError(q, fixture.Diagnostic{Code: timer.code, Message: timer.message}))
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.LockExec(t, p.Ctx, c, "SET "+timer.setting+"=0", 0)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT relname FROM pg_class WHERE relname='ix'", []string{"relname"}, []string{"NAME"}, [][]any{{"ix"}}))
					fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			})
		}
	}
	for _, commit := range []bool{false, true} {
		for _, q := range []string{"REINDEX INDEX ix", "REINDEX TABLE t"} {
			t.Run(q+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
				indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(n INT)", 0), fixture.Exec("INSERT INTO t VALUES(1)", 1), fixture.Exec("CREATE INDEX ix ON t((n+1),n)", 0)} {
						fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
					}
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(2)", 1)
					// First establish conflict independently, including native without pg_locks.
					fixture.LockExec(t, p.Ctx, c, "SET lock_timeout='50ms'", 0)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"}))
					fixture.LockExec(t, p.Ctx, c, "SET lock_timeout=0", 0)
					var holder, waiter int
					if b.PG {
						holder = fixture.LockPID(t, p.Ctx, a)
						waiter = fixture.LockPID(t, p.Ctx, c)
					}
					done := fixture.RowAsync(t, p.Ctx, c, q, true, false)
					if b.PG {
						fixture.LockWaitBehind(t, p.Ctx, waiter, holder, fixture.LockOID(t, p.Ctx, "t"), "ShareLock")
					}
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					fixture.LockExec(t, p.Ctx, a, end, 0)
					r := fixture.LockResult(t, done)
					fixture.State(t, r.Err, "")
					fixture.LockAffected(t, r.Result, 0)
					rows := [][]any{{int64(1)}}
					if commit {
						rows = append(rows, []any{int64(2)})
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, rows))
				})
			})
		}
	}
}

func TestReferenceIndexMaintenanceLockAdmission(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(n INT)", 0)
		fixture.LockExec(t, p.Ctx, a, "CREATE INDEX ix ON t(n)", 0)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
		fixture.LockExec(t, p.Ctx, c, "SET lock_timeout='50ms'", 0)
		fixture.LockExec(t, p.Ctx, c, "BEGIN READ ONLY", 0)
		for _, q := range []string{"REINDEX INDEX ix", "REINDEX TABLE t", "DROP INDEX ix", "ALTER INDEX ix RENAME TO fresh"} {
			tag := "REINDEX"
			if q == "DROP INDEX ix" {
				tag = "DROP INDEX"
			}
			if q == "ALTER INDEX ix RENAME TO fresh" {
				tag = "ALTER INDEX"
			}
			want := fixture.Diagnostic{Code: "25006", Message: "cannot execute " + tag + " in a read-only transaction"}
			if b.PG && tag == "REINDEX" {
				want = fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.ImplicitError(q, want)))
			// A PostgreSQL error aborts the transaction; each admission gets a fresh one.
			fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
			fixture.LockExec(t, p.Ctx, c, "BEGIN READ ONLY", 0)
		}
		fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
		fixture.LockExec(t, p.Ctx, c, "SET lock_timeout=0", 0)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
	})
}

func TestReferenceIndexMaintenanceLockCancellation(t *testing.T) {
	for _, q := range []string{"REINDEX INDEX ix", "DROP INDEX ix"} {
		t.Run(q, func(t *testing.T) {
			indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(n INT)", 0)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1)", 1)
				fixture.LockExec(t, p.Ctx, a, "CREATE INDEX ix ON t(n)", 0)
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				fixture.LockExec(t, p.Ctx, a, "LOCK TABLE t IN ROW EXCLUSIVE MODE", 0)
				if b.PG {
					pid := fixture.LockPID(t, p.Ctx, c)
					holder := fixture.LockPID(t, p.Ctx, a)
					done := fixture.RowAsync(t, p.Ctx, c, q, true, false)
					fixture.RowWait(t, p.Ctx, pid, holder)
					var canceled bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
						t.Fatal("cancel admitted utility", canceled, err)
					}
					r := fixture.LockResult(t, done)
					want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
					fixture.AssertDiagnostic(t, r.Err, &want)
					if r.Result != nil {
						t.Fatal("canceled result", r.Result)
					}
				} else {
					// Actual native rebuild extraction/sort and final-publication veto require
					// engine barriers, not a race-prone pre-canceled public SQL substitute.
					fixture.LockExec(t, p.Ctx, c, "SET statement_timeout='50ms'", 0)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "57014", Message: "canceling statement due to statement timeout"}))
					fixture.LockExec(t, p.Ctx, c, "SET statement_timeout=0", 0)
				}
				fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}

func TestReferenceIndexMaintenanceLockModesOracle(t *testing.T) {
	// Ordinary PG utilities hold transaction-scoped locks. Native utilities are
	// intentionally excluded in writable explicit transactions; internal barrier
	// units must establish their instantaneous lock modes/order, not emulate PG.
	for _, q := range []string{"ALTER INDEX ix RENAME TO fresh", "DROP INDEX ix", "REINDEX INDEX ix", "REINDEX TABLE t"} {
		t.Run(q, func(t *testing.T) {
			indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(n INT)", 0)
				fixture.LockExec(t, p.Ctx, a, "CREATE INDEX ix ON t(n)", 0)
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				if b.PG {
					fixture.LockExec(t, p.Ctx, a, q, 0)
					pid := fixture.LockPID(t, p.Ctx, a)
					wantLocks := [][]any{{"ix", "AccessExclusiveLock", true}, {"t", "ShareLock", true}}
					if q == "ALTER INDEX ix RENAME TO fresh" {
						wantLocks = [][]any{{"ix", "ShareUpdateExclusiveLock", true}}
					}
					if q == "DROP INDEX ix" {
						wantLocks = [][]any{{"ix", "AccessExclusiveLock", true}, {"t", "AccessExclusiveLock", true}}
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT c.relname,l.mode,l.granted FROM pg_locks l JOIN pg_class c ON c.oid=l.relation WHERE l.pid="+fixture.Position(pid)+" AND c.relname IN ('t','ix','fresh') ORDER BY c.relname,l.mode", []string{"relname", "mode", "granted"}, []string{"NAME", "TEXT", "BOOL"}, wantLocks))
					fixture.LockExec(t, p.Ctx, c, "SET lock_timeout='50ms'", 0)
					if q == "DROP INDEX ix" {
						s := fixture.ImplicitError("SELECT n FROM t", fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout", Position: "15"})
						s.SelectRows = true
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError("INSERT INTO t VALUES(2)", fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout", Position: "13"}))
					}
					if q == "REINDEX INDEX ix" || q == "REINDEX TABLE t" {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ImplicitError("INSERT INTO t VALUES(2)", fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout", Position: "13"}))
					}
					if q == "ALTER INDEX ix RENAME TO fresh" {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO t VALUES(2)", 1))
					}
					fixture.LockExec(t, p.Ctx, c, "SET lock_timeout=0", 0)
				} else {
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
				}
				fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			})
		})
	}
}
