package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAvailableExtensionsControls(t *testing.T) {
	availableExtensionsCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		// The transaction control is genuine on both backends. Native Dump must
		// exclude its private installation while retaining package availability.
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent", 0))
		availableExtensionsState(t, p, c, r, pg, true)
		var committed bytes.Buffer
		if !pg {
			if err := stuntgres.Dump(p.Ctx, p.Native, &committed); err != nil { t.Fatal(err) }
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK", 0))
		availableExtensionsState(t, p, c, r, pg, false)
		if !pg {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(committed.String())); err != nil { t.Fatal(err) }
			availableExtensionsState(t, p, c, r, false, false)
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SCHEMA ext_addon", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
		availableExtensionsState(t, p, c, r, pg, true)
		call := fixture.Select("SELECT ext_addon.unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}})
		availableExtensionsBoth(t, p, c, r, call)
		if pg { return } // Reset/Restore are public native APIs, not pq operations.
		old, err := p.Native.QueryContext(p.Ctx, "SELECT * FROM pg_available_extensions WHERE name='unaccent'")
		if err != nil { t.Fatal(err) }
		defer old.Close()
		availableExtensionsMetadata(t, old, availableExtensionsColumns, availableExtensionsTypes, false)
		oldVersions, err := p.Native.QueryContext(p.Ctx, "SELECT * FROM pg_available_extension_versions WHERE name='unaccent'")
		if err != nil { t.Fatal(err) }
		defer oldVersions.Close()
		availableExtensionsMetadata(t, oldVersions, availableVersionsColumns, availableVersionsTypes, false)
		var snapshot bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil { t.Fatal(err) }
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		availableExtensionsState(t, p, c, r, false, false)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(snapshot.String())); err != nil { t.Fatal(err) }
		availableExtensionsState(t, p, c, r, false, true)
		availableExtensionsBoth(t, p, c, r, call)
		availableExtensionsRetained(t, old, false, availableExtensionsRow("1.1"))
		availableExtensionsMetadata(t, oldVersions, availableVersionsColumns, availableVersionsTypes, false)
		if !oldVersions.Next() { t.Fatal("retained versions missing", oldVersions.Err()) }
		var name, version, comment string
		var installed, superuser, trusted, relocatable bool
		var schema, requires any
		if err := oldVersions.Scan(&name, &version, &installed, &superuser, &trusted, &relocatable, &schema, &requires, &comment); err != nil { t.Fatal(err) }
		if name != "unaccent" || version != "1.1" || !installed || !superuser || !trusted || !relocatable || schema != nil || requires != nil || comment != availableExtensionsComment || oldVersions.Next() || oldVersions.Err() != nil { t.Fatal("retained versions changed") }
		connector, err := (&stuntgres.Driver{}).OpenConnector("available-extension-controls-independent")
		if err != nil { t.Fatal(err) }
		independent := sql.OpenDB(connector)
		defer independent.Close()
		checkIndependent := func(installed bool) {
			var version any
			if installed { version = "1.1" }
			for i, view := range []string{"pg_available_extensions", "pg_available_extension_versions"} {
				stmt, err := independent.PrepareContext(p.Ctx, "SELECT * FROM "+view+" WHERE name=$1")
				if err != nil { t.Fatal(err) }
				defer stmt.Close()
				want := availableExtensionsRow(version)
				if i == 1 { want = availableVersionsRow(installed) }
				availableExtensionsHeld(t, p, stmt, i == 1, false, want, "unaccent")
				stmt.Close()
			}
		}
		checkIndependent(false)
		if err := stuntgres.Restore(p.Ctx, independent, strings.NewReader(snapshot.String())); err != nil { t.Fatal(err) }
		checkIndependent(true)
		var result string
		if err := independent.QueryRowContext(p.Ctx, "SELECT ext_addon.unaccent('Hôtel')").Scan(&result); err != nil || result != "Hotel" { t.Fatal("independent provider", result, err) }
	})
}
