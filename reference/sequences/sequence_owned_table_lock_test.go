package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"stuntgres/reference/fixture"
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
		err := fixture.Admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND relation=$3::oid AND mode=$4 AND NOT granted) AND $2 = ANY(pg_blocking_pids($1))`, waiter, holder, oid, mode).Scan(&waiting)
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
	want := fixture.ControlsReferenceDiagnostic("42P01", `relation "`+name+`" does not exist`)
	want.Position = "24"
	fixture.AssertDiagnostic(t, err, &want)
}

func ownedTableReferenceExec(t *testing.T, ctx context.Context, c *sql.Conn, q string, prepared bool) func(context.Context) fixture.RelationOutcome {
	t.Helper()
	if !prepared {
		return func(ctx context.Context) fixture.RelationOutcome {
			r, e := c.ExecContext(ctx, q)
			return fixture.RelationOutcome{Result: r, Err: e}
		}
	}
	s, err := c.PrepareContext(ctx, q)
	if err != nil {
		t.Fatal("analysis-only utility Prepare", err)
	}
	t.Cleanup(func() { s.Close() })
	return func(ctx context.Context) fixture.RelationOutcome {
		r, e := s.ExecContext(ctx)
		return fixture.RelationOutcome{Result: r, Err: e}
	}
}

func TestReferenceSequenceOwnedTableLock(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, alter := range []bool{false, true} {
			for _, failure := range []string{"Release", "Cancel", "LockTimeout"} {
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Create", true: "/Alter"}[alter]+"/"+failure, func(t *testing.T) {
					p := fixture.NewScenario(t)
					if p.Postgres == nil {
						return
					}
					// Server cancellation retains this actual PG session for held-handle recovery.
					a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(c INT)", 0)
					q := "CREATE SEQUENCE s START 17 OWNED BY t.c"
					if alter {
						fixture.LockExec(t, p.Ctx, b, "CREATE SEQUENCE s START 17", 0)
						q = "ALTER SEQUENCE s OWNED BY t.c"
					}
					execute := ownedTableReferenceExec(t, p.Ctx, b, q, prepared)
					ap, bp := fixture.SequenceOwnedLockPID(t, p.Ctx, a), fixture.SequenceOwnedLockPID(t, p.Ctx, b)
					oid := ownedTableReferenceOID(t, p.Ctx, a, "t")
					holder, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { holder.Rollback() })
					fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					if failure == "LockTimeout" {
						fixture.LockExec(t, p.Ctx, b, "SET lock_timeout='100ms'", 0)
					}
					waitCtx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					await := fixture.LockOperation(t, waitCtx, execute)
					ownedTableReferenceQueue(t, p.Ctx, bp, ap, oid, "AccessShareLock")
					switch failure {
					case "Cancel":
						// Server-side cancellation retains the session for recovery.
						var canceled bool
						if err = fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&canceled); err != nil || !canceled {
							t.Fatal(canceled, err)
						}
					case "Release":
						if err = holder.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
					o := await()
					if failure == "Release" {
						if o.Err != nil {
							t.Fatal(o.Err)
						}
						fixture.LockAffected(t, o.Result, 0)
						fixture.LockExec(t, p.Ctx, a, "ALTER TABLE t DROP COLUMN c", 0)
						ownedTableReferenceMissing(t, p.Ctx, b, "s")
						return
					}
					if o.Result != nil {
						t.Fatal("failed ownership returned result")
					}
					want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
					if failure == "Cancel" {
						want = fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
					}
					fixture.AssertDiagnostic(t, o.Err, &want)
					if err = holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					fixture.LockExec(t, p.Ctx, b, "RESET lock_timeout", 0)
					fixture.LockExec(t, p.Ctx, a, "DROP TABLE t", 0)
					if alter {
						fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 17)
					} else {
						ownedTableReferenceMissing(t, p.Ctx, b, "s")
					}
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(other INT)", 0)
					o = execute(p.Ctx)
					if o.Result != nil {
						t.Fatal("missing-column retry exposed a result")
					}
					want = fixture.ControlsReferenceDiagnostic("42703", `column "c" of relation "t" does not exist`)
					fixture.AssertDiagnostic(t, o.Err, &want)
					fixture.LockExec(t, p.Ctx, a, "DROP TABLE t", 0)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(c INT)", 0)
					o = execute(p.Ctx)
					if o.Err != nil {
						t.Fatal("same retained utility recovery", o.Err)
					}
					fixture.LockAffected(t, o.Result, 0)
					fixture.LockExec(t, p.Ctx, a, "DROP TABLE t", 0)
					ownedTableReferenceMissing(t, p.Ctx, b, "s")
				})
			}
		}
	}
}

func TestReferenceSequenceOwnedTableLockRetentionOracle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, alter := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Create", true: "/Alter"}[alter]+map[bool]string{false: "/Rollback", true: "/Commit"}[commit], func(t *testing.T) {
					p := fixture.NewScenario(t)
					if p.Postgres == nil {
						return
					}
					a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(c INT)", 0)
					q := "CREATE SEQUENCE s OWNED BY t.c"
					if alter {
						fixture.LockExec(t, p.Ctx, a, "CREATE SEQUENCE s", 0)
						q = "ALTER SEQUENCE s OWNED BY t.c"
					}
					ap, bp := fixture.SequenceOwnedLockPID(t, p.Ctx, a), fixture.SequenceOwnedLockPID(t, p.Ctx, b)
					oid := ownedTableReferenceOID(t, p.Ctx, a, "t")
					tx, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { tx.Rollback() })
					// Close the prepared utility before inspecting the retained lock.
					fixture.RunStep(t, p.Ctx, tx, p.PostgresRecord, fixture.Step{Query: q, Prepared: prepared, Affected: 0})
					await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						r, e := b.ExecContext(ctx, "ALTER TABLE t DROP COLUMN c")
						return fixture.RelationOutcome{Result: r, Err: e}
					})
					ownedTableReferenceQueue(t, p.Ctx, bp, ap, oid, "AccessExclusiveLock")
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					o := await()
					if o.Err != nil {
						t.Fatal(o.Err)
					}
					fixture.LockAffected(t, o.Result, 0)
					if alter && !commit {
						fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 1)
					} else {
						ownedTableReferenceMissing(t, p.Ctx, b, "s")
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
				p := fixture.NewScenario(t)
				if p.Postgres == nil {
					return
				}
				a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
				c, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { c.Close() })
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(c INT, survivor INT)", 0)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(7,9)", 1)
				fixture.LockExec(t, p.Ctx, a, "CREATE SEQUENCE blocker OWNED BY t.c", 0)
				q := "CREATE SEQUENCE late START 31 OWNED BY t.c"
				if alter {
					fixture.LockExec(t, p.Ctx, c, "CREATE SEQUENCE late START 31", 0)
					q = "ALTER SEQUENCE late OWNED BY t.c"
				}
				execute := ownedTableReferenceExec(t, p.Ctx, c, q, prepared)
				ap, bp, cp := fixture.SequenceOwnedLockPID(t, p.Ctx, a), fixture.SequenceOwnedLockPID(t, p.Ctx, b), fixture.SequenceOwnedLockPID(t, p.Ctx, c)
				tableOID, sequenceOID := ownedTableReferenceOID(t, p.Ctx, a, "t"), ownedTableReferenceOID(t, p.Ctx, a, "blocker")
				holder, err := b.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { holder.Rollback() })
				var n int64
				if err = holder.QueryRowContext(p.Ctx, "SELECT nextval('blocker')").Scan(&n); err != nil || n != 1 {
					t.Fatal(n, err)
				}
				drop := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					r, e := a.ExecContext(ctx, "ALTER TABLE t DROP COLUMN c")
					return fixture.RelationOutcome{Result: r, Err: e}
				})
				ownedTableReferenceQueue(t, p.Ctx, ap, bp, sequenceOID, "AccessExclusiveLock")
				late := fixture.LockOperation(t, p.Ctx, execute)
				ownedTableReferenceQueue(t, p.Ctx, cp, ap, tableOID, "AccessShareLock")
				if err = holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				o := drop()
				if o.Err != nil {
					t.Fatal(o.Err)
				}
				fixture.LockAffected(t, o.Result, 0)
				o = late()
				if o.Result != nil {
					t.Fatal("missing-column failure returned result")
				}
				want := fixture.ControlsReferenceDiagnostic("42703", `column "c" of relation "t" does not exist`)
				fixture.AssertDiagnostic(t, o.Err, &want)
				ownedTableReferenceMissing(t, p.Ctx, c, "blocker")
				rows, err := c.QueryContext(p.Ctx, "SELECT survivor FROM t")
				if err != nil {
					t.Fatal(err)
				}
				fixture.TransactionRowValues(t, rows, 9)
				rows.Close()
				var dependencies int
				if err = c.QueryRowContext(p.Ctx, `SELECT count(*) FROM pg_depend d JOIN pg_class s ON s.oid=d.objid WHERE d.classid='pg_class'::regclass AND d.refclassid='pg_class'::regclass AND d.refobjid=$1::oid AND d.deptype='a' AND s.relkind='S'`, tableOID).Scan(&dependencies); err != nil || dependencies != 0 {
					t.Fatal("dangling AUTO dependency", dependencies, err)
				}
				if alter {
					fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('late')", 31)
				} else {
					ownedTableReferenceMissing(t, p.Ctx, c, "late")
				}
				// The failed retained handle must also recover on a new valid table.
				fixture.LockExec(t, p.Ctx, a, "DROP TABLE t", 0)
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(c INT)", 0)
				o = execute(p.Ctx)
				if o.Err != nil {
					t.Fatal(o.Err)
				}
				fixture.LockAffected(t, o.Result, 0)
				fixture.LockExec(t, p.Ctx, a, "DROP TABLE t", 0)
				ownedTableReferenceMissing(t, p.Ctx, c, "late")
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
				p := fixture.NewScenario(t)
				a, b := fixture.LockConnections(t, p.Ctx, p.Memory)
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(c INT, survivor INT)", 0)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(7,9)", 1)
				q := "CREATE SEQUENCE late START 31 OWNED BY t.c"
				if alter {
					fixture.LockExec(t, p.Ctx, b, "CREATE SEQUENCE late START 31", 0)
					q = "ALTER SEQUENCE late OWNED BY t.c"
				}
				execute := ownedTableReferenceExec(t, p.Ctx, b, q, prepared)
				holder, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
				fixture.LockExec(t, p.Ctx, b, "SET lock_timeout='100ms'", 0)
				o := execute(p.Ctx)
				want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
				fixture.AssertDiagnostic(t, o.Err, &want)
				if o.Result != nil {
					t.Fatal("failed native ownership returned result")
				}
				if err = holder.Commit(); err != nil {
					t.Fatal(err)
				}
				fixture.LockExec(t, p.Ctx, b, "RESET lock_timeout", 0)
				fixture.LockExec(t, p.Ctx, a, "ALTER TABLE t DROP COLUMN c", 0)
				o = execute(p.Ctx)
				want = fixture.ControlsReferenceDiagnostic("42703", `column "c" of relation "t" does not exist`)
				fixture.AssertDiagnostic(t, o.Err, &want)
				if o.Result != nil {
					t.Fatal("missing native column returned result")
				}
				fixture.RunStep(t, p.Ctx, b, p.MemoryRecord, fixture.Step{Query: "SELECT survivor FROM t", SelectRows: true, Columns: []string{"survivor"}, Types: []string{"INT4"}, Rows: [][]any{{int64(9)}}})
				fixture.LockExec(t, p.Ctx, a, "DROP TABLE t", 0)
				if alter {
					fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('late')", 31)
				} else {
					ownedTableReferenceMissing(t, p.Ctx, b, "late")
				}
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(c INT)", 0)
				o = execute(p.Ctx)
				if o.Err != nil {
					t.Fatal("healthy native retained execution", o.Err)
				}
				fixture.LockAffected(t, o.Result, 0)
				fixture.LockExec(t, p.Ctx, a, "DROP TABLE t", 0)
				ownedTableReferenceMissing(t, p.Ctx, b, "late")
			})
		}
	}
}
