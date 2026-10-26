package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexMaintenanceLocks(t *testing.T) {
	for _, action := range []string{"rename", "set", "reset", "drop"} {
		t.Run(action, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					run := transactionalIndexRun(t, p, b, a)
					indexTxMaintenanceSeed(run, action)
					ap, cp := 0, 0
					var oid int64
					if b.PG {
						ap = fixture.LockPID(t, p.Ctx, a)
						cp = fixture.LockPID(t, p.Ctx, c)
						oid = fixture.LockOID(t, p.Ctx, "ix")
					}
					run(fixture.Exec("BEGIN", 0))
					defer a.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec(indexTxMaintenanceSQL(action), 0))
					if action != "drop" {
						// SUE on the index does not block ordinary RowExclusive parent DML.
						transactionalIndexRun(t, p, b, c)(fixture.Exec("INSERT INTO t VALUES(3,'peer')", 1))
						q := "ALTER INDEX ix SET(fillfactor=70)"
						// Peer resolves the committed old name, not the private new name.
						done := make(chan struct{})
						await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(done)
							r, err := c.ExecContext(ctx, q)
							return fixture.RelationOutcome{Result: r, Err: err}
						})
						fixture.RelationLockPending(t, p, b, cp, ap, oid, "ShareUpdateExclusiveLock", done)
						run(fixture.Exec("ROLLBACK", 0))
						o := await()
						fixture.DeleteReferenceCount(t, o.Result, o.Err, 0)
						options := "{fillfactor=70}"
						if action == "reset" {
							options = "{deduplicate_items=off,fillfactor=70}"
						}
						transactionalIndexRun(t, p, b, c)(fixture.Select("SELECT reloptions::text FROM pg_class WHERE relname='ix'", []string{"reloptions"}, []string{"TEXT"}, [][]any{{options}}))
					} else {
						if b.PG {
							oid = fixture.LockOID(t, p.Ctx, "t")
						}
						for _, query := range []bool{true, false} {
							done := make(chan struct{})
							await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
								defer close(done)
								if query {
									rows, err := c.QueryContext(ctx, "SELECT n FROM t ORDER BY n NULLS LAST")
									return fixture.RelationOutcome{Rows: rows, Err: err}
								}
								r, err := c.ExecContext(ctx, "INSERT INTO t VALUES(3,'peer')")
								return fixture.RelationOutcome{Result: r, Err: err}
							})
							mode := "RowExclusiveLock"
							if query {
								mode = "AccessShareLock"
							}
							fixture.RelationLockPending(t, p, b, cp, ap, oid, mode, done)
							run(fixture.Exec("ROLLBACK", 0))
							o := await()
							if query {
								fixture.DeleteReferenceConsume(t, b.PG, o.Rows, o.Err, fixture.Select("SELECT n FROM t ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}}))
							} else {
								fixture.DeleteReferenceCount(t, o.Result, o.Err, 1)
							}
							if query {
								run(fixture.Exec("BEGIN", 0))
								run(fixture.Exec("DROP INDEX ix", 0))
							}
						}
					}
					run(fixture.Select("SELECT n FROM t ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {nil}}))
				})
			}
		})
	}
}

func TestReferenceTransactionalIndexMaintenanceSavepointLocks(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, c := fixture.LockConnections(t, p.Ctx, b.DB)
			run := transactionalIndexRun(t, p, b, a)
			for _, q := range []string{"CREATE TABLE early(n INT)", "CREATE INDEX early_ix ON early(n)", "CREATE TABLE later(n INT)", "CREATE INDEX later_ix ON later(n)", "BEGIN", "ALTER INDEX early_ix SET(fillfactor=80)", "SAVEPOINT mark", "DROP INDEX later_ix"} {
				run(fixture.Exec(q, 0))
			}
			defer a.ExecContext(p.Ctx, "ROLLBACK")
			ap, cp := 0, 0
			var early, later int64
			if b.PG {
				ap = fixture.LockPID(t, p.Ctx, a)
				cp = fixture.LockPID(t, p.Ctx, c)
				early = fixture.LockOID(t, p.Ctx, "early_ix")
				later = fixture.LockOID(t, p.Ctx, "later")
			}
			done := make(chan struct{})
			await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(done)
				r, err := c.ExecContext(ctx, "INSERT INTO later VALUES(2)")
				return fixture.RelationOutcome{Result: r, Err: err}
			})
			fixture.RelationLockPending(t, p, b, cp, ap, later, "RowExclusiveLock", done)
			run(fixture.Exec("ROLLBACK TO mark", 0))
			o := await()
			fixture.DeleteReferenceCount(t, o.Result, o.Err, 1)
			done = make(chan struct{})
			await = fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(done)
				r, err := c.ExecContext(ctx, "ALTER INDEX early_ix RESET(fillfactor)")
				return fixture.RelationOutcome{Result: r, Err: err}
			})
			fixture.RelationLockPending(t, p, b, cp, ap, early, "ShareUpdateExclusiveLock", done)
			run(fixture.Exec("ROLLBACK TO mark", 0))
			run(fixture.Exec("RELEASE mark", 0))
			run(fixture.Exec("ROLLBACK", 0))
			o = await()
			fixture.DeleteReferenceCount(t, o.Result, o.Err, 0)
			run(fixture.Select("SELECT n FROM later", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}))
			run(transactionalIndexName("later_ix", true))
		})
	}
}

func TestReferenceTransactionalIndexMaintenanceCancellation(t *testing.T) {
	for _, action := range []string{"rename", "set", "reset", "drop"} {
		for _, kind := range []string{"cancel", "lock_timeout", "statement_timeout"} {
			t.Run(action+"/"+kind, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						holder, owner := fixture.LockConnections(t, p.Ctx, b.DB)
						run := transactionalIndexRun(t, p, b, owner)
						hold := transactionalIndexRun(t, p, b, holder)
						indexTxMaintenanceSeed(run, action)
						q := indexTxMaintenanceSQL(action)
						stmt, err := owner.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						ap, cp := 0, 0
						var oid int64
						mode := "ShareUpdateExclusiveLock"
						if b.PG {
							ap = fixture.LockPID(t, p.Ctx, holder)
							cp = fixture.LockPID(t, p.Ctx, owner)
							oid = fixture.LockOID(t, p.Ctx, "ix")
						}
						hold(fixture.Exec("BEGIN", 0))
						defer holder.ExecContext(p.Ctx, "ROLLBACK")
						if action == "drop" {
							hold(fixture.Exec("LOCK TABLE t IN ROW EXCLUSIVE MODE", 0))
							mode = "AccessExclusiveLock"
							if b.PG {
								oid = fixture.LockOID(t, p.Ctx, "t")
							}
						} else {
							hold(fixture.Exec("ALTER INDEX ix SET(fillfactor=75)", 0))
						}
						run(fixture.Exec("BEGIN", 0))
						defer owner.ExecContext(p.Ctx, "ROLLBACK")
						run(fixture.Exec("SAVEPOINT recover", 0))
						if kind != "cancel" {
							run(fixture.Exec("SET LOCAL "+kind+"='"+timerValue(kind)+"'", 0))
						}
						ctx, cancel := context.WithCancel(p.Ctx)
						defer cancel()
						done := make(chan struct{})
						await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(done)
							r, err := stmt.ExecContext(ctx)
							return fixture.RelationOutcome{Result: r, Err: err}
						})
						if kind == "cancel" {
							fixture.RelationLockPending(t, p, b, cp, ap, oid, mode, done)
							if b.PG {
								var ok bool
								if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", cp).Scan(&ok); err != nil || !ok {
									t.Fatal("cancel admitted owner", ok, err)
								}
							} else {
								cancel()
							}
						}
						o := await()
						if o.Result != nil {
							t.Fatal("failed Execute returned result", o.Result)
						}
						if kind == "cancel" && !b.PG {
							if !errors.Is(o.Err, context.Canceled) {
								t.Fatal(o.Err)
							}
						} else {
							code, message := "57014", "canceling statement due to user request"
							if kind == "lock_timeout" {
								code, message = "55P03", "canceling statement due to lock timeout"
							}
							if kind == "statement_timeout" {
								message = "canceling statement due to statement timeout"
							}
							d := fixture.ControlsReferenceDiagnostic(code, message)
							fixture.AssertDiagnostic(t, o.Err, &d)
						}
						// ROLLBACK TO restores the unarmed mark; never RESET an armed owner.
						run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
						run(fixture.Exec("ROLLBACK TO recover", 0))
						run(indexTxMaintenanceState(action, false))
						hold(fixture.Exec("ROLLBACK", 0))
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: owner, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
						run(fixture.Exec("ROLLBACK", 0))
						run(indexTxMaintenanceState(action, false))
						run(fixture.Exec("INSERT INTO t VALUES(3,'healthy')", 1))
					})
				}
			})
		}
	}
}
