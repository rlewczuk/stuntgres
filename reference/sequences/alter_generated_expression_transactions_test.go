package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterGeneratedExpressionTransactions(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, unused func(fixture.Step)) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, `ROLLBACK`)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(1),(2),(3)`, 3))
		read, err := conn.PrepareContext(p.Ctx, `SELECT b FROM g WHERE a=8`)
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		for _, finish := range []string{"ROLLBACK", "COMMIT"} {
			run(fixture.Exec(`BEGIN`, 0))
			run(fixture.Exec(`UPDATE g SET a=8 WHERE a=1`, 1))
			run(fixture.Exec(`DELETE FROM g WHERE a=2`, 1))
			run(fixture.Exec(`INSERT INTO g(a) VALUES(4)`, 1))
			run(fixture.Exec(`ALTER TABLE g ALTER b SET EXPRESSION AS(a+2)`, 0))
			run(alterGeneratedRows(`SELECT * FROM g ORDER BY a`, []any{int64(3), int64(5)}, []any{int64(4), int64(6)}, []any{int64(8), int64(10)}))
			var value int64
			if err := read.QueryRowContext(p.Ctx).Scan(&value); err != nil || value != 10 {
				t.Fatal(value, err)
			}
			run(fixture.Exec(finish, 0))
			want := [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}, {int64(3), int64(4)}}
			if finish == "COMMIT" {
				want = [][]any{{int64(3), int64(5)}, {int64(4), int64(6)}, {int64(8), int64(10)}}
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, alterGeneratedRows(`SELECT * FROM g ORDER BY a`, want...))
		}
		run(fixture.Exec(`BEGIN`, 0))
		run(fixture.Exec(`SAVEPOINT before_set`, 0))
		run(fixture.Exec(`ALTER TABLE g ALTER b SET EXPRESSION AS(a+9)`, 0))
		run(fixture.Exec(`ROLLBACK TO before_set`, 0))
		run(alterGeneratedRows(`SELECT * FROM g WHERE a=8`, []any{int64(8), int64(10)}))
		run(fixture.Exec(`SAVEPOINT before_drop`, 0))
		run(fixture.Exec(`ALTER TABLE g ALTER b DROP EXPRESSION`, 0))
		run(fixture.Exec(`UPDATE g SET b=66 WHERE a=8`, 1))
		run(fixture.Exec(`ROLLBACK TO before_drop`, 0))
		run(alterGeneratedRows(`SELECT * FROM g WHERE a=8`, []any{int64(8), int64(10)}))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(6)`, 1))
		run(fixture.Exec(`COMMIT`, 0))
		run(alterGeneratedRows(`SELECT * FROM g WHERE a=6`, []any{int64(6), int64(8)}))
		run(fixture.Exec(`BEGIN`, 0))
		run(fixture.Exec(`SAVEPOINT failure`, 0))
		run(fixture.ImplicitError(`ALTER TABLE g ALTER b SET EXPRESSION AS(a/0)`, fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
		run(fixture.TransactionError(false, `SELECT * FROM g`, fixture.TransactionAborted()))
		run(fixture.Exec(`ROLLBACK TO failure`, 0))
		run(alterGeneratedRows(`SELECT * FROM g WHERE a=8`, []any{int64(8), int64(10)}))
		run(fixture.Exec(`COMMIT`, 0))
	})
}

func TestReferenceAlterGeneratedExpressionAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"alter_generated_actor"})
	fixture.UniqueReferenceSchemaCleanup(t, "alter_generated_owned")
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, unused func(fixture.Step)) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, `RESET SESSION AUTHORIZATION`)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec(`CREATE ROLE alter_generated_actor`, 0))
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
		if b.PG {
			run(fixture.Exec(`GRANT USAGE ON SCHEMA public TO alter_generated_actor`, 0))
		}
		run(fixture.Exec(`GRANT ALL ON g TO alter_generated_actor`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION alter_generated_actor`, 0))
		for _, q := range []string{`ALTER TABLE g ALTER b SET EXPRESSION AS(a/0)`, `ALTER TABLE g ALTER missing SET EXPRESSION AS(missing+1)`, `ALTER TABLE g ALTER b DROP EXPRESSION`} {
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "must be owner of table g"}))
		}
		run(fixture.Exec(`BEGIN READ ONLY`, 0))
		run(fixture.ImplicitError(`ALTER TABLE missing_g ALTER missing SET EXPRESSION AS(1/0)`, fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}))
		run(fixture.Exec(`ROLLBACK`, 0))
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(alterGeneratedRows(`SELECT * FROM g`, []any{int64(2), int64(3)}))
		run(fixture.Exec(`CREATE SCHEMA alter_generated_owned AUTHORIZATION alter_generated_actor`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION alter_generated_actor`, 0))
		run(fixture.Exec(`CREATE TABLE alter_generated_owned.owned(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO alter_generated_owned.owned(a) VALUES(2)`, 1))
		run(fixture.Exec(`ALTER TABLE alter_generated_owned.owned ALTER b SET EXPRESSION AS(a+2)`, 0))
		run(alterGeneratedRows(`SELECT * FROM alter_generated_owned.owned`, []any{int64(2), int64(4)}))
		run(fixture.Exec(`ALTER TABLE alter_generated_owned.owned ALTER b DROP EXPRESSION`, 0))
		run(fixture.Exec(`UPDATE alter_generated_owned.owned SET b=66`, 1))
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Exec(`DROP SCHEMA alter_generated_owned CASCADE`, 0))
	})
}
