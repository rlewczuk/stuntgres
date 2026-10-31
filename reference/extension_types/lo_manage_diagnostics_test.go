package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLoManageDiagnostics(t *testing.T) {
	t.Run("installer_collision", func(t *testing.T) {
		lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			loRun(t, p, b, c, fixture.Exec("CREATE FUNCTION lo_manage() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$", 0))
			d := fixture.ControlsReferenceDiagnostic("42723", `function "lo_manage" already exists with same argument types`)
			// Source-derived discovery candidate; root records the oracle's full Context.
			d.Context = "SQL statement \"CREATE FUNCTION lo_manage()\nRETURNS pg_catalog.trigger\nAS '$libdir/lo'\nLANGUAGE C\"\nextension script file \"lo--1.1.sql\", near line 22"
			lmRun(t, p, b, c, prepared, fixture.ErrorDiagnostic("CREATE EXTENSION lo", d))
			loAbsent(t, p, b, c)
			loRun(t, p, b, c, fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='lo_manage'", []string{"proname"}, []string{"NAME"}, [][]any{{"lo_manage"}}))
		})
	})
	t.Run("scalar", func(t *testing.T) {
		lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			loInstall(t, p, b, c)
			s := lmError("SELECT lo_manage()", "XX000", "lo_manage: not fired by trigger manager")
			s.SelectRows = true
			lmRun(t, p, b, c, prepared, s)
		})
	})
	for _, tc := range []struct{ name, args, level, message string }{
		{"statement_precedence", "", "STATEMENT", "lm_cleanup: must be fired for row"},
		{"no_args", "", "ROW", "lm_cleanup: no column name provided in the trigger definition"},
		{"missing", "'missing'", "ROW", `lm_cleanup: column "missing" does not exist`},
		{"system", "'tableoid'", "ROW", `lm_cleanup: column "tableoid" does not exist`},
		{"case", "'V'", "ROW", `lm_cleanup: column "V" does not exist`},
		{"null_token", "NULL", "ROW", `lm_cleanup: column "null" does not exist`},
		{"quoted_null", "'NULL'", "ROW", `lm_cleanup: column "NULL" does not exist`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				lmSetup(t, p, b, c, "lo", tc.args, "UPDATE OR DELETE", tc.level)
				if tc.level == "ROW" {
					lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=NULL WHERE id=99", 0))
					lmRun(t, p, b, c, prepared, fixture.Exec("DELETE FROM lm WHERE id=99", 0))
				}
				lmRun(t, p, b, c, prepared, lmError("UPDATE lm SET v=NULL", "XX000", tc.message))
				lmRows(t, p, b, c, lmRow(int64(910201), 0))
				lmBytes(t, p, b, c, 910201, "00ff")
				lmRun(t, p, b, c, prepared, lmError("DELETE FROM lm", "XX000", tc.message))
				// Invalid lookup still fails when OLD is NULL.
				loRun(t, p, b, c, fixture.Exec("DROP TRIGGER lm_cleanup ON lm", 0))
				loRun(t, p, b, c, fixture.Exec("UPDATE lm SET v=NULL", 1))
				loRun(t, p, b, c, fixture.Exec("CREATE TRIGGER lm_cleanup BEFORE DELETE ON lm FOR EACH "+tc.level+" EXECUTE FUNCTION lo_manage("+tc.args+")", 0))
				lmRun(t, p, b, c, prepared, lmError("DELETE FROM lm", "XX000", tc.message))
				lmRows(t, p, b, c, lmRow(nil, 0))
			})
		})
	}
}
