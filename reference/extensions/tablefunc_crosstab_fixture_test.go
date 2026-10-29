package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func crosstabCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"ct_reader", "ct_owner"})
	for _, schema := range []string{"ct_addon", "ct_moved", "ct_path"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE", "RESET search_path", "DROP EXTENSION IF EXISTS tablefunc CASCADE", "DROP SCHEMA IF EXISTS ct_addon CASCADE", "DROP SCHEMA IF EXISTS ct_moved CASCADE", "DROP SCHEMA IF EXISTS ct_path CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
					// Database CREATE grants survive the public-schema reset.
					// Drop owned objects/ACLs before the cluster role cleanup.
					var owners int
					if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE rolname='ct_owner'").Scan(&owners); err != nil {
						t.Error(err)
					} else if owners != 0 {
						for _, q := range []string{"DROP OWNED BY ct_owner", "DROP ROLE ct_owner"} {
							if _, err := c.ExecContext(p.Ctx, q); err != nil {
								t.Error(q, err)
							}
						}
					}
				}
				clean()
				defer clean()
			}
			body(t, p, b, c)
		})
	}
}

func crosstabSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.CrosstabSeedInSchema(t, p, b, c, "")
}

func crosstabFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, stage string, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, d, args...)
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		if prepared {
			s.ErrorStage = stage
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func crosstabReturnError(detail string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("42804", "invalid crosstab return type")
	d.Detail = detail
	return d
}
