package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConstraintEnforcementCancellationLocks(t *testing.T) {
	// REL_18_6 trigger.c:211/1325 grants SRE on creation, AX on deletion;
	// tablecmds.c queues a RowShare history scan only on genuine enable.
	// Parent modes are source-derived expectations, awaiting pinned observation.
	for _, tc := range []struct {
		name, initial, actions, child, parent string
		final                                 bool
	}{
		{"enable", "NOT ENFORCED", "ALTER CONSTRAINT fk ENFORCED", "AccessExclusiveLock", "ShareRowExclusiveLock", true},
		{"disable", "ENFORCED", "ALTER CONSTRAINT fk NOT ENFORCED", "AccessExclusiveLock", "AccessExclusiveLock", false},
		{"noop_true", "ENFORCED", "ALTER CONSTRAINT fk ENFORCED", "AccessExclusiveLock", "", true},
		{"noop_false", "NOT ENFORCED", "ALTER CONSTRAINT fk NOT ENFORCED", "AccessExclusiveLock", "", false},
		{"add_false", "", "ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED", "ShareRowExclusiveLock", "ShareRowExclusiveLock", false},
		{"mixed_add", "NOT ENFORCED", "ALTER CONSTRAINT fk ENFORCED, ADD CONSTRAINT more FOREIGN KEY(a) REFERENCES p NOT ENFORCED", "AccessExclusiveLock", "ShareRowExclusiveLock", true},
		{"self", "NOT ENFORCED", "ALTER CONSTRAINT fk ENFORCED", "AccessExclusiveLock", "", true},
	} {
		t.Run("grants/"+tc.name, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				parent := "p(id)"
				if tc.name == "self" {
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT uq UNIQUE(a)", 0))
					parent = "t(a)"
				}
				if tc.initial != "" {
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES "+parent+" "+tc.initial, 0))
				}
				held := addConstraintHeld(t, p, b, c, "ALTER TABLE t "+tc.actions)
				peer, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				pid := 0
				if b.PG {
					pid = fixture.LockPID(t, p.Ctx, c)
				}
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
				if b.PG {
					for table, mode := range map[string]string{"t": tc.child, "p": tc.parent} {
						if mode == "" {
							var n int
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND relation=$2::regclass AND granted", pid, table).Scan(&n); err != nil || n != 0 {
								t.Fatal("unexpected parent relock", table, n, err)
							}
						} else {
							var granted bool
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation=$2::regclass AND mode=$3 AND granted)", pid, table, mode).Scan(&granted); err != nil || !granted {
								t.Fatal("missing exact grant", table, mode, granted, err)
							}
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
					s := fixture.Exec(q, 0)
					if !success {
						s = fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on relation "`+table+`"`))
					}
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
				}
				probe("t", "ACCESS SHARE", tc.child != "AccessExclusiveLock")
				probe("t", "ROW EXCLUSIVE", false)
				probe("p", "ACCESS SHARE", tc.parent != "AccessExclusiveLock")
				probe("p", "ROW EXCLUSIVE", tc.parent == "")
				probe("p", "SHARE", tc.parent == "")
				probe("p", "ACCESS EXCLUSIVE", tc.parent == "")
				run(enforcementFlags("t", "fk", tc.final, tc.final))
				run(fixture.Exec("COMMIT", 0))
				probe("t", "ACCESS EXCLUSIVE", true)
				probe("p", "ACCESS EXCLUSIVE", true)
			})
		})
	}
	for _, target := range []string{"child", "parent_enable", "parent_disable", "noop_child"} {
		for _, canceled := range []bool{false, true} {
			name := "/release"
			if canceled {
				name = "/cancel"
			}
			t.Run("blocked/"+target+name, func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := addConstraintSetup(t, p, b, c)
					initial, requested, table, holderMode, waitMode := "NOT ENFORCED", "ENFORCED", "t", "ACCESS SHARE", "AccessExclusiveLock"
					if target == "parent_enable" {
						table, holderMode, waitMode = "p", "ROW EXCLUSIVE", "ShareRowExclusiveLock"
					}
					if target == "parent_disable" {
						initial, requested, table, holderMode, waitMode = "ENFORCED", "NOT ENFORCED", "p", "ACCESS SHARE", "AccessExclusiveLock"
					}
					if target == "noop_child" {
						initial = "ENFORCED"
					}
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p "+initial, 0))
					held := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT fk "+requested)
					holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
					before := enforcementSnapshot(t, p, b, observer)
					waiterPID, holderPID := 0, 0
					var oid int64
					if b.PG {
						waiterPID = fixture.LockPID(t, p.Ctx, c)
						holderPID = fixture.LockPID(t, p.Ctx, holder)
						oid = fixture.LockOID(t, p.Ctx, table)
					}
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
						r, err := held.ExecContext(ctx, held.SQL)
						close(finished)
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, waitMode, finished)
					// Catalog-only reads remain possible when the pending operation owns AX.
					// Do not read t's rows or Dump here: those legitimately wait behind AX.
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, enforcementFlags("t", "fk", initial == "ENFORCED", initial == "ENFORCED"))
					if canceled {
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
					if canceled {
						if out.Result != nil {
							t.Fatal("canceled ALTER returned result")
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
						enforcementReleasedLocks(t, p, b, observer, true)
						validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
					} else {
						if out.Err != nil || out.Result == nil {
							t.Fatal(out.Result, out.Err)
						}
						if n, err := out.Result.RowsAffected(); err != nil || n != 0 {
							t.Fatal(n, err)
						}
					}
					run(enforcementFlags("t", "fk", requested == "ENFORCED", requested == "ENFORCED"))
				})
			})
		}
	}
	for _, timer := range []string{"lock_timeout", "statement_timeout"} {
		t.Run(timer, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED", 0))
				held := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT fk ENFORCED")
				holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
				before := enforcementSnapshot(t, p, b, observer)
				tx, err := holder.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE t IN ACCESS SHARE MODE", 0))
				value, code, message := "'10ms'", "55P03", "canceling statement due to lock timeout"
				if timer == "statement_timeout" {
					value, code, message = "'100ms'", "57014", "canceling statement due to statement timeout"
				}
				run(fixture.Exec("SET "+timer+"="+value, 0))
				validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, fixture.ControlsReferenceDiagnostic(code, message)))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if err := held.Statement.Close(); err != nil {
					t.Fatal(err)
				}
				if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
					t.Fatal(err)
				}
				enforcementUnchanged(t, p, b, observer, before)
				enforcementReleasedLocks(t, p, b, observer, true)
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Exec(held.SQL, 0))
			})
		})
	}
	t.Run("active_history_scan", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(a INT,CONSTRAINT ck CHECK(pg_sleep(60) IS NULL) NOT ENFORCED)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			held := addConstraintHeld(t, p, b, c, "ALTER TABLE t DROP CONSTRAINT ck, ADD CONSTRAINT ck CHECK(pg_sleep(60) IS NULL) ENFORCED")
			_, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			before := enforcementSnapshot(t, p, b, c)
			fixture.SleepActiveCancel(t, p, b, c, held.SQL, func(ctx context.Context) {
				fixture.RunStep(t, ctx, observer, b.Recorder, enforcementFlags("t", "ck", false, false))
			}, func(ctx context.Context) fixture.RelationOutcome {
				r, err := held.ExecContext(ctx, held.SQL)
				return fixture.RelationOutcome{Result: r, Err: err}
			})
			enforcementUnchanged(t, p, b, c, before)
			enforcementReleasedLocks(t, p, b, observer, false)
			run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT ck, ADD CONSTRAINT ck CHECK(a>0) ENFORCED", 0))
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	})
}

func enforcementReleasedLocks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, parent bool) {
	t.Helper()
	tx, err := c.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
	if parent {
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE p IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
	}
}

// ChooseConstraintName uses namespace-wide collisions and declaration order.
// Trigger deletion requires parent AX even when the ADD name was generated.
func TestReferenceConstraintEnforcementGeneratedNames(t *testing.T) {
	for _, route := range []string{"fresh", "misc_textually_first", "existing_collision", "namespace_collision", "earlier_explicit", "earlier_nn", "drop_reuse", "two_generated", "utf8_collision", "utf8_table_collision"} {
		for _, self := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				label := route + "/distinct/direct"
				if self {
					label = route + "/self/direct"
				}
				if prepared {
					label += "/held"
				}
				t.Run(label, func(t *testing.T) {
					addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := enforcementRun(t, p, b, c)
						column, base, generated := "a", "t_a_fkey", "t_a_fkey"
						if route == "utf8_collision" {
							column = strings.Repeat("é", 28)
							base = "t_" + column + "_fkey"
							generated = "t_" + strings.Repeat("é", 27) + "_fkey1"
						}
						relation, tableSQL := "t", "t"
						if route == "utf8_table_collision" {
							relation = strings.Repeat("é", 28)
							tableSQL = `"` + relation + `"`
							base, generated = relation+"_a_fkey", strings.Repeat("é", 27)+"_a_fkey1"
						}
						parent := "p"
						if self {
							parent = tableSQL
						} else {
							run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
							run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
						}
						run(fixture.Exec("CREATE TABLE "+tableSQL+`(id INT PRIMARY KEY,"`+column+`" INT)`, 0))
						run(fixture.Exec("INSERT INTO "+tableSQL+`(id,"`+column+`") VALUES(1,1)`, 1))
						if route == "existing_collision" || strings.HasPrefix(route, "utf8_") {
							run(fixture.Exec("ALTER TABLE "+tableSQL+` ADD CONSTRAINT "`+base+`" CHECK(false) NOT ENFORCED`, 0))
							if route == "existing_collision" {
								generated = "t_a_fkey1"
							}
						}
						if route == "namespace_collision" {
							run(fixture.Exec("CREATE TABLE other(v INT,CONSTRAINT t_a_fkey CHECK(v>0) NOT ENFORCED)", 0))
							generated = "t_a_fkey1"
						}
						declare := `ADD FOREIGN KEY("` + column + `") REFERENCES ` + parent + "(id) NOT ENFORCED"
						actions := declare
						if route == "drop_reuse" {
							run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT t_a_fkey CHECK(false) NOT ENFORCED", 0))
							actions += ",DROP CONSTRAINT t_a_fkey"
						}
						if route == "earlier_nn" {
							actions += ",ADD CONSTRAINT t_a_fkey NOT NULL a"
							generated = "t_a_fkey1"
						}
						if route == "earlier_explicit" {
							// PG18.6 reports a duplicate during installation, rather
							// than reserving this CHECK name for the unnamed FK.
							actions += ",ADD CONSTRAINT t_a_fkey CHECK(false) NOT ENFORCED"
							generated = "t_a_fkey1"
						}
						names := []string{generated}
						if route == "two_generated" {
							actions += "," + declare
							names = append(names, "t_a_fkey1")
						}
						for _, name := range names {
							actions += `,ALTER CONSTRAINT "` + name + `" ENFORCED,ALTER CONSTRAINT "` + name + `" NOT ENFORCED`
						}
						if route == "misc_textually_first" {
							actions = `ALTER CONSTRAINT "` + generated + `" ENFORCED,ALTER CONSTRAINT "` + generated + `" NOT ENFORCED,` + declare
						}
						q := "ALTER TABLE " + tableSQL + " " + actions
						if route == "earlier_explicit" {
							before := enforcementSnapshot(t, p, b, c)
							step := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42710", `constraint "t_a_fkey" for relation "t" already exists`))
							if prepared {
								held := addConstraintHeld(t, p, b, c, q)
								enforcementUnchanged(t, p, b, c, before)
								validateConstraintHeldRun(t, p, b, held, step)
							} else {
								run(step)
							}
							enforcementUnchanged(t, p, b, c, before)
							run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass AND contype IN ('c','f')", []string{"conname"}, []string{"NAME"}, nil))
							return
						}
						peer, _ := fixture.LockConnections(t, p.Ctx, b.DB)
						pid := 0
						if b.PG {
							pid = fixture.LockPID(t, p.Ctx, c)
						}
						var held fixture.HeldStatement
						if prepared {
							held = addConstraintHeld(t, p, b, c, q)
						}
						run(fixture.Exec("BEGIN", 0))
						defer c.ExecContext(p.Ctx, "ROLLBACK")
						if prepared {
							validateConstraintHeldRun(t, p, b, held, fixture.Exec(q, 0))
						} else {
							run(fixture.Exec(q, 0))
						}
						if b.PG {
							var granted bool
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation=$2::regclass AND mode='AccessExclusiveLock' AND granted)", pid, parent).Scan(&granted); err != nil || !granted {
								t.Fatal("missing generated-name parent AX", parent, granted, err)
							}
						}
						tx, err := peer.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						probe := "LOCK TABLE ONLY " + parent + " IN ACCESS SHARE MODE NOWAIT"
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic(probe, fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on relation "`+strings.Trim(parent, `"`)+`"`)))
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						displayColumn := column
						if route == "utf8_collision" {
							displayColumn = `"` + column + `"`
						}
						for _, name := range names {
							oid := enforcementIdentity(t, p, b, c, relation, name, "FOREIGN KEY ("+displayColumn+") REFERENCES "+parent+"(id) NOT ENFORCED", false, false)
							enforcementTriggers(t, p, b, c, oid, false)
						}
						run(fixture.Exec("COMMIT", 0))
						tx, err = peer.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE ONLY "+parent+" IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
					})
				})
			}
		}
	}
	for _, prepared := range []bool{false, true} {
		label := "distinct_parents/direct"
		if prepared {
			label = "distinct_parents/held"
		}
		t.Run(label, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := enforcementRun(t, p, b, c)
				for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE p2(id INT PRIMARY KEY)", "CREATE TABLE t(a INT,b INT)"} {
					run(fixture.Exec(q, 0))
				}
				for _, q := range []string{"INSERT INTO p VALUES(1)", "INSERT INTO p2 VALUES(1)", "INSERT INTO t VALUES(1,1)"} {
					run(fixture.Exec(q, 1))
				}
				q := "ALTER TABLE t ADD FOREIGN KEY(a) REFERENCES p NOT ENFORCED,ADD FOREIGN KEY(b) REFERENCES p2 NOT ENFORCED,ALTER CONSTRAINT t_a_fkey ENFORCED,ALTER CONSTRAINT t_a_fkey NOT ENFORCED,ALTER CONSTRAINT t_b_fkey ENFORCED"
				var held fixture.HeldStatement
				if prepared {
					held = addConstraintHeld(t, p, b, c, q)
				}
				peer, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				pid := 0
				if b.PG {
					pid = fixture.LockPID(t, p.Ctx, c)
				}
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				if prepared {
					validateConstraintHeldRun(t, p, b, held, fixture.Exec(q, 0))
				} else {
					run(fixture.Exec(q, 0))
				}
				for _, parent := range []string{"p", "p2"} {
					mode := "AccessExclusiveLock"
					if parent == "p2" {
						mode = "ShareRowExclusiveLock"
					}
					if b.PG {
						var granted bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation=$2::regclass AND mode=$3 AND granted)", pid, parent, mode).Scan(&granted); err != nil || !granted {
							t.Fatal(parent, mode, granted, err)
						}
					}
					probe := func(mode string, success bool) {
						tx, err := peer.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						query := "LOCK TABLE ONLY " + parent + " IN " + mode + " MODE NOWAIT"
						step := fixture.Exec(query, 0)
						if !success {
							step = fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on relation "`+parent+`"`))
						}
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, step)
					}
					probe("ACCESS SHARE", parent == "p2")
					probe("ROW EXCLUSIVE", false)
				}
				a := enforcementIdentity(t, p, b, c, "t", "t_a_fkey", "FOREIGN KEY (a) REFERENCES p(id) NOT ENFORCED", false, false)
				bid := enforcementIdentity(t, p, b, c, "t", "t_b_fkey", "FOREIGN KEY (b) REFERENCES p2(id)", true, true)
				enforcementTriggers(t, p, b, c, a, false)
				enforcementTriggers(t, p, b, c, bid, true)
				run(fixture.Exec("COMMIT", 0))
				run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(9,9)", addConstraintFK("t", "t_b_fkey", "b", "9", "p2")))
				run(fixture.Exec("INSERT INTO t VALUES(9,1)", 1))
				run(fixture.Select("SELECT a,b FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(9), int64(1)}}))
			})
		})
	}
	for _, prepared := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			label := "blocked/direct/release"
			if prepared {
				label = "blocked/held/release"
			}
			if canceled {
				label += "/cancel"
			}
			t.Run(label, func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					addConstraintSetup(t, p, b, c)
					q := "ALTER TABLE t ADD FOREIGN KEY(a) REFERENCES p NOT ENFORCED,ALTER CONSTRAINT t_a_fkey ENFORCED,ALTER CONSTRAINT t_a_fkey NOT ENFORCED"
					execute := func(ctx context.Context) (sql.Result, error) { return c.ExecContext(ctx, q) }
					if prepared {
						held := addConstraintHeld(t, p, b, c, q)
						execute = func(ctx context.Context) (sql.Result, error) { return held.ExecContext(ctx, held.SQL) }
					}
					holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
					before := enforcementSnapshot(t, p, b, observer)
					waiterPID, holderPID := 0, 0
					var oid int64
					if b.PG {
						waiterPID, holderPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, holder)
						oid = fixture.LockOID(t, p.Ctx, "p")
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
					fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, "AccessExclusiveLock", finished)
					fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass", []string{"conname"}, []string{"NAME"}, nil))
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
							t.Fatal("canceled generated ADD returned result")
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
					oid = enforcementIdentity(t, p, b, observer, "t", "t_a_fkey", "FOREIGN KEY (a) REFERENCES p(id) NOT ENFORCED", false, false)
					enforcementTriggers(t, p, b, observer, oid, false)
					enforcementReleasedLocks(t, p, b, observer, true)
				})
			})
		}
	}
}
