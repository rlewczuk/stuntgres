package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerAfterFunctionReplacement(t *testing.T) {
	for _, level := range []string{"ROW", "STATEMENT"} {
		t.Run(level, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				id := "NEW.id"
				var first, third any = int64(1), int64(3)
				if level == "STATEMENT" {
					id, first, third = "NULL", nil, nil
				}
				body := func(marker string) string {
					return "BEGIN INSERT INTO aft_replace_log VALUES('" + marker + "',TG_LEVEL,TG_NAME," + id + ",(SELECT count(*) FROM aft_replace)); RETURN NULL; END"
				}
				replace := func(source string) string {
					return "CREATE OR REPLACE FUNCTION aft_replace_z() RETURNS trigger LANGUAGE plpgsql AS $body$" + source + "$body$"
				}
				installer := func(source string) string {
					return "CREATE OR REPLACE FUNCTION aft_replace_a() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN " + replace(source) + "; INSERT INTO aft_replace_log VALUES('installer',TG_LEVEL,TG_NAME," + id + ",(SELECT count(*) FROM aft_replace)); RETURN NULL; END$$"
				}
				userOperatorExec(t, p, b, c,
					"CREATE TABLE aft_replace(id integer PRIMARY KEY)",
					"CREATE TABLE aft_replace_log(marker text,level text,name text,id integer,n bigint)",
					replace(body("obsolete")),
					installer(body("current")),
					"CREATE TRIGGER a AFTER INSERT ON aft_replace FOR EACH " + level + " EXECUTE FUNCTION aft_replace_a()",
					"CREATE TRIGGER z AFTER INSERT ON aft_replace FOR EACH " + level + " EXECUTE FUNCTION aft_replace_z()")
				run := func(s fixture.Step) {
					t.Helper()
					afterStep(t, p, b, c, prepared, s)
				}
				check := func(rows [][]any) {
					t.Helper()
					run(fixture.Select("SELECT marker,level,name,id,n FROM aft_replace_log ORDER BY n,marker DESC", []string{"marker", "level", "name", "id", "n"}, []string{"TEXT", "TEXT", "TEXT", "INT4", "INT8"}, rows))
				}
				run(fixture.Exec("INSERT INTO aft_replace VALUES(1)", 1))
				check([][]any{{"installer", level, "a", first, int64(1)}, {"current", level, "z", first, int64(1)}})

				// The replacement itself, the installer's audit, and all writes in
				// the failing body belong to the rejected outer command.
				failing := "BEGIN INSERT INTO aft_replace_log VALUES('doomed',TG_LEVEL,TG_NAME," + id + ",(SELECT count(*) FROM aft_replace)); PERFORM 1/0; RETURN NULL; END"
				userOperatorExec(t, p, b, c, installer(failing))
				diagnostic := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				diagnostic.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function aft_replace_z() line 1 at PERFORM"
				failure := fixture.ErrorDiagnostic("INSERT INTO aft_replace VALUES(2) RETURNING id", diagnostic)
				failure.SelectRows = true
				run(failure)
				run(fixture.Select("SELECT id FROM aft_replace ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				check([][]any{{"installer", level, "a", first, int64(1)}, {"current", level, "z", first, int64(1)}})

				// Without the installer, the next invocation must still run the
				// successful replacement, not the rolled-back failing definition.
				userOperatorExec(t, p, b, c, "DROP TRIGGER a ON aft_replace")
				run(fixture.Select("INSERT INTO aft_replace VALUES(3) RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
				run(fixture.Select("SELECT id FROM aft_replace ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
				check([][]any{{"installer", level, "a", first, int64(1)}, {"current", level, "z", first, int64(1)}, {"current", level, "z", third, int64(2)}})
			})
		})
	}
}
