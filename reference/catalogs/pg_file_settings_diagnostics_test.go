package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGFileSettingsDiagnostics(t *testing.T) {
	pgFileSettingsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE pgfilesettings_sentinel(n integer)", 0))
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO pgfilesettings_sentinel VALUES(42)", 1))
		cases := []struct{ query, code, message, position string }{
			{"SELECT source FROM pg_catalog.pg_file_settings", "42703", `column "source" does not exist`, "8"},
			{"SELECT missing FROM pg_catalog.pg_file_settings", "42703", `column "missing" does not exist`, "8"},
			{"SELECT missing FROM pg_catalog.pg_file_settings WHERE FALSE", "42703", `column "missing" does not exist`, "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_file_settings", "42703", `column "missing" does not exist`, "14"},
			{"SELECT p.missing FROM pg_catalog.pg_file_settings p", "42703", "column p.missing does not exist", "8"},
			{"SELECT * FROM pg_catalog.pg_file_settings_missing", "42P01", `relation "pg_catalog.pg_file_settings_missing" does not exist`, "15"},
			{"SELECT * FROM missing_schema.pg_file_settings", "42P01", `relation "missing_schema.pg_file_settings" does not exist`, "15"},
			{`SELECT * FROM pg_catalog."PG_FILE_SETTINGS"`, "42P01", `relation "pg_catalog.PG_FILE_SETTINGS" does not exist`, "15"},
			{"SELECT x.name FROM pg_catalog.pg_file_settings p", "42P01", `missing FROM-clause entry for table "x"`, "8"},
		}
		for _, tc := range cases {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.query+map[bool]string{false: "/direct", true: "/prepare"}[prepared], func(t *testing.T) {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.SelectRows = true
					s.Prepared = prepared
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
					pgFileSettingsRead(t, p, b, conn)
					pgFileSettingsSentinel(t, p, b, conn)
					pgFileSettingsInitialACL(t, p, b, conn, true)
				})
			}
		}
	})
}
