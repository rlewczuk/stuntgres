package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateSyntaxAndPrepare(t *testing.T) {
	truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		truncateSeed(t, f)
		for _, q := range []string{"TRUNCATE p", "TRUNCATE TABLE p,q CONTINUE IDENTITY RESTRICT", "TRUNCATE ONLY p", "TRUNCATE ONLY(p),q", "TRUNCATE p *,q CASCADE", "TRUNCATE public.p,p,p CONTINUE IDENTITY"} {
			truncateRun(t, f, f.A, fixture.Prepared(fixture.Exec(q, 0)))
			truncateRows(t, f, f.A, "p")
		}
		truncateExec(t, f, f.A, `CREATE TABLE "É table"(id INT)`, 0)
		truncateExec(t, f, f.A, `TRUNCATE TABLE public."É table" CONTINUE IDENTITY`, 0)
		truncateExec(t, f, f.A, "CREATE TABLE truncate(continue INT)", 0)
		truncateExec(t, f, f.A, "INSERT INTO truncate VALUES(1)", 1)
		truncateRun(t, f, f.A, fixture.Select("TRUNCATE truncate", []string{}, []string{}, [][]any{}))
		truncateRun(t, f, f.A, fixture.Prepared(fixture.Select("TRUNCATE truncate CONTINUE IDENTITY", []string{}, []string{}, [][]any{})))
		// Utility Prepare must not resolve a name or take locks/effects.
		stmt, err := f.A.PrepareContext(p.Ctx, "TRUNCATE later")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		truncateExec(t, f, f.A, "CREATE TABLE later(id INT)", 0)
		truncateExec(t, f, f.A, "INSERT INTO later VALUES(8)", 1)
		truncateRows(t, f, f.A, "later", 8)
		r, err := stmt.ExecContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		n, err := r.RowsAffected()
		if err != nil || n != 0 {
			t.Fatalf("utility count %d: %v", n, err)
		}
		truncateRows(t, f, f.A, "later")
	})
}
func TestReferenceTruncateDiagnostics(t *testing.T) {
	truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		truncateSeed(t, f)
		for _, c := range []struct{ q, code, message, pos string }{
			{"TRUNCATE missing", "42P01", `relation "missing" does not exist`, ""},
			{"TRUNCATE missing.p", "3F000", `schema "missing" does not exist`, ""},
			{"TRUNCATE p,", "42601", "syntax error at end of input", "12"},
			{"TRUNCATE ONLY p *", "42601", `syntax error at or near "*"`, "17"},
			{"TRUNCATE p RESTRICT CASCADE", "42601", `syntax error at or near "CASCADE"`, "21"},
			{"TRUNCATE /*é*/ p CONTINUE IDENTITY junk", "42601", `syntax error at or near "junk"`, "36"},
			{"TRUNCATE p CONTINUE IDENTITY CONTINUE IDENTITY", "42601", `syntax error at or near "CONTINUE"`, "30"},
			{"TRUNCATE p CASCADE CONTINUE IDENTITY", "42601", `syntax error at or near "CONTINUE"`, "20"},
			{"TRUNCATE p,,q", "42601", `syntax error at or near ","`, "12"},
		} {
			d := truncateWant(c.code, c.message)
			d.Position = c.pos
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(c.q, d)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared && c.code == "42601" {
					s.ErrorStage = "prepare"
				}
				truncateRun(t, f, f.A, s)
			}
		}
		truncateExec(t, f, f.A, "CREATE VIEW v AS SELECT id FROM p", 0)
		truncateExec(t, f, f.A, "CREATE MATERIALIZED VIEW m AS SELECT id FROM p", 2)
		truncateExec(t, f, f.A, "CREATE SEQUENCE s", 0)
		truncateExec(t, f, f.A, "CREATE INDEX ix ON p(id)", 0)
		truncateExec(t, f, f.A, "CREATE TYPE composite AS (id INT)", 0)
		for _, name := range []string{"v", "m", "s", "ix", "composite"} {
			truncateError(t, f, f.A, "TRUNCATE "+name, truncateWant("42809", `"`+name+`" is not a table`))
		}
		for _, q := range []string{"TRUNCATE p", "TRUNCATE p,q", "TRUNCATE p,p,q CONTINUE IDENTITY"} {
			truncateExec(t, f, f.A, q, 0)
		}
		truncateExec(t, f, f.A, "INSERT INTO p VALUES(1),(2),(3)", 3)
		truncateExec(t, f, f.A, "TRUNCATE p", 0)
		truncateRows(t, f, f.A, "p")
	})
}
func TestReferenceTruncateAtomicity(t *testing.T) {
	truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		truncateSeed(t, f)
		var oidBefore, oidAfter int64
		query := "SELECT oid::bigint FROM pg_class WHERE relname='p'"
		if err := f.A.QueryRowContext(p.Ctx, query).Scan(&oidBefore); err != nil {
			t.Fatal(err)
		}
		truncateError(t, f, f.A, "TRUNCATE p,missing", truncateWant("42P01", `relation "missing" does not exist`))
		truncateRows(t, f, f.A, "p", 1, 2)
		truncateRows(t, f, f.A, "q", 7)
		tx := truncateBegin(t, p, f.A, nil)
		truncateExec(t, f, tx, "TRUNCATE p,q", 0)
		truncateRows(t, f, tx, "p")
		truncateRows(t, f, tx, "q")
		truncateExec(t, f, tx, "INSERT INTO p VALUES(1)", 1)
		d := truncateWant("23505", `duplicate key value violates unique constraint "p_pkey"`)
		d.Detail = "Key (id)=(1) already exists."
		d.Schema = "public"
		d.Table = "p"
		d.Constraint = "p_pkey"
		truncateError(t, f, tx, "INSERT INTO p VALUES(1)", d)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		truncateRows(t, f, f.A, "p", 1, 2)
		truncateRows(t, f, f.A, "q", 7)
		truncateExec(t, f, f.A, "TRUNCATE p,q", 0)
		truncateExec(t, f, f.A, "INSERT INTO p VALUES(1)", 1)
		truncateError(t, f, f.A, "INSERT INTO p VALUES(1)", d)
		if err := f.A.QueryRowContext(p.Ctx, query).Scan(&oidAfter); err != nil || oidBefore <= 0 || oidBefore != oidAfter {
			t.Fatalf("relation identity %d -> %d: %v", oidBefore, oidAfter, err)
		}
		truncateRun(t, f, f.A, fixture.Select("SELECT hasindexes FROM pg_tables WHERE tablename='p'", []string{"hasindexes"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
func TestReferenceTruncateTransactions(t *testing.T) {
	truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		truncateSeed(t, f)
		tx := truncateBegin(t, p, f.A, nil)
		truncateExec(t, f, tx, "INSERT INTO p VALUES(3)", 1)
		truncateExec(t, f, tx, "UPDATE p SET id=11 WHERE id=1", 1)
		truncateExec(t, f, tx, "DELETE FROM p WHERE id=2", 1)
		truncateExec(t, f, tx, "INSERT INTO q VALUES(8)", 1)
		truncateExec(t, f, tx, "SAVEPOINT before_truncate", 0)
		truncateRun(t, f, tx, fixture.Prepared(fixture.Exec("TRUNCATE p CONTINUE IDENTITY", 0)))
		truncateRows(t, f, tx, "p")
		truncateRows(t, f, tx, "q", 7, 8)
		truncateExec(t, f, tx, "INSERT INTO p VALUES(4)", 1)
		truncateRows(t, f, tx, "p", 4)
		truncateExec(t, f, tx, "TRUNCATE p", 0)
		truncateRows(t, f, tx, "p")
		truncateExec(t, f, tx, "ROLLBACK TO before_truncate", 0)
		truncateRows(t, f, tx, "p", 3, 11)
		truncateRows(t, f, tx, "q", 7, 8)
		truncateError(t, f, tx, "TRUNCATE p,missing", truncateWant("42P01", `relation "missing" does not exist`))
		truncateError(t, f, tx, "TRUNCATE p", truncateWant("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
		truncateExec(t, f, tx, "ROLLBACK TO before_truncate", 0)
		truncateRows(t, f, tx, "p", 3, 11)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		truncateRows(t, f, f.A, "p", 1, 2)
		truncateRows(t, f, f.A, "q", 7)
		tx = truncateBegin(t, p, f.A, &sql.TxOptions{})
		truncateExec(t, f, tx, "TRUNCATE p,q", 0)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		truncateRows(t, f, f.A, "p")
		truncateRows(t, f, f.A, "q")
		truncateExec(t, f, f.A, "BEGIN", 0)
		truncateExec(t, f, f.A, "INSERT INTO p VALUES(5)", 1)
		truncateExec(t, f, f.A, "TRUNCATE p", 0)
		truncateExec(t, f, f.A, "ROLLBACK", 0)
		truncateRows(t, f, f.A, "p")
	})
}
