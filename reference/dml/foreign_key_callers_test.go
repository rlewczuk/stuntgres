package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteCoreForeignKeyBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT c_pid_fkey REFERENCES p(id))", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
			run(fixture.Exec("INSERT INTO c VALUES(2)", 1))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("DELETE FROM p", fixture.Diagnostic{Code: "23503", Message: `update or delete on table "p" violates foreign key constraint "c_pid_fkey" on table "c"`, Detail: `Key (id)=(2) is still referenced from table "c".`, Schema: "public", Table: "c", Constraint: "c_pid_fkey"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT4"}, [][]any{{int64(2)}}))
			}
		})
	}
}

func TestReferenceForeignKeyDeleteReturningCaller(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE child(id INT CONSTRAINT child_ref REFERENCES t(id))", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Exec("INSERT INTO child VALUES(1)", 1))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("DELETE FROM t RETURNING id", fixture.Diagnostic{Code: "23503", Message: `update or delete on table "t" violates foreign key constraint "child_ref" on table "child"`, Detail: `Key (id)=(1) is still referenced from table "child".`, Schema: "public", Table: "child", Constraint: "child_ref"})
				s.Prepared, s.ErrorStage, s.SelectRows = prepared, "execute", true
				run(s)
				run(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(fixture.Select("SELECT id FROM child", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			}
		})
	}
}
