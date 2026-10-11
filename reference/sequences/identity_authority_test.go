package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIdentityInsertOnlyAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"identity_writer"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		c, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
		if backend.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, q := range []string{`CREATE TABLE secured(id INT GENERATED ALWAYS AS IDENTITY)`, `CREATE ROLE identity_writer`, `GRANT INSERT ON secured TO identity_writer`, `SET ROLE identity_writer`} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec(`INSERT INTO secured DEFAULT VALUES`, 1))
		run(fixture.ImplicitError(`SELECT nextval('secured_id_seq')`, fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence secured_id_seq"}))
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.Select(`SELECT id FROM secured`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	}
}
