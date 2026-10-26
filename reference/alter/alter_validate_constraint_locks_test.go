package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterValidateConstraintLocks(t *testing.T) {
	t.Run("already_valid_fk_no_parent_relock", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID", 0))
			held := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT fk")
			validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
			holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			tx, err := holder.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE p IN EXCLUSIVE MODE", 0))
			run(fixture.Exec("SET lock_timeout='10ms'", 0))
			validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
			if err := held.Statement.Close(); err != nil {
				t.Fatal(err)
			}
			if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal("discard armed no-op connection", err)
			}
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, validateConstraintFlag("t", "fk", true))
		})
	})
	for _, tc := range []struct{ name, extra, child, parent string }{
		{"check", "", "ShareUpdateExclusiveLock", ""},
		{"foreign_key", "", "ShareUpdateExclusiveLock", "RowShareLock"},
		{"mixed_fk", ", ADD CONSTRAINT more FOREIGN KEY(a) REFERENCES p NOT VALID", "ShareRowExclusiveLock", "ShareRowExclusiveLock"},
		{"mixed_shape", ", ADD COLUMN more INT", "AccessExclusiveLock", ""},
		{"self_fk", "", "ShareUpdateExclusiveLock", ""},
		{"descendant", "", "ShareUpdateExclusiveLock", ""},
	} {
		t.Run("grants/"+tc.name, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES(1,'one')", 1))
				name, clause := "ck", "CONSTRAINT ck CHECK(a>0) NOT VALID"
				if tc.name == "foreign_key" || tc.name == "mixed_fk" {
					name, clause = "fk", "CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID"
				}
				if tc.name == "self_fk" {
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT uq UNIQUE(a)", 0))
					name, clause = "fk", "CONSTRAINT fk FOREIGN KEY(a) REFERENCES t(a) NOT VALID"
				}
				if tc.name == "descendant" {
					run(fixture.Exec("CREATE TABLE child() INHERITS(t)", 0))
				}
				run(fixture.Exec("ALTER TABLE t ADD "+clause, 0))
				held := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT "+name+tc.extra)
				peer, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				pid := 0
				if b.PG {
					pid = fixture.LockPID(t, p.Ctx, c)
				}
				run(fixture.Exec("BEGIN", 0))
				validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
				if b.PG {
					for table, mode := range map[string]string{"t": tc.child, "p": tc.parent} {
						if mode == "" {
							continue
						}
						var granted bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation=$2::regclass AND mode=$3 AND granted)", pid, table, mode).Scan(&granted); err != nil || !granted {
							t.Fatal("missing exact validation grant", table, mode, granted, err)
						}
					}
				}
				probe := func(table, mode string, success bool) {
					tx, err := peer.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					q := "LOCK TABLE ONLY " + table + " IN " + mode + " MODE NOWAIT"
					if success {
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(q, 0))
					} else {
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on relation "`+table+`"`)))
					}
				}
				probe("t", "ACCESS SHARE", tc.child != "AccessExclusiveLock")
				probe("t", "ROW EXCLUSIVE", tc.child == "ShareUpdateExclusiveLock")
				probe("t", "SHARE", false)
				if tc.parent != "" {
					probe("p", "SHARE", tc.parent == "RowShareLock")
					probe("p", "ROW EXCLUSIVE", tc.parent == "RowShareLock")
					probe("p", "EXCLUSIVE", false)
				}
				if tc.name == "descendant" {
					probe("child", "ROW EXCLUSIVE", true)
					probe("child", "SHARE", false)
				}
				// Compatible DML is enforced while the validation transaction retains SUE.
				if tc.child == "ShareUpdateExclusiveLock" {
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("INSERT INTO t VALUES(2,'concurrent')", 1))
					if tc.name == "check" || tc.name == "descendant" {
						d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "t" violates check constraint "ck"`)
						d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (0, bad).", "public", "t", "ck"
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.ErrorDiagnostic("INSERT INTO t VALUES(0,'bad')", d))
					}
					if tc.name == "foreign_key" {
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.ErrorDiagnostic("INSERT INTO t VALUES(9,'bad')", addConstraintFK("t", "fk", "a", "9", "p")))
					}
				}
				run(fixture.Exec("COMMIT", 0))
				probe("t", "ACCESS EXCLUSIVE", true)
				run(validateConstraintFlag("t", name, true))
				if tc.child == "ShareUpdateExclusiveLock" {
					run(addConstraintRows([]any{int64(1), "one"}, []any{int64(2), "concurrent"}))
				} else {
					run(addConstraintRows([]any{int64(1), "one"}))
				}
			})
		})
	}
	for _, target := range []string{"child", "parent", "descendant"} {
		for _, cancelWorker := range []bool{false, true} {
			t.Run("queue/"+target+map[bool]string{false: "/release", true: "/cancel"}[cancelWorker], func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := addConstraintSetup(t, p, b, c)
					run(fixture.Exec("INSERT INTO t VALUES(1,'one')", 1))
					name, clause, table, holderMode, waitMode := "ck", "CONSTRAINT ck CHECK(a>0) NOT VALID", "t", "SHARE", "ShareUpdateExclusiveLock"
					if target == "parent" {
						name, clause, table, holderMode, waitMode = "fk", "CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID", "p", "EXCLUSIVE", "RowShareLock"
					}
					if target == "descendant" {
						run(fixture.Exec("CREATE TABLE child() INHERITS(t)", 0))
						table = "child"
					}
					run(fixture.Exec("ALTER TABLE t ADD "+clause, 0))
					held := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT "+name)
					holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
					waiterPID, holderPID := 0, 0
					var oid int64
					if b.PG {
						waiterPID = fixture.LockPID(t, p.Ctx, c)
						holderPID = fixture.LockPID(t, p.Ctx, holder)
						oid = fixture.LockOID(t, p.Ctx, table)
					}
					before := validateConstraintSnapshot(t, p, observer)
					tx, err := holder.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE "+table+" IN "+holderMode+" MODE", 0))
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					finished := make(chan struct{})
					await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						result, err := held.ExecContext(ctx, held.SQL)
						close(finished)
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, waitMode, finished)
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, validateConstraintFlag("t", name, false))
					if !reflect.DeepEqual(before, validateConstraintSnapshot(t, p, observer)) {
						t.Fatal("blocked validation exposed private promotions")
					}
					if cancelWorker {
						if b.PG {
							var ok bool
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", waiterPID).Scan(&ok); err != nil || !ok {
								t.Fatal(ok, err)
							}
						} else {
							cancel()
						}
					} else {
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
					}
					out := await()
					if cancelWorker {
						if out.Result != nil {
							t.Fatal("canceled validation returned result")
						}
						if b.PG {
							fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
						} else if !errors.Is(out.Err, context.Canceled) {
							t.Fatal(out.Err)
						}
						fixture.RunStep(t, p.Ctx, observer, b.Recorder, validateConstraintFlag("t", name, false))
						if !reflect.DeepEqual(before, validateConstraintSnapshot(t, p, observer)) {
							t.Fatal("canceled validation changed any selected validity/origin")
						}
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
						validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
					} else {
						if out.Err != nil {
							t.Fatal(out.Err)
						}
						if out.Result == nil {
							t.Fatal("released validation has no result")
						}
						n, err := out.Result.RowsAffected()
						if err != nil || n != 0 {
							t.Fatal(n, err)
						}
					}
					run(validateConstraintFlag("t", name, true))
					run(fixture.Exec("ALTER TABLE t VALIDATE CONSTRAINT "+name, 0))
				})
			})
		}
	}
	for _, timeout := range []string{"lock_timeout", "statement_timeout"} {
		t.Run(timeout, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT VALID", 0))
				holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
				held := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT ck")
				tx, err := holder.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE t IN SHARE MODE", 0))
				value, code, message := "'10ms'", "55P03", "canceling statement due to lock timeout"
				if timeout == "statement_timeout" {
					value, code, message = "'100ms'", "57014", "canceling statement due to statement timeout"
				}
				run(fixture.Exec("SET "+timeout+"="+value, 0))
				validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, fixture.ControlsReferenceDiagnostic(code, message)))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				// Dispose of the armed session; never RESET or reuse it under the timer.
				if err := held.Statement.Close(); err != nil {
					t.Fatal(err)
				}
				if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
					t.Fatal("discard armed connection", err)
				}
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, validateConstraintFlag("t", "ck", false))
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Exec("ALTER TABLE t VALIDATE CONSTRAINT ck", 0))
			})
		})
	}
	t.Run("active_scan_cancel", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("INSERT INTO t VALUES(1,'one')", 1))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT sleeping CHECK(pg_sleep(60) IS NULL) NOT VALID", 0))
			held := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT sleeping")
			_, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.SleepActiveCancel(t, p, b, c, held.SQL, func(ctx context.Context) {
				fixture.RunStep(t, ctx, observer, b.Recorder, validateConstraintFlag("t", "sleeping", false))
			}, func(ctx context.Context) fixture.RelationOutcome {
				r, err := held.ExecContext(ctx, held.SQL)
				return fixture.RelationOutcome{Result: r, Err: err}
			})
			run(validateConstraintFlag("t", "sleeping", false))
			run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT sleeping, ADD CONSTRAINT sleeping CHECK(a>0) NOT VALID", 0))
			validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
			run(addConstraintRows([]any{int64(1), "one"}))
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				r, err := held.ExecContext(ctx, held.SQL)
				if r != nil || !errors.Is(err, context.Canceled) {
					t.Fatal(r, err)
				}
			}
		})
	})
}
