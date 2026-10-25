package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// RFC 9562 sections 5.9 and 6.6, independently of either implementation.
var uuidOsspNamespaces = []struct{ name, value string }{
	{"uuid_nil", "00000000-0000-0000-0000-000000000000"},
	{"uuid_ns_dns", "6ba7b810-9dad-11d1-80b4-00c04fd430c8"},
	{"uuid_ns_url", "6ba7b811-9dad-11d1-80b4-00c04fd430c8"},
	{"uuid_ns_oid", "6ba7b812-9dad-11d1-80b4-00c04fd430c8"},
	{"uuid_ns_x500", "6ba7b814-9dad-11d1-80b4-00c04fd430c8"},
}

func uuidOsspNamespaceCase(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool)) {
	t.Helper()
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
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS \"uuid-ossp\" CASCADE", "DROP SCHEMA IF EXISTS uuid_ossp_part CASCADE", "DROP OWNED BY uuid_ossp_owner", "DROP ROLE IF EXISTS uuid_ossp_owner"} {
						// DROP OWNED needs an existing role; all other cleanup failures
						// remain visible, including failures after a fatal assertion.
						if q == "DROP OWNED BY uuid_ossp_owner" {
							var n int
							if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE rolname='uuid_ossp_owner'").Scan(&n); err != nil {
								t.Error(err)
								continue
							}
							if n == 0 {
								continue
							}
						}
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

func uuidOsspNamespaceInstall(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SCHEMA uuid_ossp_part", 0))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION \"uuid-ossp\" SCHEMA uuid_ossp_part", 0))
}

func uuidOsspNamespaceSelect(q, value string) fixture.Step {
	return fixture.Select(q, []string{"u"}, []string{"UUID"}, [][]any{{value}})
}
