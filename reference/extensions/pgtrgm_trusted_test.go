package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func pgTrgmTrustedDeclarationCase(t *testing.T) {
	t.Run("TrustedInstallerOwnership", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := pgTrgmRun(t, p, b, c)
			var database string
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
			if err := c.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("CREATE USER trgm_owner NOSUPERUSER", 0))
			quotedDatabase := `"` + strings.ReplaceAll(database, `"`, `""`) + `"`
			// Database ACLs outlive schema reset. Revoke on assertion failure
			// too, before the scenario's later schema and role cleanup.
			defer func() {
				for _, query := range []string{
					"RESET SESSION AUTHORIZATION",
					"REVOKE CREATE ON DATABASE " + quotedDatabase + " FROM trgm_owner",
				} {
					if _, err := c.ExecContext(ctx, query); err != nil {
						t.Error(query, err)
					}
				}
			}()
			run(fixture.Exec("GRANT CREATE ON DATABASE "+quotedDatabase+" TO trgm_owner", 0))
			run(fixture.Exec("CREATE SCHEMA trgm_addon AUTHORIZATION trgm_owner", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION trgm_owner", 0))
			run(fixture.Exec("CREATE EXTENSION pg_trgm SCHEMA trgm_addon", 0))
			bootstrap := "postgres"
			if b.PG {
				bootstrap = "stuntgres"
			}
			run(fixture.Select(
				"SELECT e.extversion,e.extrelocatable,r.rolname,p.proowner=(SELECT oid FROM pg_roles WHERE rolname=$1) AS bootstrap_owner,p.proowner<>e.extowner AS split_owner FROM pg_extension e JOIN pg_roles r ON r.oid=e.extowner JOIN pg_proc p ON p.oid='trgm_addon.similarity(text,text)'::regprocedure::oid WHERE e.extname='pg_trgm'",
				[]string{"extversion", "extrelocatable", "rolname", "bootstrap_owner", "split_owner"},
				[]string{"TEXT", "BOOL", "NAME", "BOOL", "BOOL"},
				[][]any{{"1.6", true, "trgm_owner", true, true}}, bootstrap,
			))
			run(pgTrgmScalar(b, "SELECT trgm_addon.similarity('a','aa')", "similarity", "FLOAT4", float64(0.25)))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			pgTrgmNotices(t, notices)
		})
	})
}
