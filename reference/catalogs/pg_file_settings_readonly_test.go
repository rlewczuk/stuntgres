package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGFileSettingsReadOnly(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			pgSequenceExec(t, p, b, b.DB, "CREATE TABLE pgfilesettings_sentinel(n integer)")
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgfilesettings_sentinel VALUES(42)", 1))
			for _, tc := range []struct{ query, verb, message string }{
				{"INSERT INTO pg_catalog.pg_file_settings DEFAULT VALUES", "INSERT", `cannot insert into view "pg_file_settings"`},
				{"UPDATE pg_catalog.pg_file_settings SET setting='x'", "UPDATE", `cannot update view "pg_file_settings"`},
				{"DELETE FROM pg_catalog.pg_file_settings", "DELETE", `cannot delete from view "pg_file_settings"`},
				{"TRUNCATE pg_catalog.pg_file_settings", "TRUNCATE", `"pg_file_settings" is not a table`},
			} {
				want := fixture.Diagnostic{Code: "55000", Message: tc.message, Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[tc.verb] + " the view, provide an INSTEAD OF " + tc.verb + " trigger or an unconditional ON " + tc.verb + " DO INSTEAD rule."}
				if tc.verb == "TRUNCATE" {
					want = fixture.Diagnostic{Code: "42809", Message: tc.message}
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared && tc.verb != "TRUNCATE" {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					pgFileSettingsRead(t, p, b, b.DB)
					pgFileSettingsSentinel(t, p, b, b.DB)
					pgFileSettingsInitialACL(t, p, b, b.DB, true)
				}
				if strings.Contains(tc.query, "TRUNCATE") {
					pgFileSettingsSource(t, p, b, b.DB, false)
				}
			}
		})
	}
}
