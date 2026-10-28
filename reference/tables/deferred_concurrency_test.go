package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeferredForeignKeyConcurrent(t *testing.T) {
	for _, parentCommit := range []bool{false, true} {
		for _, mutation := range []string{"DELETE FROM p WHERE id=1", "UPDATE p SET id=2 WHERE id=1"} {
			t.Run(fmt.Sprintf("%s/commit=%t", mutation, parentCommit), func(t *testing.T) {
				fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn, run func(fixture.Step)) {
					peer := fixture.ConcurrentIndexConn(t, p, b)
					step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec(mutation, 1))
					step(fixture.Exec("BEGIN", 0))
					step(fixture.Prepared(fixture.Exec("INSERT INTO c VALUES($1)", 1, int64(1))))
					join := fixture.DefinitionLockOperation(t, p.Ctx, p, b, peer, a, "COMMIT", "transactionid", "ShareLock", 0, 0)
					// Admission is an actual key-share/transaction wait, not a timer.
					if parentCommit {
						run(fixture.Exec("COMMIT", 0))
					} else {
						run(fixture.Exec("ROLLBACK", 0))
					}
					out := join()
					if parentCommit {
						want := fixture.ForeignKeyChild("c", "fk", `Key (pid)=(1) is not present in table "p".`)
						fixture.AssertDiagnostic(t, out.Err, &want)
						if out.Result != nil {
							t.Fatal("failed commit returned Result")
						}
					} else {
						fixture.State(t, out.Err, "")
						fixture.LockAffected(t, out.Result, 0)
					}
					rows := [][]any(nil)
					if !parentCommit {
						rows = [][]any{{int64(1)}}
					}
					run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT4"}, rows))
					run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
					run(fixture.Exec("INSERT INTO c VALUES(9)", 1))
				})
			})
		}
	}
	t.Run("parent-no-action-new-child", func(t *testing.T) {
		fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn, run func(fixture.Step)) {
			peer := fixture.ConcurrentIndexConn(t, p, b)
			step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
			step(fixture.Exec("BEGIN", 0))
			step(fixture.Exec("INSERT INTO c VALUES(1)", 1))
			step(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
			run(fixture.Exec("BEGIN", 0))
			join := fixture.DefinitionLockOperation(t, p.Ctx, p, b, a, peer, "DELETE FROM p WHERE id=1", "transactionid", "ShareLock", 0, 0)
			step(fixture.Exec("COMMIT", 0))
			out := join()
			fixture.State(t, out.Err, "")
			fixture.LockAffected(t, out.Result, 1)
			run(fixture.ErrorDiagnostic("COMMIT", fixture.ForeignKeyParent("p", "c", "fk", `Key (id)=(1) is still referenced from table "c".`)))
			run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	})
	t.Run("repeatable-read-fresh-key-protection", func(t *testing.T) {
		fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn, run func(fixture.Step)) {
			peer := fixture.ConcurrentIndexConn(t, p, b)
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
			run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
			run(fixture.Select("SELECT id FROM p", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("DELETE FROM p", 1))
			run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
			want := fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to concurrent update")
			want.Context = `SQL statement "SELECT 1 FROM ONLY "public"."p" x WHERE "id" OPERATOR(pg_catalog.=) $1 FOR KEY SHARE OF x"`
			run(fixture.ErrorDiagnostic("COMMIT", want))
			run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT4"}, nil))
			run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
			run(fixture.Exec("INSERT INTO c VALUES(9)", 1))
		})
	})
}

func TestReferenceDeferredConstraintCancellation(t *testing.T) {
	fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn, run func(fixture.Step)) {
		peer := fixture.ConcurrentIndexConn(t, p, b)
		step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT u UNIQUE(v) INITIALLY DEFERRED)", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		step(fixture.Exec("BEGIN", 0))
		step(fixture.Exec("SAVEPOINT s", 0))
		step(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		fixture.DefinitionActiveCancel(t, p, b, peer, a, "SET CONSTRAINTS u IMMEDIATE", "transactionid", "ShareLock", 0, 0, "u", func(ctx context.Context) {
			// A separate key progresses while the conflicting recheck is parked.
			fixture.RunStep(t, ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO t VALUES(9)", 1))
		})
		step(fixture.Exec("ROLLBACK TO s", 0))
		step(fixture.Exec("INSERT INTO t VALUES(2)", 1))
		step(fixture.Exec("SET CONSTRAINTS u IMMEDIATE", 0))
		step(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(9)}}))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
	})
}
