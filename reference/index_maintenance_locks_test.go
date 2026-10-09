package reference_test

import "testing"

func TestReferenceIndexMaintenanceLocks(t *testing.T) {
	for _, q := range []string{"REINDEX INDEX ix", "REINDEX TABLE t", "DROP INDEX ix"} {
		for _, timer := range []struct{ setting, code, message string }{
			{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
			{"statement_timeout", "57014", "canceling statement due to statement timeout"},
		} {
			t.Run(q+"/"+timer.setting, func(t *testing.T) {
				indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, c := referenceLockConnections(t, p.ctx, b.db)
					for _, s := range []referenceStep{referenceExec("CREATE TABLE t(n INT)", 0), referenceExec("INSERT INTO t VALUES(1)", 1), referenceExec("CREATE INDEX ix ON t(n)", 0)} {
						referenceRunStep(t, p.ctx, a, b.recorder, s)
					}
					referenceLockExec(t, p.ctx, a, "BEGIN", 0)
					referenceLockExec(t, p.ctx, a, "LOCK TABLE t IN ROW EXCLUSIVE MODE", 0)
					referenceLockExec(t, p.ctx, c, "SET "+timer.setting+"='50ms'", 0)
					s := referencePrepared(referenceImplicitError(q, referenceDiagnostic{Code: timer.code, Message: timer.message}))
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, c, b.recorder, s)
					referenceLockExec(t, p.ctx, c, "SET "+timer.setting+"=0", 0)
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT relname FROM pg_class WHERE relname='ix'", []string{"relname"}, []string{"NAME"}, [][]any{{"ix"}}))
					referenceLockExec(t, p.ctx, a, "ROLLBACK", 0)
					referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(q, 0))
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			})
		}
	}
	for _, commit := range []bool{false, true} {
		for _, q := range []string{"REINDEX INDEX ix", "REINDEX TABLE t"} {
			t.Run(q+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
				indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, c := referenceLockConnections(t, p.ctx, b.db)
					for _, s := range []referenceStep{referenceExec("CREATE TABLE t(n INT)", 0), referenceExec("INSERT INTO t VALUES(1)", 1), referenceExec("CREATE INDEX ix ON t((n+1),n)", 0)} {
						referenceRunStep(t, p.ctx, a, b.recorder, s)
					}
					referenceLockExec(t, p.ctx, a, "BEGIN", 0)
					referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(2)", 1)
					// First establish conflict independently, including native without pg_locks.
					referenceLockExec(t, p.ctx, c, "SET lock_timeout='50ms'", 0)
					referenceRunStep(t, p.ctx, c, b.recorder, referenceImplicitError(q, referenceDiagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"}))
					referenceLockExec(t, p.ctx, c, "SET lock_timeout=0", 0)
					var holder, waiter int
					if b.pg {
						holder = referenceLockPID(t, p.ctx, a)
						waiter = referenceLockPID(t, p.ctx, c)
					}
					done := referenceRowAsync(t, p.ctx, c, q, true, false)
					if b.pg {
						referenceLockWaitBehind(t, p.ctx, waiter, holder, referenceLockOID(t, p.ctx, "t"), "ShareLock")
					}
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					referenceLockExec(t, p.ctx, a, end, 0)
					r := referenceLockResult(t, done)
					referenceState(t, r.err, "")
					referenceLockAffected(t, r.result, 0)
					rows := [][]any{{int64(1)}}
					if commit {
						rows = append(rows, []any{int64(2)})
					}
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, rows))
				})
			})
		}
	}
}

func TestReferenceIndexMaintenanceLockAdmission(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, c := referenceLockConnections(t, p.ctx, b.db)
		referenceLockExec(t, p.ctx, a, "CREATE TABLE t(n INT)", 0)
		referenceLockExec(t, p.ctx, a, "CREATE INDEX ix ON t(n)", 0)
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		referenceLockExec(t, p.ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
		referenceLockExec(t, p.ctx, c, "SET lock_timeout='50ms'", 0)
		referenceLockExec(t, p.ctx, c, "BEGIN READ ONLY", 0)
		for _, q := range []string{"REINDEX INDEX ix", "REINDEX TABLE t", "DROP INDEX ix", "ALTER INDEX ix RENAME TO fresh"} {
			tag := "REINDEX"
			if q == "DROP INDEX ix" {
				tag = "DROP INDEX"
			}
			if q == "ALTER INDEX ix RENAME TO fresh" {
				tag = "ALTER INDEX"
			}
			want := referenceDiagnostic{Code: "25006", Message: "cannot execute " + tag + " in a read-only transaction"}
			if b.pg && tag == "REINDEX" {
				want = referenceDiagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"}
			}
			referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(referenceImplicitError(q, want)))
			// A PostgreSQL error aborts the transaction; each admission gets a fresh one.
			referenceLockExec(t, p.ctx, c, "ROLLBACK", 0)
			referenceLockExec(t, p.ctx, c, "BEGIN READ ONLY", 0)
		}
		referenceLockExec(t, p.ctx, c, "ROLLBACK", 0)
		referenceLockExec(t, p.ctx, c, "SET lock_timeout=0", 0)
		referenceLockExec(t, p.ctx, a, "ROLLBACK", 0)
	})
}

func TestReferenceIndexMaintenanceLockCancellation(t *testing.T) {
	for _, q := range []string{"REINDEX INDEX ix", "DROP INDEX ix"} {
		t.Run(q, func(t *testing.T) {
			indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c := referenceLockConnections(t, p.ctx, b.db)
				referenceLockExec(t, p.ctx, a, "CREATE TABLE t(n INT)", 0)
				referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1)", 1)
				referenceLockExec(t, p.ctx, a, "CREATE INDEX ix ON t(n)", 0)
				referenceLockExec(t, p.ctx, a, "BEGIN", 0)
				referenceLockExec(t, p.ctx, a, "LOCK TABLE t IN ROW EXCLUSIVE MODE", 0)
				if b.pg {
					pid := referenceLockPID(t, p.ctx, c)
					holder := referenceLockPID(t, p.ctx, a)
					done := referenceRowAsync(t, p.ctx, c, q, true, false)
					referenceRowWait(t, p.ctx, pid, holder)
					var canceled bool
					if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
						t.Fatal("cancel admitted utility", canceled, err)
					}
					r := referenceLockResult(t, done)
					want := controlsReferenceDiagnostic("57014", "canceling statement due to user request")
					referenceAssertDiagnostic(t, r.err, &want)
					if r.result != nil {
						t.Fatal("canceled result", r.result)
					}
				} else {
					// Actual native rebuild extraction/sort and final-publication veto require
					// engine barriers, not a race-prone pre-canceled public SQL substitute.
					referenceLockExec(t, p.ctx, c, "SET statement_timeout='50ms'", 0)
					referenceRunStep(t, p.ctx, c, b.recorder, referenceImplicitError(q, referenceDiagnostic{Code: "57014", Message: "canceling statement due to statement timeout"}))
					referenceLockExec(t, p.ctx, c, "SET statement_timeout=0", 0)
				}
				referenceLockExec(t, p.ctx, a, "ROLLBACK", 0)
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(q, 0))
				referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
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
			indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c := referenceLockConnections(t, p.ctx, b.db)
				referenceLockExec(t, p.ctx, a, "CREATE TABLE t(n INT)", 0)
				referenceLockExec(t, p.ctx, a, "CREATE INDEX ix ON t(n)", 0)
				referenceLockExec(t, p.ctx, a, "BEGIN", 0)
				if b.pg {
					referenceLockExec(t, p.ctx, a, q, 0)
					pid := referenceLockPID(t, p.ctx, a)
					wantLocks := [][]any{{"ix", "AccessExclusiveLock", true}, {"t", "ShareLock", true}}
					if q == "ALTER INDEX ix RENAME TO fresh" {
						wantLocks = [][]any{{"ix", "ShareUpdateExclusiveLock", true}}
					}
					if q == "DROP INDEX ix" {
						wantLocks = [][]any{{"ix", "AccessExclusiveLock", true}, {"t", "AccessExclusiveLock", true}}
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT c.relname,l.mode,l.granted FROM pg_locks l JOIN pg_class c ON c.oid=l.relation WHERE l.pid="+referencePosition(pid)+" AND c.relname IN ('t','ix','fresh') ORDER BY c.relname,l.mode", []string{"relname", "mode", "granted"}, []string{"NAME", "TEXT", "BOOL"}, wantLocks))
					referenceLockExec(t, p.ctx, c, "SET lock_timeout='50ms'", 0)
					if q == "DROP INDEX ix" {
						s := referenceImplicitError("SELECT n FROM t", referenceDiagnostic{Code: "55P03", Message: "canceling statement due to lock timeout", Position: "15"})
						s.selectRows = true
						referenceRunStep(t, p.ctx, c, b.recorder, s)
						referenceRunStep(t, p.ctx, c, b.recorder, referenceImplicitError("INSERT INTO t VALUES(2)", referenceDiagnostic{Code: "55P03", Message: "canceling statement due to lock timeout", Position: "13"}))
					}
					if q == "REINDEX INDEX ix" || q == "REINDEX TABLE t" {
						referenceRunStep(t, p.ctx, c, b.recorder, referenceImplicitError("INSERT INTO t VALUES(2)", referenceDiagnostic{Code: "55P03", Message: "canceling statement due to lock timeout", Position: "13"}))
					}
					if q == "ALTER INDEX ix RENAME TO fresh" {
						referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO t VALUES(2)", 1))
					}
					referenceLockExec(t, p.ctx, c, "SET lock_timeout=0", 0)
				} else {
					referenceRunStep(t, p.ctx, a, b.recorder, referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
				}
				referenceLockExec(t, p.ctx, a, "ROLLBACK", 0)
			})
		})
	}
}
