package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAvailableExtensionsFeatureSmoke(t *testing.T) {
	availableExtensionsCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		availableExtensionsState(t, p, c, r, pg, false)
		a, err := c.PrepareContext(p.Ctx, "SELECT * FROM pg_available_extensions WHERE name=$1")
		if err != nil { t.Fatal(err) }
		defer a.Close()
		v, err := c.PrepareContext(p.Ctx, "SELECT * FROM pg_available_extension_versions WHERE name=$1")
		if err != nil { t.Fatal(err) }
		defer v.Close()
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent", 0))
		availableExtensionsHeld(t, p, a, false, pg, availableExtensionsRow("1.1"), "unaccent")
		availableExtensionsHeld(t, p, v, true, pg, availableVersionsRow(true), "unaccent")
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK", 0))
		availableExtensionsHeld(t, p, a, false, pg, availableExtensionsRow(nil), "unaccent")
		availableExtensionsHeld(t, p, v, true, pg, availableVersionsRow(false), "unaccent")
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SCHEMA ext_addon", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
		availableExtensionsState(t, p, c, r, pg, true)
		availableExtensionsHeld(t, p, a, false, pg, availableExtensionsRow("1.1"), "unaccent")
		availableExtensionsHeld(t, p, v, true, pg, availableVersionsRow(true), "unaccent")
		availableExtensionsHeld(t, p, a, false, pg, [][]any{}, "available_absent")
		for _, version := range []string{"1.0", "available-unknown"} {
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions WHERE name='unaccent' AND version=$1", true, [][]any{}, pg, version))
		}
		availableExtensionsBoth(t, p, c, r, fixture.Select("SELECT ext_addon.unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
		s := fixture.ErrorDiagnostic("SELECT missing FROM pg_available_extensions WHERE FALSE", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "missing" does not exist`, Position: "8"})
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, c, r, s)
		s.Prepared, s.ErrorStage = true, "prepare"
		fixture.RunStep(t, p.Ctx, c, r, s)
	})
}
