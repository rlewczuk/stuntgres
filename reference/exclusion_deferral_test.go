package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceExclusionDeferral(t *testing.T) {
	for _, mode := range []string{"NOT DEFERRABLE", "DEFERRABLE INITIALLY IMMEDIATE"} {
		t.Run("statement/"+mode, func(t *testing.T) {
			exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { exclusionReferenceRun(t, p, b, b.db, s) }
				run(referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) "+mode+")", 0))
				run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
				if mode == "NOT DEFERRABLE" {
					run(referenceErrorDiagnostic("UPDATE t SET v=v+1", exclusionReferenceConflict("t", "ex", "v", "2", "2")))
					run(exclusionReferenceRows("SELECT v FROM t ORDER BY v", 1, 2))
				} else {
					run(referencePrepared(referenceExec("UPDATE t SET v=v+1", 2)))
					run(exclusionReferenceRows("SELECT v FROM t ORDER BY v", 2, 3))
				}
			})
		})
	}
	for _, goCommit := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed_commit/go=%t", goCommit), func(t *testing.T) {
			exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				c := concurrentIndexConn(t, p, b)
				run := func(x referenceExecutor, s referenceStep) { exclusionReferenceRun(t, p, b, x, s) }
				run(c, referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) INITIALLY DEFERRED)", 0))
				want := exclusionReferenceConflict("t", "ex", "v", "1", "1")
				if goCommit {
					tx, err := c.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					run(tx, referenceExec("INSERT INTO t VALUES(1)", 1))
					run(tx, referencePrepared(referenceExec("INSERT INTO t VALUES(1)", 1)))
					referenceAssertDiagnostic(t, tx.Commit(), &want)
				} else {
					run(c, referenceExec("BEGIN", 0))
					run(c, referenceExec("INSERT INTO t VALUES(1),(1)", 2))
					run(c, referenceErrorDiagnostic("COMMIT", want))
				}
				run(c, exclusionReferenceRows("SELECT v FROM t"))
				run(c, referenceErrorDiagnostic("INSERT INTO t VALUES(1),(1)", want))
				run(c, exclusionReferenceRows("SELECT v FROM t"))
				run(c, referenceExec("INSERT INTO t VALUES(2)", 1))
			})
		})
	}
	for _, repair := range []string{"DELETE FROM t WHERE id=2", "UPDATE t SET v=2 WHERE id=2"} {
		t.Run("repair/"+repair, func(t *testing.T) {
			exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				c := concurrentIndexConn(t, p, b)
				run := func(s referenceStep) { exclusionReferenceRun(t, p, b, c, s) }
				run(referenceExec("CREATE TABLE t(id INT,v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE INITIALLY DEFERRED)", 0))
				run(referenceExec("BEGIN", 0))
				run(referenceExec("INSERT INTO t VALUES(1,1),(2,1)", 2))
				run(referenceExec("SAVEPOINT s", 0))
				run(referenceExec("RELEASE SAVEPOINT s", 0))
				run(referencePrepared(referenceExec(repair, 1)))
				run(referenceExec("COMMIT", 0))
				values := []int64{1}
				if repair[0] == 'U' {
					values = append(values, 2)
				}
				run(exclusionReferenceRows("SELECT v FROM t ORDER BY v", values...))
			})
		})
	}
}

func TestReferenceExclusionSetConstraints(t *testing.T) {
	for _, target := range []string{"ALL", "ex", "public.ex", "ex,ex"} {
		t.Run("retroactive/"+target, func(t *testing.T) {
			exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				c := concurrentIndexConn(t, p, b)
				run := func(s referenceStep) { exclusionReferenceRun(t, p, b, c, s) }
				run(referenceExec("CREATE TABLE t(id INT,v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE INITIALLY IMMEDIATE)", 0))
				run(referenceExec("BEGIN", 0))
				run(referencePrepared(referenceExec("SET CONSTRAINTS "+target+" DEFERRED", 0)))
				run(referenceExec("INSERT INTO t VALUES(1,1),(2,1)", 2))
				run(referenceExec("SAVEPOINT s", 0))
				q := "SET CONSTRAINTS " + target + " IMMEDIATE"
				held, err := c.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				// Prepare cannot check outstanding rows or change transaction mode.
				run(exclusionReferenceRows("SELECT v FROM t ORDER BY id", 1, 1))
				want := exclusionReferenceConflict("t", "ex", "v", "1", "1")
				exclusionReferenceRun(t, p, b, referenceHeldStatement{referenceExecutor: c, statement: held, query: q}, referenceErrorDiagnostic(q, want))
				run(referenceErrorDiagnostic("SELECT v FROM t", referenceTransactionAborted()))
				run(referenceExec("ROLLBACK TO SAVEPOINT s", 0))
				// Restored mode is DEFERRED; another collision is admitted.
				run(referenceExec("INSERT INTO t VALUES(3,1)", 1))
				run(referenceExec("DELETE FROM t WHERE id>1", 2))
				run(referenceExec("RELEASE SAVEPOINT s", 0))
				run(referenceExec(q, 0))
				run(referenceExec("SET CONSTRAINTS "+target+" DEFERRED", 0))
				run(referenceExec("COMMIT AND CHAIN", 0))
				// New incarnation uses definition defaults, not the old named override.
				run(referenceErrorDiagnostic("INSERT INTO t VALUES(4,1)", want))
				run(referenceExec("ROLLBACK", 0))
				run(exclusionReferenceRows("SELECT v FROM t", 1))
			})
		})
	}
	for _, c := range []struct{ q, code, message string }{
		{"SET CONSTRAINTS missing IMMEDIATE", "42704", `constraint "missing" does not exist`},
		{"SET CONSTRAINTS ex DEFERRED", "42809", `constraint "ex" is not deferrable`},
		{"SET CONSTRAINTS ck DEFERRED", "42809", `constraint "ck" is not deferrable`},
	} {
		t.Run(c.q, func(t *testing.T) {
			exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				x := concurrentIndexConn(t, p, b)
				run := func(s referenceStep) { exclusionReferenceRun(t, p, b, x, s) }
				run(referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =),CONSTRAINT ck CHECK(v>0))", 0))
				run(referenceExec("BEGIN", 0))
				run(referenceExec("SET CONSTRAINTS ex,ck IMMEDIATE", 0))
				run(referenceExec("SET CONSTRAINTS ALL DEFERRED", 0))
				run(referencePrepared(referenceErrorDiagnostic(c.q, controlsReferenceDiagnostic(c.code, c.message))))
				run(referenceExec("ROLLBACK", 0))
			})
		})
	}
}

func TestReferenceExclusionSetConstraintsLookup(t *testing.T) {
	exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		c := concurrentIndexConn(t, p, b)
		run := func(s referenceStep) { exclusionReferenceRun(t, p, b, c, s) }
		// Clean nonpublic schemas explicitly; fixture reset only owns public.
		run(referenceExec("CREATE SCHEMA exclusion_first", 0))
		run(referenceExec("CREATE SCHEMA exclusion_second", 0))
		defer c.ExecContext(p.ctx, "DROP SCHEMA exclusion_first CASCADE; DROP SCHEMA exclusion_second CASCADE")
		run(referenceExec("CREATE TABLE exclusion_first.t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE)", 0))
		run(referenceExec("CREATE TABLE exclusion_second.t(v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
		run(referenceExec("SET search_path=exclusion_first,exclusion_second,public", 0))
		run(referenceExec("BEGIN", 0))
		run(referenceExec("SET CONSTRAINTS ex DEFERRED", 0))
		run(referenceExec("INSERT INTO exclusion_first.t VALUES(1),(1)", 2))
		run(referenceExec("DELETE FROM exclusion_first.t", 2))
		run(referenceExec("SET CONSTRAINTS ALL IMMEDIATE", 0))
		run(referenceExec("COMMIT", 0))
		run(referenceExec("BEGIN", 0))
		run(referenceErrorDiagnostic("SET CONSTRAINTS exclusion_second.ex DEFERRED", controlsReferenceDiagnostic("42809", `constraint "ex" is not deferrable`)))
		run(referenceExec("ROLLBACK", 0))
		// All names in the first matching schema matter, including CHECK names.
		run(referenceExec("CREATE TABLE exclusion_first.other(v INT,CONSTRAINT ex CHECK(v>0))", 0))
		run(referenceExec("BEGIN", 0))
		run(referenceErrorDiagnostic("SET CONSTRAINTS ex DEFERRED", controlsReferenceDiagnostic("42809", `constraint "ex" is not deferrable`)))
		run(referenceExec("ROLLBACK", 0))
	})
}

func TestReferenceExclusionSetConstraintsOutside(t *testing.T) {
	exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		f := newControlsReferenceFixture(t, p, b.pg)
		f.exec(t, f.a, "CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE)", false, 0)
		f.noticesWant(t, "", "")
		f.exec(t, f.a, "SET CONSTRAINTS ex DEFERRED", false, 0)
		f.noticesWant(t, "25P01", "SET CONSTRAINTS can only be used in transaction blocks")
		exclusionReferenceRun(t, p, b, f.a, referenceErrorDiagnostic("SET CONSTRAINTS missing DEFERRED", controlsReferenceDiagnostic("42704", `constraint "missing" does not exist`)))
		f.noticesWant(t, "25P01", "SET CONSTRAINTS can only be used in transaction blocks")
		f.exec(t, f.a, "BEGIN", false, 0)
		exclusionReferenceRun(t, p, b, f.a, referenceErrorDiagnostic("INSERT INTO t VALUES(1),(1)", exclusionReferenceConflict("t", "ex", "v", "1", "1")))
		f.exec(t, f.a, "ROLLBACK", false, 0)
	})
}

func TestReferenceExclusionPreparedTransactions(t *testing.T) {
	for _, valid := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("valid=%t/commit=%t", valid, commit), func(t *testing.T) {
				exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					c := concurrentIndexConn(t, p, b)
					run := func(s referenceStep) { exclusionReferenceRun(t, p, b, c, s) }
					const gid = "exclusion_checkpoint_a"
					t.Cleanup(func() { c.ExecContext(p.ctx, "ROLLBACK"); c.ExecContext(p.ctx, "ROLLBACK PREPARED '"+gid+"'") })
					run(referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) INITIALLY DEFERRED)", 0))
					run(referenceExec("BEGIN", 0))
					run(referenceExec("INSERT INTO t VALUES(1)", 1))
					if !valid {
						run(referenceExec("INSERT INTO t VALUES(1)", 1))
						run(referenceErrorDiagnostic("PREPARE TRANSACTION '"+gid+"'", exclusionReferenceConflict("t", "ex", "v", "1", "1")))
						run(referenceExec("ROLLBACK", 0))
					} else {
						run(referenceExec("PREPARE TRANSACTION '"+gid+"'", 0))
						run(exclusionReferenceRows("SELECT v FROM t"))
						command := "ROLLBACK PREPARED"
						if commit {
							command = "COMMIT PREPARED"
						}
						run(referenceExec(command+" '"+gid+"'", 0))
					}
					run(referenceSelect("SELECT gid FROM pg_prepared_xacts WHERE gid='"+gid+"'", []string{"gid"}, []string{"TEXT"}, nil))
					if valid && commit {
						run(exclusionReferenceRows("SELECT v FROM t", 1))
						run(referenceErrorDiagnostic("INSERT INTO t VALUES(1)", exclusionReferenceConflict("t", "ex", "v", "1", "1")))
					} else {
						run(exclusionReferenceRows("SELECT v FROM t"))
						run(referenceExec("INSERT INTO t VALUES(1)", 1))
					}
				})
			})
		}
	}
}
