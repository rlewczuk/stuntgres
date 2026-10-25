package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const uuidNameDNS = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

// RFC 9562 A.2/A.4 and REL_18_6 contrib/uuid-ossp regression outputs.
var uuidNameFunctions = []struct{ name, example, widgets string }{
	{"uuid_generate_v3", "5df41881-3aed-3515-88a7-2f4a814cf09e", "3d813cbb-47fb-32ba-91df-831e1593ac29"},
	{"uuid_generate_v5", "2ed6657d-e927-568b-95e1-2665a8aea6a2", "21f7f8de-8051-5b89-8680-0195ef798b6a"},
}

func uuidNameCase(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "uuid_ossp_part")
	fixture.RoleReferenceCleanup(t, []string{"uuid_ossp_owner"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if backend.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS \"uuid-ossp\" CASCADE", "DROP SCHEMA IF EXISTS uuid_ossp_part CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			body(t, p, c, backend.Recorder, backend.PG)
		})
	}
}

func uuidNameStep(q string, want any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"u"}, []string{"UUID"}, [][]any{{want}}, args...)
}

func uuidNameHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
}
