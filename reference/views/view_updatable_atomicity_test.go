package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewUpdatableAtomicity(t *testing.T) {
	viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s fixture.Step) { viewWriteRun(t, p, b, held, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,a INT NOT NULL)", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2), fixture.Exec("CREATE VIEW v AS SELECT id AS key,a AS x FROM t", 0),
			fixture.ImplicitError("INSERT INTO v VALUES(3,30),(1,40)", fixture.Diagnostic{Code: "23505", Message: "duplicate key value violates unique constraint \"t_pkey\"", Detail: "Key (id)=(1) already exists.", Schema: "public", Table: "t", Constraint: "t_pkey"}),
			fixture.ImplicitError("INSERT INTO v VALUES(3,30),(4,NULL)", fixture.Diagnostic{Code: "23502", Message: "null value in column \"a\" of relation \"t\" violates not-null constraint", Detail: "Failing row contains (4, null).", Schema: "public", Table: "t", Column: "a"}),
			fixture.ImplicitError("UPDATE v SET x=x+2147483630", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}),
			fixture.ImplicitError("UPDATE v SET x=100/(key-2)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}),
			fixture.ImplicitError("DELETE FROM v WHERE 100/(key-2)>0", fixture.Diagnostic{Code: "22012", Message: "division by zero"}),
		} {
			run(s)
		}
		ret := fixture.ImplicitError("DELETE FROM v RETURNING 100/(key-2)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		ret.SelectRows = true
		run(ret)
		run(fixture.Select("SELECT id,a FROM t ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
		run(fixture.Exec("CREATE TABLE checked(id INT,a INT CONSTRAINT positive CHECK(a>0))", 0))
		run(fixture.Exec("CREATE VIEW cv AS SELECT id,a FROM checked", 0))
		run(fixture.ImplicitError("INSERT INTO cv VALUES(1,10),(2,-1)", fixture.Diagnostic{Code: "23514", Message: "new row for relation \"checked\" violates check constraint \"positive\"", Detail: "Failing row contains (2, -1).", Schema: "public", Table: "checked", Constraint: "positive"}))
		run(fixture.Select("SELECT id,a FROM checked", []string{"id", "a"}, []string{"INT4", "INT4"}, nil))
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		var err error
		if held {
			h := fixture.ViewHold(t, p, b, "UPDATE v SET x=99")
			_, err = h.ExecContext(canceled, h.SQL)
		} else {
			_, err = b.DB.ExecContext(canceled, "UPDATE v SET x=99")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("precanceled write", err)
		}
		run(fixture.Select("SELECT id,a FROM t ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
	})
}
func TestReferenceViewUpdatableTransactions(t *testing.T) {
	viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s fixture.Step) { viewWriteRun(t, p, b, held, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,a INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		run(fixture.Exec("CREATE VIEW v AS SELECT id,a FROM t WHERE id>0", 0))
		tx, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		own := func(s fixture.Step) { s.Prepared = held; fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
		own(fixture.Exec("UPDATE v SET a=11", 1))
		own(fixture.Exec("SAVEPOINT one", 0))
		own(fixture.Exec("INSERT INTO v VALUES(2,20)", 1))
		own(fixture.Select("SELECT id,a FROM v ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(20)}}))
		run(fixture.Select("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
		own(fixture.Exec("ROLLBACK TO one", 0))
		own(fixture.Exec("DELETE FROM v", 1))
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
		tx, err = b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		own(fixture.Exec("UPDATE v SET a=12", 1))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(12)}}))
	})
}
