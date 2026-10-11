package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedStoredAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"generated_actor"})
	fixture.UniqueReferenceSchemaCleanup(t, "generated_guarded")
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, unused func(fixture.Step)) {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
		run(fixture.Exec(`CREATE ROLE generated_actor`, 0))
		run(fixture.Exec(`CREATE SCHEMA generated_guarded`, 0))
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
		if backend.PG {
			run(fixture.Exec(`GRANT USAGE ON SCHEMA public,generated_guarded TO generated_actor`, 0))
		}
		run(fixture.Exec(`SET SESSION AUTHORIZATION generated_actor`, 0))
		for _, q := range []string{`INSERT INTO g(a) VALUES(4)`, `UPDATE g SET a=8`, `SELECT b FROM g`} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table g"})
			s.SelectRows = q == `SELECT b FROM g`
			run(s)
		}
		run(fixture.ImplicitError(`CREATE TABLE generated_guarded.denied(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema generated_guarded", Position: "14"}))
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		run(fixture.Select(`SELECT * FROM g`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
		run(fixture.Exec(`GRANT SELECT(b),INSERT(a),UPDATE(a) ON g TO generated_actor`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION generated_actor`, 0))
		run(fixture.Select(`SELECT b FROM g`, []string{"b"}, []string{"INT4"}, [][]any{{int64(3)}}))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(4)`, 1))
		run(fixture.Exec(`UPDATE g SET a=8`, 2))
		run(fixture.ImplicitError(`UPDATE g SET a=a+1`, fixture.Diagnostic{Code: "42501", Message: "permission denied for table g"}))
		run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
	})
}
