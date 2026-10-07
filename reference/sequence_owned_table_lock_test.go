//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"testing"
	"time"
)

// Inspect the exact relation and actual blocking PID, not query text, a mock
// queue, or the duration for which a goroutine appears not to finish.
func ownedTableReferenceQueue(t *testing.T, ctx context.Context, waiter, holder int, oid int64, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		err := referenceAdmin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND relation=$3::oid AND mode=$4 AND NOT granted) AND $2 = ANY(pg_blocking_pids($1))`, waiter, holder, oid, mode).Scan(&waiting)
		if err != nil {
			t.Fatal("ownership queue observation", err)
		}
		if waiting {
			return
		}
		runtime.Gosched()
	}
}

func ownedTableReferenceOID(t *testing.T, ctx context.Context, c *sql.Conn, name string) int64 {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(ctx, "SELECT '"+name+"'::regclass::oid").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	return oid
}

func ownedTableReferenceMissing(t *testing.T, ctx context.Context, c *sql.Conn, name string) {
	t.Helper()
	var n int64
	err := c.QueryRowContext(ctx, "SELECT last_value FROM "+name).Scan(&n)
	want := controlsReferenceDiagnostic("42P01", `relation "`+name+`" does not exist`)
	want.Position = "24"
	referenceAssertDiagnostic(t, err, &want)
}

func ownedTableReferenceExec(t *testing.T, ctx context.Context, c *sql.Conn, q string, prepared bool) func(context.Context) relationOutcome {
	t.Helper()
	if !prepared {
		return func(ctx context.Context) relationOutcome {
			r, e := c.ExecContext(ctx, q)
			return relationOutcome{result: r, err: e}
		}
	}
	s, err := c.PrepareContext(ctx, q)
	if err != nil {
		t.Fatal("analysis-only utility Prepare", err)
	}
	t.Cleanup(func() { s.Close() })
	return func(ctx context.Context) relationOutcome {
		r, e := s.ExecContext(ctx)
		return relationOutcome{result: r, err: e}
	}
}

func TestReferenceSequenceOwnedTableLock(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, alter := range []bool{false, true} {
			for _, failure := range []string{"Release", "Cancel", "LockTimeout"} {
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Create", true: "/Alter"}[alter]+"/"+failure, func(t *testing.T) {
					p := newReferencePair(t)
					// Native has independent positive timeout/public retention
					// witnesses in root tests. Here cancellation follows a real PG
					// queue observation, which has no native SQL equivalent.
					a, b := referenceLockConnections(t, p.ctx, p.postgres)
					referenceLockExec(t, p.ctx, a, "CREATE TABLE t(c INT)", 0)
					q := "CREATE SEQUENCE s START 17 OWNED BY t.c"
					if alter {
						referenceLockExec(t, p.ctx, b, "CREATE SEQUENCE s START 17", 0)
						q = "ALTER SEQUENCE s OWNED BY t.c"
					}
					execute := ownedTableReferenceExec(t, p.ctx, b, q, prepared)
					ap, bp := referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, b)
					oid := ownedTableReferenceOID(t, p.ctx, a, "t")
					holder, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					referenceLockExec(t, p.ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					if failure == "LockTimeout" {
						referenceLockExec(t, p.ctx, b, "SET lock_timeout='100ms'", 0)
					}
					waitCtx, cancel := context.WithCancel(p.ctx)
					defer cancel()
					await := referenceLockOperation(t, waitCtx, execute)
					ownedTableReferenceQueue(t, p.ctx, bp, ap, oid, "AccessShareLock")
					switch failure {
					case "Cancel":
						// Server-side cancellation retains the session for recovery.
						var canceled bool
						if err = referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
							t.Fatal(canceled, err)
						}
					case "Release":
						if err = holder.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
					o := await()
					if failure == "Release" {
						if o.err != nil {
							t.Fatal(o.err)
						}
						referenceLockAffected(t, o.result, 0)
						referenceLockExec(t, p.ctx, a, "ALTER TABLE t DROP COLUMN c", 0)
						ownedTableReferenceMissing(t, p.ctx, b, "s")
						return
					}
					if o.result != nil {
						t.Fatal("failed ownership returned result")
					}
					want := controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
					if failure == "Cancel" {
						want = controlsReferenceDiagnostic("57014", "canceling statement due to user request")
					}
					referenceAssertDiagnostic(t, o.err, &want)
					if err = holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					referenceLockExec(t, p.ctx, b, "RESET lock_timeout", 0)
					referenceLockExec(t, p.ctx, a, "DROP TABLE t", 0)
					if alter {
						sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 17)
					} else {
						ownedTableReferenceMissing(t, p.ctx, b, "s")
					}
					referenceLockExec(t, p.ctx, a, "CREATE TABLE t(c INT)", 0)
					o = execute(p.ctx)
					if o.err != nil {
						t.Fatal("same retained utility recovery", o.err)
					}
					referenceLockAffected(t, o.result, 0)
					referenceLockExec(t, p.ctx, a, "DROP TABLE t", 0)
					ownedTableReferenceMissing(t, p.ctx, b, "s")
				})
			}
		}
	}
}

func TestReferenceSequenceOwnedTableLockRetention(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, alter := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Create", true: "/Alter"}[alter]+map[bool]string{false: "/Rollback", true: "/Commit"}[commit], func(t *testing.T) {
					p := newReferencePair(t)
					a, b := referenceLockConnections(t, p.ctx, p.postgres)
					referenceLockExec(t, p.ctx, a, "CREATE TABLE t(c INT)", 0)
					q := "CREATE SEQUENCE s OWNED BY t.c"
					if alter {
						referenceLockExec(t, p.ctx, a, "CREATE SEQUENCE s", 0)
						q = "ALTER SEQUENCE s OWNED BY t.c"
					}
					ap, bp := referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, b)
					oid := ownedTableReferenceOID(t, p.ctx, a, "t")
					tx, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { tx.Rollback() })
					// Close the prepared utility before inspecting the retained lock.
					referenceRunStep(t, p.ctx, tx, p.postgresRecord, referenceStep{query: q, prepared: prepared, affected: 0})
					await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
						r, e := b.ExecContext(ctx, "ALTER TABLE t DROP COLUMN c")
						return relationOutcome{result: r, err: e}
					})
					ownedTableReferenceQueue(t, p.ctx, bp, ap, oid, "AccessExclusiveLock")
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					o := await()
					if o.err != nil {
						t.Fatal(o.err)
					}
					referenceLockAffected(t, o.result, 0)
					if alter && !commit {
						sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 1)
					} else {
						ownedTableReferenceMissing(t, p.ctx, b, "s")
					}
				})
			}
		}
	}
}

func TestReferenceSequenceOwnedTableLockDropClosure(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, alter := range []bool{false, true} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Create", true: "/Alter"}[alter], func(t *testing.T) {
				p := newReferencePair(t)
				a, b := referenceLockConnections(t, p.ctx, p.postgres)
				c, err := p.postgres.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { c.Close() })
				referenceLockExec(t, p.ctx, a, "CREATE TABLE t(c INT, survivor INT)", 0)
				referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(7,9)", 1)
				referenceLockExec(t, p.ctx, a, "CREATE SEQUENCE blocker OWNED BY t.c", 0)
				q := "CREATE SEQUENCE late START 31 OWNED BY t.c"
				if alter {
					referenceLockExec(t, p.ctx, c, "CREATE SEQUENCE late START 31", 0)
					q = "ALTER SEQUENCE late OWNED BY t.c"
				}
				execute := ownedTableReferenceExec(t, p.ctx, c, q, prepared)
				ap, bp, cp := referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, b), referenceLockPID(t, p.ctx, c)
				tableOID, sequenceOID := ownedTableReferenceOID(t, p.ctx, a, "t"), ownedTableReferenceOID(t, p.ctx, a, "blocker")
				holder, err := b.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { holder.Rollback() })
				var n int64
				if err = holder.QueryRowContext(p.ctx, "SELECT nextval('blocker')").Scan(&n); err != nil || n != 1 {
					t.Fatal(n, err)
				}
				drop := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
					r, e := a.ExecContext(ctx, "ALTER TABLE t DROP COLUMN c")
					return relationOutcome{result: r, err: e}
				})
				ownedTableReferenceQueue(t, p.ctx, ap, bp, sequenceOID, "AccessExclusiveLock")
				late := referenceLockOperation(t, p.ctx, execute)
				ownedTableReferenceQueue(t, p.ctx, cp, ap, tableOID, "AccessShareLock")
				if err = holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				o := drop()
				if o.err != nil {
					t.Fatal(o.err)
				}
				referenceLockAffected(t, o.result, 0)
				o = late()
				if o.result != nil {
					t.Fatal("missing-column failure returned result")
				}
				want := controlsReferenceDiagnostic("42703", `column "c" of relation "t" does not exist`)
				referenceAssertDiagnostic(t, o.err, &want)
				ownedTableReferenceMissing(t, p.ctx, c, "blocker")
				rows, err := c.QueryContext(p.ctx, "SELECT survivor FROM t")
				if err != nil {
					t.Fatal(err)
				}
				transactionRowValues(t, rows, 9)
				rows.Close()
				var dependencies int
				if err = c.QueryRowContext(p.ctx, `SELECT count(*) FROM pg_depend d JOIN pg_class s ON s.oid=d.objid WHERE d.classid='pg_class'::regclass AND d.refclassid='pg_class'::regclass AND d.refobjid=$1::oid AND d.deptype='a' AND s.relkind='S'`, tableOID).Scan(&dependencies); err != nil || dependencies != 0 {
					t.Fatal("dangling AUTO dependency", dependencies, err)
				}
				if alter {
					sequenceProbeValue(t, p.ctx, c, "SELECT nextval('late')", 31)
				} else {
					ownedTableReferenceMissing(t, p.ctx, c, "late")
				}
				// The failed retained handle must also recover on a new valid table.
				referenceLockExec(t, p.ctx, a, "DROP TABLE t", 0)
				referenceLockExec(t, p.ctx, a, "CREATE TABLE t(c INT)", 0)
				o = execute(p.ctx)
				if o.err != nil {
					t.Fatal(o.err)
				}
				referenceLockAffected(t, o.result, 0)
				referenceLockExec(t, p.ctx, a, "DROP TABLE t", 0)
				ownedTableReferenceMissing(t, p.ctx, c, "late")
			})
		}
	}
}

// Public native outcomes have their own expected state ledger, not a comparison
// with the oracle's result. Internal tests own native manager admission signals.
func TestReferenceSequenceOwnedTableLockNative(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, alter := range []bool{false, true} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Create", true: "/Alter"}[alter], func(t *testing.T) {
				p := newReferencePair(t)
				a, b := referenceLockConnections(t, p.ctx, p.memory)
				referenceLockExec(t, p.ctx, a, "CREATE TABLE t(c INT, survivor INT)", 0)
				referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(7,9)", 1)
				q := "CREATE SEQUENCE late START 31 OWNED BY t.c"
				if alter {
					referenceLockExec(t, p.ctx, b, "CREATE SEQUENCE late START 31", 0)
					q = "ALTER SEQUENCE late OWNED BY t.c"
				}
				execute := ownedTableReferenceExec(t, p.ctx, b, q, prepared)
				holder, err := a.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				referenceLockExec(t, p.ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
				referenceLockExec(t, p.ctx, b, "SET lock_timeout='100ms'", 0)
				o := execute(p.ctx)
				want := controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
				referenceAssertDiagnostic(t, o.err, &want)
				if o.result != nil {
					t.Fatal("failed native ownership returned result")
				}
				if err = holder.Commit(); err != nil {
					t.Fatal(err)
				}
				referenceLockExec(t, p.ctx, b, "RESET lock_timeout", 0)
				// Deferred at user request. Uncomment this native DROP COLUMN/
				// missing-column retry case when that capability is enabled
				// for this coverage.
				// referenceLockExec(t, p.ctx, a, "ALTER TABLE t DROP COLUMN c", 0)
				// o = execute(p.ctx)
				// want = controlsReferenceDiagnostic("42703", `column "c" of relation "t" does not exist`)
				// referenceAssertDiagnostic(t, o.err, &want)
				// if o.result != nil {
				// 	t.Fatal("missing native column returned result")
				// }
				// rows, err := b.QueryContext(p.ctx, "SELECT survivor FROM t")
				// if err != nil {
				// 	t.Fatal(err)
				// }
				// transactionRowValues(t, rows, 9)
				// rows.Close()
				referenceLockExec(t, p.ctx, a, "DROP TABLE t", 0)
				if alter {
					sequenceProbeValue(t, p.ctx, b, "SELECT nextval('late')", 31)
				} else {
					ownedTableReferenceMissing(t, p.ctx, b, "late")
				}
				referenceLockExec(t, p.ctx, a, "CREATE TABLE t(c INT)", 0)
				o = execute(p.ctx)
				if o.err != nil {
					t.Fatal("healthy native retained execution", o.err)
				}
				referenceLockAffected(t, o.result, 0)
				referenceLockExec(t, p.ctx, a, "DROP TABLE t", 0)
				ownedTableReferenceMissing(t, p.ctx, b, "late")
			})
		}
	}
}
