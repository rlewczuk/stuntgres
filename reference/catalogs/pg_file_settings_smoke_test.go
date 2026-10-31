package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGFileSettingsSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgfilesettings_actor"})
	pgFileSettingsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgFileSettingsPrivileges(t, p, b)
		for _, prepared := range []bool{false, true} {
			pgFileSettingsRead(t, p, b, b.DB)
			pgFileSettingsSource(t, p, b, b.DB, prepared)
			s := fixture.ImplicitError("SELECT source FROM pg_catalog.pg_file_settings", fixture.Diagnostic{Code: "42703", Message: `column "source" does not exist`, Position: "8"})
			s.Prepared, s.SelectRows = prepared, true
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			s = fixture.ImplicitError("UPDATE pg_catalog.pg_file_settings SET setting='x'", fixture.Diagnostic{Code: "55000", Message: `cannot update view "pg_file_settings"`, Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable updating the view, provide an INSTEAD OF UPDATE trigger or an unconditional ON UPDATE DO INSTEAD rule."})
			s.Prepared = prepared
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			pgFileSettingsSentinel(t, p, b, b.DB)
		}
	})
}
