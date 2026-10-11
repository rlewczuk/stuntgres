package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExclusionDeferral(t *testing.T) {
	for _, mode := range []string{"NOT DEFERRABLE", "DEFERRABLE INITIALLY IMMEDIATE"} {
		t.Run("statement/"+mode, func(t *testing.T) {
			fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, b.DB, s) }
				run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) "+mode+")", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
				if mode == "NOT DEFERRABLE" {
					run(fixture.ErrorDiagnostic("UPDATE t SET v=v+1", fixture.ExclusionReferenceConflict("t", "ex", "v", "2", "2")))
					run(fixture.ExclusionReferenceRows("SELECT v FROM t ORDER BY v", 1, 2))
				} else {
					run(fixture.Prepared(fixture.Exec("UPDATE t SET v=v+1", 2)))
					run(fixture.ExclusionReferenceRows("SELECT v FROM t ORDER BY v", 2, 3))
				}
			})
		})
	}
	for _, goCommit := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed_commit/go=%t", goCommit), func(t *testing.T) {
			fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := fixture.ConcurrentIndexConn(t, p, b)
				run := func(x fixture.Executor, s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, x, s) }
				run(c, fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) INITIALLY DEFERRED)", 0))
				want := fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")
				if goCommit {
					tx, err := c.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					run(tx, fixture.Exec("INSERT INTO t VALUES(1)", 1))
					run(tx, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(1)", 1)))
					fixture.AssertDiagnostic(t, tx.Commit(), &want)
				} else {
					run(c, fixture.Exec("BEGIN", 0))
					run(c, fixture.Exec("INSERT INTO t VALUES(1),(1)", 2))
					run(c, fixture.ErrorDiagnostic("COMMIT", want))
				}
				run(c, fixture.ExclusionReferenceRows("SELECT v FROM t"))
				run(c, fixture.ErrorDiagnostic("INSERT INTO t VALUES(1),(1)", want))
				run(c, fixture.ExclusionReferenceRows("SELECT v FROM t"))
				run(c, fixture.Exec("INSERT INTO t VALUES(2)", 1))
			})
		})
	}
	for _, repair := range []string{"DELETE FROM t WHERE id=2", "UPDATE t SET v=2 WHERE id=2"} {
		t.Run("repair/"+repair, func(t *testing.T) {
			fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := fixture.ConcurrentIndexConn(t, p, b)
				run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, c, s) }
				run(fixture.Exec("CREATE TABLE t(id INT,v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE INITIALLY DEFERRED)", 0))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,1),(2,1)", 2))
				run(fixture.Exec("SAVEPOINT s", 0))
				run(fixture.Exec("RELEASE SAVEPOINT s", 0))
				run(fixture.Prepared(fixture.Exec(repair, 1)))
				run(fixture.Exec("COMMIT", 0))
				values := []int64{1}
				if repair[0] == 'U' {
					values = append(values, 2)
				}
				run(fixture.ExclusionReferenceRows("SELECT v FROM t ORDER BY v", values...))
			})
		})
	}
}

func TestReferenceExclusionSetConstraints(t *testing.T) {
	for _, target := range []string{"ALL", "ex", "public.ex", "ex,ex"} {
		t.Run("retroactive/"+target, func(t *testing.T) {
			fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := fixture.ConcurrentIndexConn(t, p, b)
				run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, c, s) }
				run(fixture.Exec("CREATE TABLE t(id INT,v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE INITIALLY IMMEDIATE)", 0))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Prepared(fixture.Exec("SET CONSTRAINTS "+target+" DEFERRED", 0)))
				run(fixture.Exec("INSERT INTO t VALUES(1,1),(2,1)", 2))
				run(fixture.Exec("SAVEPOINT s", 0))
				q := "SET CONSTRAINTS " + target + " IMMEDIATE"
				held, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				// Prepare cannot check outstanding rows or change transaction mode.
				run(fixture.ExclusionReferenceRows("SELECT v FROM t ORDER BY id", 1, 1))
				want := fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")
				fixture.ExclusionReferenceRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, fixture.ErrorDiagnostic(q, want))
				run(fixture.ErrorDiagnostic("SELECT v FROM t", fixture.TransactionAborted()))
				run(fixture.Exec("ROLLBACK TO SAVEPOINT s", 0))
				// Restored mode is DEFERRED; another collision is admitted.
				run(fixture.Exec("INSERT INTO t VALUES(3,1)", 1))
				run(fixture.Exec("DELETE FROM t WHERE id>1", 2))
				run(fixture.Exec("RELEASE SAVEPOINT s", 0))
				run(fixture.Exec(q, 0))
				run(fixture.Exec("SET CONSTRAINTS "+target+" DEFERRED", 0))
				run(fixture.Exec("COMMIT AND CHAIN", 0))
				// New incarnation uses definition defaults, not the old named override.
				run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(4,1)", want))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.ExclusionReferenceRows("SELECT v FROM t", 1))
			})
		})
	}
	for _, c := range []struct{ q, code, message string }{
		{"SET CONSTRAINTS missing IMMEDIATE", "42704", `constraint "missing" does not exist`},
		{"SET CONSTRAINTS ex DEFERRED", "42809", `constraint "ex" is not deferrable`},
		{"SET CONSTRAINTS ck DEFERRED", "42809", `constraint "ck" is not deferrable`},
	} {
		t.Run(c.q, func(t *testing.T) {
			fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				x := fixture.ConcurrentIndexConn(t, p, b)
				run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, x, s) }
				run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =),CONSTRAINT ck CHECK(v>0))", 0))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SET CONSTRAINTS ex,ck IMMEDIATE", 0))
				run(fixture.Exec("SET CONSTRAINTS ALL DEFERRED", 0))
				run(fixture.Prepared(fixture.ErrorDiagnostic(c.q, fixture.ControlsReferenceDiagnostic(c.code, c.message))))
				run(fixture.Exec("ROLLBACK", 0))
			})
		})
	}
}

func TestReferenceExclusionSetConstraintsLookup(t *testing.T) {
	fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, c, s) }
		// Clean nonpublic schemas explicitly; fixture reset only owns public.
		run(fixture.Exec("CREATE SCHEMA exclusion_first", 0))
		run(fixture.Exec("CREATE SCHEMA exclusion_second", 0))
		defer c.ExecContext(p.Ctx, "DROP SCHEMA exclusion_first CASCADE; DROP SCHEMA exclusion_second CASCADE")
		run(fixture.Exec("CREATE TABLE exclusion_first.t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE)", 0))
		run(fixture.Exec("CREATE TABLE exclusion_second.t(v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
		run(fixture.Exec("SET search_path=exclusion_first,exclusion_second,public", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET CONSTRAINTS ex DEFERRED", 0))
		run(fixture.Exec("INSERT INTO exclusion_first.t VALUES(1),(1)", 2))
		run(fixture.Exec("DELETE FROM exclusion_first.t", 2))
		run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.ErrorDiagnostic("SET CONSTRAINTS exclusion_second.ex DEFERRED", fixture.ControlsReferenceDiagnostic("42809", `constraint "ex" is not deferrable`)))
		run(fixture.Exec("ROLLBACK", 0))
		// All names in the first matching schema matter, including CHECK names.
		run(fixture.Exec("CREATE TABLE exclusion_first.other(v INT,CONSTRAINT ex CHECK(v>0))", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.ErrorDiagnostic("SET CONSTRAINTS ex DEFERRED", fixture.ControlsReferenceDiagnostic("42809", `constraint "ex" is not deferrable`)))
		run(fixture.Exec("ROLLBACK", 0))
	})
}

func TestReferenceExclusionSetConstraintsOutside(t *testing.T) {
	fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		f := fixture.NewControlsReferenceFixture(t, p, b.PG)
		f.Exec(t, f.A, "CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE)", false, 0)
		f.NoticesWant(t, "", "")
		f.Exec(t, f.A, "SET CONSTRAINTS ex DEFERRED", false, 0)
		f.NoticesWant(t, "25P01", "SET CONSTRAINTS can only be used in transaction blocks")
		fixture.ExclusionReferenceRun(t, p, b, f.A, fixture.ErrorDiagnostic("SET CONSTRAINTS missing DEFERRED", fixture.ControlsReferenceDiagnostic("42704", `constraint "missing" does not exist`)))
		f.NoticesWant(t, "25P01", "SET CONSTRAINTS can only be used in transaction blocks")
		f.Exec(t, f.A, "BEGIN", false, 0)
		fixture.ExclusionReferenceRun(t, p, b, f.A, fixture.ErrorDiagnostic("INSERT INTO t VALUES(1),(1)", fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")))
		f.Exec(t, f.A, "ROLLBACK", false, 0)
	})
}

func TestReferenceExclusionPreparedTransactions(t *testing.T) {
	for _, valid := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("valid=%t/commit=%t", valid, commit), func(t *testing.T) {
				fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					c := fixture.ConcurrentIndexConn(t, p, b)
					run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, c, s) }
					const gid = "exclusion_checkpoint_a"
					t.Cleanup(func() { c.ExecContext(p.Ctx, "ROLLBACK"); c.ExecContext(p.Ctx, "ROLLBACK PREPARED '"+gid+"'") })
					run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) INITIALLY DEFERRED)", 0))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
					if !valid {
						run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
						run(fixture.ErrorDiagnostic("PREPARE TRANSACTION '"+gid+"'", fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")))
						run(fixture.Exec("ROLLBACK", 0))
					} else {
						run(fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0))
						run(fixture.ExclusionReferenceRows("SELECT v FROM t"))
						command := "ROLLBACK PREPARED"
						if commit {
							command = "COMMIT PREPARED"
						}
						run(fixture.Exec(command+" '"+gid+"'", 0))
					}
					run(fixture.Select("SELECT gid FROM pg_prepared_xacts WHERE gid='"+gid+"'", []string{"gid"}, []string{"TEXT"}, nil))
					if valid && commit {
						run(fixture.ExclusionReferenceRows("SELECT v FROM t", 1))
						run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")))
					} else {
						run(fixture.ExclusionReferenceRows("SELECT v FROM t"))
						run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
					}
				})
			})
		}
	}
}
