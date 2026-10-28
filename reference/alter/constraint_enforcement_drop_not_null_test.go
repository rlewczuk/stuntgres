package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 executes DROP NOT NULL in AT_PASS_DROP and removes the NN catalog
// fact before the unnamed FK chooses its name. A no-op must not remove another
// fact's reservation, and namespace incumbents remain reserved independently.
func TestReferenceConstraintEnforcementDropNotNullNames(t *testing.T) {
	for _, route := range []string{"remove", "bare_add", "nullable_noop", "namespace_incumbent", "domain_incumbent", "noop_with_check", "noop_other_column", "remove_other_column", "invalid_history", "late_failure"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/held_%v", route, prepared), func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := enforcementRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					definition, dropped, generated := "a INT CONSTRAINT t_a_fkey NOT NULL,b INT", "a", "t_a_fkey"
					if route == "noop_with_check" {
						definition, generated = "a INT,b INT,CONSTRAINT t_a_fkey CHECK(a>0) NOT ENFORCED", "t_a_fkey1"
					}
					if route == "nullable_noop" {
						definition = "a INT,b INT"
					}
					if route == "noop_other_column" {
						dropped, generated = "b", "t_a_fkey1"
					}
					if route == "remove_other_column" {
						definition, dropped = "a INT,b INT CONSTRAINT t_a_fkey NOT NULL", "b"
					}
					run(fixture.Exec("CREATE TABLE t("+definition+")", 0))
					var otherOID int64
					if route == "namespace_incumbent" {
						run(fixture.Exec("CREATE TABLE other(v INT,CONSTRAINT t_a_fkey CHECK(v>0) NOT ENFORCED)", 0))
						otherOID = enforcementIdentity(t, p, b, c, "other", "t_a_fkey", "CHECK ((v > 0)) NOT ENFORCED", false, false)
						generated = "t_a_fkey1"
					}
					var domainOID int64
					if route == "domain_incumbent" {
						run(fixture.Exec("CREATE DOMAIN reserved_domain AS INT CONSTRAINT t_a_fkey CHECK(VALUE>0)", 0))
						if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE contypid='reserved_domain'::regtype AND conname='t_a_fkey'").Scan(&domainOID); err != nil {
							t.Fatal(err)
						}
						generated = "t_a_fkey1"
					}
					if route == "late_failure" {
						run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT stop CHECK(a>0) NOT ENFORCED", 0))
					}
					value := 1
					if route == "invalid_history" {
						value = 9
					}
					run(fixture.Exec("INSERT INTO t VALUES($1,1)", 1, value))
					var oldNN int64
					err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conrelid='t'::regclass AND contype='n' AND conname='t_a_fkey'").Scan(&oldNN)
					if route == "noop_with_check" || route == "nullable_noop" {
						if err != sql.ErrNoRows {
							t.Fatal("unexpected NN fact", oldNN, err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
					q := "ALTER TABLE t ALTER COLUMN " + dropped + " DROP NOT NULL,ADD FOREIGN KEY(a) REFERENCES p NOT ENFORCED,ALTER CONSTRAINT " + generated + " ENFORCED,ALTER CONSTRAINT " + generated + " NOT ENFORCED"
					if route == "bare_add" {
						q = "ALTER TABLE t ALTER a DROP NOT NULL,ADD FOREIGN KEY(a) REFERENCES p NOT ENFORCED"
					}
					if route == "nullable_noop" {
						q = "ALTER TABLE t ALTER a DROP NOT NULL," + q[len("ALTER TABLE t "):]
					}
					if route == "late_failure" {
						q += ",VALIDATE CONSTRAINT stop"
					}
					before := enforcementSnapshot(t, p, b, c)
					execute := run
					if prepared {
						held := addConstraintHeld(t, p, b, c, q)
						execute = func(step fixture.Step) { validateConstraintHeldRun(t, p, b, held, step) }
						enforcementUnchanged(t, p, b, c, before)
					}
					if route == "invalid_history" {
						execute(fixture.ErrorDiagnostic(q, addConstraintFK("t", generated, "a", "9", "p")))
						enforcementUnchanged(t, p, b, c, before)
						run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
					}
					if route == "late_failure" {
						execute(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("55000", "cannot validate NOT ENFORCED constraint")))
						enforcementUnchanged(t, p, b, c, before)
						run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT stop,ADD CONSTRAINT stop CHECK(a>0)", 0))
					}
					pid := 0
					if b.PG {
						pid = fixture.LockPID(t, p.Ctx, c)
					}
					peer, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					execute(fixture.Exec(q, 0))
					if b.PG {
						var granted bool
						mode := "AccessExclusiveLock"
						if route == "bare_add" {
							mode = "ShareRowExclusiveLock"
						}
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='p'::regclass AND mode=$2 AND granted)", pid, mode).Scan(&granted); err != nil || !granted {
							t.Fatal("missing removal-transition parent grant", mode, granted, err)
						}
					}
					tx, err := peer.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					probe := fixture.ErrorDiagnostic("LOCK TABLE p IN ACCESS SHARE MODE NOWAIT", fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on relation "p"`))
					if route == "bare_add" {
						probe = fixture.Exec("LOCK TABLE p IN ACCESS SHARE MODE NOWAIT", 0)
					}
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, probe)
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					oid := enforcementIdentity(t, p, b, c, "t", generated, "FOREIGN KEY (a) REFERENCES p(id) NOT ENFORCED", false, false)
					if oid <= 0 || oid == oldNN {
						t.Fatal("new FK did not receive a fresh positive identity", oldNN, oid)
					}
					run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass AND contype='f'", []string{"conname"}, []string{"NAME"}, [][]any{{generated}}))
					enforcementTriggers(t, p, b, c, oid, false)
					var expectedNN [][]any
					if route == "noop_other_column" {
						expectedNN = [][]any{{true}}
					}
					run(fixture.Select("SELECT oid=$1::oid AS same FROM pg_constraint WHERE conrelid='t'::regclass AND contype='n'", []string{"same"}, []string{"BOOL"}, expectedNN, oldNN))
					run(fixture.Select("SELECT attname,attnotnull FROM pg_attribute WHERE attrelid='t'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attnotnull"}, []string{"NAME", "BOOL"}, [][]any{{"a", route == "noop_other_column"}, {"b", false}}))
					if route == "namespace_incumbent" && enforcementIdentity(t, p, b, c, "other", "t_a_fkey", "CHECK ((v > 0)) NOT ENFORCED", false, false) != otherOID {
						t.Fatal("removal changed other relation's reservation")
					}
					if route == "noop_with_check" {
						run(enforcementFlags("t", "t_a_fkey", false, false))
					}
					if route == "domain_incumbent" {
						run(fixture.Select("SELECT oid=$1::oid AS same,conenforced,convalidated,pg_get_constraintdef(oid) FROM pg_constraint WHERE contypid='reserved_domain'::regtype AND conname='t_a_fkey'", []string{"same", "conenforced", "convalidated", "pg_get_constraintdef"}, []string{"BOOL", "BOOL", "BOOL", "TEXT"}, [][]any{{true, true, true, "CHECK ((VALUE > 0))"}}, domainOID))
					}
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Exec("INSERT INTO t VALUES(99,1)", 1))
					if route != "noop_other_column" {
						run(fixture.Exec("INSERT INTO t VALUES(NULL,NULL)", 1))
					}
					rows := [][]any{{int64(value), int64(1)}, {int64(99), int64(1)}}
					if route != "noop_other_column" {
						rows = append([][]any{{nil, nil}}, rows...)
					}
					run(fixture.Select("SELECT a,b FROM t ORDER BY a NULLS FIRST", []string{"a", "b"}, []string{"INT4", "INT4"}, rows))
					enforcementReleasedLocks(t, p, b, c, true)
				})
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			t.Run(fmt.Sprintf("blocked/held_%v/canceled_%v", prepared, canceled), func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := enforcementRun(t, p, b, c)
					for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE t(a INT CONSTRAINT t_a_fkey NOT NULL,b INT)"} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					run(fixture.Exec("INSERT INTO t VALUES(1,1)", 1))
					q := "ALTER TABLE t ALTER a DROP NOT NULL,ADD FOREIGN KEY(a) REFERENCES p NOT ENFORCED,ALTER CONSTRAINT t_a_fkey ENFORCED,ALTER CONSTRAINT t_a_fkey NOT ENFORCED"
					execute := func(ctx context.Context) (sql.Result, error) { return c.ExecContext(ctx, q) }
					if prepared {
						held := addConstraintHeld(t, p, b, c, q)
						execute = func(ctx context.Context) (sql.Result, error) { return held.ExecContext(ctx, held.SQL) }
					}
					holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
					before := enforcementSnapshot(t, p, b, observer)
					waiterPID, holderPID := 0, 0
					var parentOID int64
					if b.PG {
						waiterPID, holderPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, holder)
						parentOID = fixture.LockOID(t, p.Ctx, "p")
					}
					tx, err := holder.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE p IN ACCESS SHARE MODE", 0))
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					finished := make(chan struct{})
					await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						result, err := execute(ctx)
						close(finished)
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					fixture.RelationLockPending(t, p, b, waiterPID, holderPID, parentOID, "AccessExclusiveLock", finished)
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT conname,contype,conenforced,convalidated FROM pg_constraint WHERE conrelid='t'::regclass", []string{"conname", "contype", "conenforced", "convalidated"}, []string{"NAME", "CHAR", "BOOL", "BOOL"}, [][]any{{"t_a_fkey", "n", true, true}}))
					if canceled {
						if b.PG {
							var ok bool
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", waiterPID).Scan(&ok); err != nil || !ok {
								t.Fatal(ok, err)
							}
						} else {
							cancel()
						}
					} else if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					out := await()
					if canceled {
						if out.Result != nil {
							t.Fatal("canceled removal published a result")
						}
						if b.PG {
							fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
						} else if !errors.Is(out.Err, context.Canceled) {
							t.Fatal(out.Err)
						}
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
						enforcementUnchanged(t, p, b, observer, before)
						result, err := execute(p.Ctx)
						if err != nil || result == nil {
							t.Fatal(result, err)
						}
						if n, err := result.RowsAffected(); err != nil || n != 0 {
							t.Fatal(n, err)
						}
					} else {
						if out.Err != nil || out.Result == nil {
							t.Fatal(out.Result, out.Err)
						}
						if n, err := out.Result.RowsAffected(); err != nil || n != 0 {
							t.Fatal(n, err)
						}
					}
					oid := enforcementIdentity(t, p, b, observer, "t", "t_a_fkey", "FOREIGN KEY (a) REFERENCES p(id) NOT ENFORCED", false, false)
					enforcementTriggers(t, p, b, observer, oid, false)
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass AND contype='n'", []string{"conname"}, []string{"NAME"}, nil))
					run(fixture.Exec("INSERT INTO t VALUES(NULL,NULL)", 1))
					enforcementReleasedLocks(t, p, b, observer, true)
				})
			})
		}
	}
}
