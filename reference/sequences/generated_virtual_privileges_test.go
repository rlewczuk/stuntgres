package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"virtual_actor"})
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, unused func(fixture.Step)) {
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE ROLE virtual_actor`, 0))
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
		if b.PG {
			run(fixture.Exec(`GRANT USAGE ON SCHEMA public TO virtual_actor`, 0))
		}
		run(fixture.Exec(`GRANT SELECT(b) ON g TO virtual_actor`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION virtual_actor`, 0))
		run(fixture.Select(`SELECT b FROM g WHERE b>0 ORDER BY b`, []string{"b"}, []string{"INT4"}, [][]any{{int64(3)}}))
		for _, q := range []string{`SELECT a FROM g`, `SELECT * FROM g`, `SELECT a FROM g WHERE FALSE`, `SELECT a FROM g WHERE b<0`} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table g"})
			s.SelectRows = true
			run(s)
		}
		q := `SELECT b FROM g`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Exec(`REVOKE SELECT(b) ON g FROM virtual_actor`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION virtual_actor`, 0))
		denied := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table g"})
		denied.SelectRows = true
		denied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, denied)
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Exec(`GRANT SELECT(b),INSERT(a),UPDATE(a) ON g TO virtual_actor`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION virtual_actor`, 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"b"}, []string{"INT4"}, [][]any{{int64(3)}}))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(4)`, 1))
		run(fixture.Select(`SELECT b FROM g ORDER BY b`, []string{"b"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(5)}}))
		sDenied := fixture.ImplicitError(`SELECT a FROM g WHERE b=5`, fixture.Diagnostic{Code: "42501", Message: "permission denied for table g"})
		sDenied.SelectRows = true
		run(sDenied)
		s := fixture.Select(`UPDATE g SET a=8 RETURNING b`, []string{"b"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(9)}})
		s.Bag = true
		run(s)
		run(fixture.ImplicitError(`UPDATE g SET a=a+1`, fixture.Diagnostic{Code: "42501", Message: "permission denied for table g"}))
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Exec(`CREATE TABLE hidden(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL,c INT CHECK(c>0))`, 0))
		run(fixture.Exec(`INSERT INTO hidden(a,c) VALUES(42,1)`, 1))
		run(fixture.Exec(`GRANT SELECT(b),UPDATE(c) ON hidden TO virtual_actor`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION virtual_actor`, 0))
		run(fixture.ImplicitError(`UPDATE hidden SET c=-1`, fixture.Diagnostic{Code: "23514", Message: `new row for relation "hidden" violates check constraint "hidden_c_check"`, Detail: "Failing row contains (b, c) = (virtual, -1).", Schema: "public", Table: "hidden", Constraint: "hidden_c_check"}))
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Select(`SELECT * FROM hidden`, []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(42), int64(43), int64(1)}}))
	})
}
