package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAvailableExtensionsVisibility(t *testing.T) {
	t.Run("OffPathAndUnprivileged", func(t *testing.T) {
		availableExtensionsCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			run(fixture.Exec("CREATE SCHEMA ext_locked", 0))
			run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_locked", 0))
			run(fixture.Exec("CREATE USER ext_installer", 0))
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			target := `"` + strings.ReplaceAll(database, `"`, `""`) + `"`
			run(fixture.Exec("REVOKE CREATE ON DATABASE "+target+" FROM PUBLIC", 0))
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			defer c.ExecContext(p.Ctx, "RESET search_path")
			// Native private schemas already deny this reader USAGE. Real PG
			// starts private schemas without PUBLIC USAGE too: no GRANT shim.
			run(fixture.Exec("SET SESSION AUTHORIZATION ext_installer", 0))
			run(fixture.Exec("SET search_path TO public", 0))
			availableExtensionsState(t, p, c, r, pg, true)
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extensions WHERE name='unaccent'", false, availableExtensionsRow("1.1"), pg))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions WHERE name='unaccent'", true, availableVersionsRow(true), pg))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("RESET search_path", 0))
		})
	})
	t.Run("ForeignTemporaryNamespace", func(t *testing.T) {
		availableExtensionsCase(t, func(t *testing.T, p *fixture.Pair, installer *sql.Conn, r *fixture.Recorder, pg bool) {
			owner := fixture.ExtensionTemporaryOwner(t, p, pg)
			defer owner.Close(t)
			name := fixture.ExtensionTempSeed(t, p, owner)
			// The authorized installer is a different real physical session from
			// the TEMP owner; own-TEMP installation is deliberately not added.
			fixture.RunStep(t, p.Ctx, installer, r, fixture.Exec("CREATE EXTENSION unaccent SCHEMA "+fixture.ExtensionTempIdentifier(name), 0))
			availableExtensionsState(t, p, installer, r, pg, true)
			availableExtensionsState(t, p, owner.Conn, owner.Recorder, pg, true)
			fixture.RunStep(t, p.Ctx, installer, r, fixture.Exec("CREATE USER ext_installer", 0))
			fixture.RunStep(t, p.Ctx, installer, r, fixture.Exec("SET SESSION AUTHORIZATION ext_installer", 0))
			defer installer.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			availableExtensionsState(t, p, installer, r, pg, true)
			fixture.RunStep(t, p.Ctx, installer, r, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	})
}
