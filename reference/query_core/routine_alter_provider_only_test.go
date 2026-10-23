package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The parent ProviderPersistence definition also exercises this no-owned-SQL
// case: ALTER replay must not depend on a check_function_bodies directive.
func routineAlterProviderOnlyPersistence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ra_provider_only_owner"})
	fixture.SchemaReferenceCleanup(t, "ra_provider_only")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		restoreAbs := routineAlterAbsCleanup(t, p, c)
		defer restoreAbs()
		// Natural schema ownership avoids adding an explicit ACL to an
		// otherwise provider-only persistence stream.
		routineExec(t, p, b, c, "CREATE ROLE ra_provider_only_owner", "CREATE SCHEMA ra_provider_only AUTHORIZATION ra_provider_only_owner", "ALTER FUNCTION pg_catalog.abs(integer) RENAME TO ra_only_abs")
		routineExec(t, p, b, c, "ALTER FUNCTION pg_catalog.ra_only_abs(integer) SET SCHEMA ra_provider_only")
		routineExec(t, p, b, c, "ALTER FUNCTION ra_provider_only.ra_only_abs(integer) OWNER TO ra_provider_only_owner")
		check := func(conn *sql.Conn, backend fixture.Backend) {
			t.Helper()
			routineValue(t, p, backend, conn, "SELECT ra_provider_only.ra_only_abs(-4::integer) AS v", "v", "INT4", int64(4))
			routineAlterRows(t, p, backend, conn, "SELECT proowner='ra_provider_only_owner'::regrole AS owned FROM pg_proc WHERE proname='ra_only_abs'", []string{"owned"}, []string{"BOOL"}, [][]any{{true}})
		}
		check(c, b)
		if b.PG {
			return
		}
		for range 2 {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "CREATE FUNCTION") || strings.Contains(dump.String(), "SET check_function_bodies = false") {
				t.Fatal("provider-only stream acquired owned SQL declarations/directive")
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(c, b)
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			if _, err := fresh.ExecContext(p.Ctx, "CREATE ROLE ra_provider_only_owner"); err != nil {
				fresh.Close()
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
				fresh.Close()
				t.Fatal(err)
			}
			var value int64
			var owned bool
			if err := fresh.QueryRowContext(p.Ctx, "SELECT ra_provider_only.ra_only_abs(-4::integer)").Scan(&value); err != nil || value != 4 {
				fresh.Close()
				t.Fatal(value, err)
			}
			if err := fresh.QueryRowContext(p.Ctx, "SELECT proowner='ra_provider_only_owner'::regrole FROM pg_proc WHERE proname='ra_only_abs'").Scan(&owned); err != nil || !owned {
				fresh.Close()
				t.Fatal(owned, err)
			}
			fresh.Close()
			if p.Postgres != nil {
				func() {
					pg, err := p.Postgres.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer pg.Close()
					restorePGAbs := routineAlterAbsCleanup(t, p, pg)
					defer restorePGAbs()
					if _, err := pg.ExecContext(p.Ctx, "CREATE ROLE ra_provider_only_owner"); err != nil {
						t.Fatal(err)
					}
					defer func() {
						restorePGAbs()
						if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS ra_provider_only CASCADE; DROP ROLE ra_provider_only_owner"); err != nil {
							t.Error(err)
						}
					}()
					if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
						t.Fatal("actual provider-only SQL replay", err)
					}
					check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
					restorePGAbs()
				}()
			}
		}
	})
}
