package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeferredConstraintSet(t *testing.T) {
	for _, target := range []string{"ALL", "u", "public.u", "u,u"} {
		t.Run(target, func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(id INT,v INT,CONSTRAINT u UNIQUE(v) DEFERRABLE)", 0))
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
				run(fixture.Exec("INSERT INTO t VALUES(3,1)", 1))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.ErrorDiagnostic(q, fixture.DeferredUniqueConflict("t", "u", "v", "1")))
				run(fixture.ErrorDiagnostic("SELECT v FROM t", fixture.TransactionAborted()))
				run(fixture.Exec("ROLLBACK TO s", 0))
				run(fixture.Exec("INSERT INTO t VALUES(3,1)", 1))
				run(fixture.Exec("DELETE FROM t WHERE id>1", 2))
				run(fixture.Exec("RELEASE s", 0))
				run(fixture.Exec(q, 0))
				run(fixture.Exec("SET CONSTRAINTS "+target+" DEFERRED", 0))
				run(fixture.Exec("COMMIT AND CHAIN", 0))
				run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(4,1)", fixture.DeferredUniqueConflict("t", "u", "v", "1")))
				run(fixture.Exec("ROLLBACK", 0))
			})
		})
	}
	t.Run("override-order", func(t *testing.T) {
		fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT u UNIQUE(v) DEFERRABLE)", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET CONSTRAINTS u IMMEDIATE", 0))
			run(fixture.Exec("SET CONSTRAINTS ALL DEFERRED", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1),(1)", 2))
			run(fixture.Exec("DELETE FROM t", 2))
			run(fixture.Exec("SET CONSTRAINTS u IMMEDIATE", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1),(1)", fixture.DeferredUniqueConflict("t", "u", "v", "1")))
			run(fixture.Exec("ROLLBACK", 0))
		})
	})
}

func TestReferenceDeferredConstraintLookup(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "deferred_first")
	fixture.SchemaReferenceCleanup(t, "deferred_second")
	fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE SCHEMA deferred_first", 0))
		run(fixture.Exec("CREATE SCHEMA deferred_second", 0))
		run(fixture.Exec("CREATE TABLE deferred_first.t(v INT,CONSTRAINT u UNIQUE(v) DEFERRABLE)", 0))
		run(fixture.Exec("CREATE TABLE deferred_second.t(v INT,CONSTRAINT u UNIQUE(v))", 0))
		run(fixture.Exec("SET search_path=deferred_first,deferred_second,public", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET CONSTRAINTS u DEFERRED", 0))
		run(fixture.Exec("INSERT INTO deferred_first.t VALUES(1),(1)", 2))
		run(fixture.Exec("DELETE FROM deferred_first.t", 2))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("CREATE TABLE deferred_first.other(v INT,CONSTRAINT u CHECK(v>0))", 0))
		for _, tc := range []struct{ q, code, message string }{
			{"SET CONSTRAINTS u DEFERRED", "42809", `constraint "u" is not deferrable`},
			{"SET CONSTRAINTS deferred_second.u DEFERRED", "42809", `constraint "u" is not deferrable`},
			{"SET CONSTRAINTS missing IMMEDIATE", "42704", `constraint "missing" does not exist`},
		} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Prepared(fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message))))
			run(fixture.Exec("ROLLBACK", 0))
		}
		run(fixture.Exec("SET search_path=public", 0))
		run(fixture.Exec("CREATE TABLE p(v INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(v INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
		run(fixture.ErrorDiagnostic("INSERT INTO c VALUES(9)", fixture.ForeignKeyChild("c", "fk", `Key (v)=(9) is not present in table "p".`)))
		run(fixture.Exec("ROLLBACK", 0))
		f := fixture.NewControlsReferenceFixture(t, p, b.PG)
		f.Exec(t, f.A, "SET CONSTRAINTS fk IMMEDIATE", false, 0)
		f.NoticesWant(t, "25P01", "SET CONSTRAINTS can only be used in transaction blocks")
	})
}

func TestReferenceDeferredConstraintCompletion(t *testing.T) {
	for _, kind := range []string{"unique", "fk", "exclude"} {
		for _, valid := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/valid=%t/commit=%t", kind, valid, commit), func(t *testing.T) {
					fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
						const gid = "deferred_constraint_round1"
						t.Cleanup(func() { c.ExecContext(p.Ctx, "ROLLBACK"); c.ExecContext(p.Ctx, "ROLLBACK PREPARED '"+gid+"'") })
						definition := "v INT,CONSTRAINT u UNIQUE(v) INITIALLY DEFERRED"
						want := fixture.DeferredUniqueConflict("t", "u", "v", "1")
						if kind == "exclude" {
							definition = "v INT,CONSTRAINT u EXCLUDE(v WITH =) INITIALLY DEFERRED"
							want = fixture.ExclusionReferenceConflict("t", "u", "v", "1", "1")
						}
						if kind == "fk" {
							run(fixture.Exec("CREATE TABLE p(v INT PRIMARY KEY)", 0))
							definition = "v INT CONSTRAINT u REFERENCES p INITIALLY DEFERRED"
							want = fixture.ForeignKeyChild("t", "u", `Key (v)=(1) is not present in table "p".`)
						}
						run(fixture.Exec("CREATE TABLE t("+definition+")", 0))
						run(fixture.Exec("BEGIN", 0))
						run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
						if kind == "fk" {
							if valid {
								run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
							}
						} else if !valid {
							run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
						}
						run(fixture.Exec("SAVEPOINT s", 0))
						run(fixture.Exec("RELEASE s", 0))
						if valid {
							run(fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0))
							command := "ROLLBACK"
							if commit {
								command = "COMMIT"
							}
							run(fixture.Exec(command+" PREPARED '"+gid+"'", 0))
						} else {
							run(fixture.ErrorDiagnostic("PREPARE TRANSACTION '"+gid+"'", want))
							run(fixture.Exec("ROLLBACK", 0))
						}
						run(fixture.Select("SELECT gid FROM pg_prepared_xacts WHERE gid='"+gid+"'", []string{"gid"}, []string{"TEXT"}, nil))
						rows := [][]any(nil)
						if valid && commit {
							rows = [][]any{{int64(1)}}
						}
						run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, rows))
					})
				})
			}
		}
	}
	t.Run("procedure-boundary", func(t *testing.T) {
		fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT u UNIQUE(v) INITIALLY DEFERRED)", 0))
			run(fixture.Exec("CREATE PROCEDURE deferred_boundary() LANGUAGE plpgsql AS $$ BEGIN INSERT INTO t VALUES(1),(1); COMMIT; END $$", 0))
			want := fixture.DeferredUniqueConflict("t", "u", "v", "1")
			want.Context = "PL/pgSQL function deferred_boundary() line 1 at COMMIT"
			run(fixture.ErrorDiagnostic("CALL deferred_boundary()", want))
			run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, nil))
		})
	})
}
