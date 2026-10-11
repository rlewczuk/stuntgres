package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompareOpsRightOnlyPublicAuthorization(t *testing.T) {
	owner, reader := "scalar_compare_owner", "scalar_compare_reader"
	fixture.RoleReferenceCleanup(t, []string{owner, reader})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer func() {
				if _, err := conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Error(err)
				}
			}()
			run := func(step fixture.Step) { fixture.ScalarRunStep(t, p.Ctx, conn, backend.Recorder, step) }
			run(fixture.Exec("CREATE USER "+owner, 0))
			run(fixture.Exec("CREATE USER "+reader, 0))
			if backend.PG {
				if _, err := fixture.Admin.ExecContext(p.Ctx, "GRANT USAGE,CREATE ON SCHEMA public TO "+owner); err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.Admin.ExecContext(p.Ctx, "GRANT USAGE ON SCHEMA public TO "+reader); err != nil {
					t.Fatal(err)
				}
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION "+owner, 0))
			run(fixture.Exec("CREATE TABLE secret(i INT)", 0))
			run(fixture.Exec("INSERT INTO secret VALUES(1)", 1))
			q := "SELECT 0<i FROM secret"
			held, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
			for _, query := range []string{q, "SELECT 7 FROM secret WHERE 0<i"} {
				step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table secret"})
				step.SelectRows = true
				step.ErrorStage = "execute"
				run(step)
			}
			step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table secret"})
			step.SelectRows = true
			step.ErrorStage = "execute"
			fixture.ScalarRunStep(t, p.Ctx, fixture.HeldStatement{conn, held, q}, backend.Recorder, step)
		})
	}
}
