package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSQLExecutionCommands(t *testing.T) {
	for _, frame := range []string{"function", "procedure", "do"} {
		t.Run(frame, func(t *testing.T) {
			sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE sx_data(id integer,t text)")
				body := "DECLARE local_text text:='local'; BEGIN CREATE TABLE sx_utility(i integer); SET application_name='sx_command'; INSERT INTO sx_data VALUES(3,local_text); UPDATE sx_data SET t='new' WHERE id=1; DELETE FROM sx_data WHERE id=2; END"
				q := "DO $$" + body + "$$"
				if frame == "function" {
					userOperatorExec(t, p, b, c, "CREATE FUNCTION sx_command() RETURNS integer LANGUAGE plpgsql AS $$DECLARE local_text text:='local'; BEGIN CREATE TABLE sx_utility(i integer); SET application_name='sx_command'; INSERT INTO sx_data VALUES(3,local_text); UPDATE sx_data SET t='new' WHERE id=1; DELETE FROM sx_data WHERE id=2; RETURN 1; END$$")
					q = "SELECT sx_command() AS v"
				} else if frame == "procedure" {
					userOperatorExec(t, p, b, c, "CREATE PROCEDURE sx_command() LANGUAGE plpgsql AS $$"+body+"$$")
					q = "CALL sx_command()"
				}
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sx_data VALUES(1,'old'),(2,'drop')", 2))
					if frame == "function" {
						s := sqlExecutionStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}})
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					} else {
						s := fixture.Exec(q, 0)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					sqlExecutionBoth(t, p, b, c, "SELECT id+0 AS id,t||'' AS t FROM sx_data ORDER BY id", []string{"id", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "new"}, {int64(3), "local"}})
					setting := sqlExecutionStep(b, "SELECT current_setting('application_name')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{"sx_command"}})
					setting.CheckMetadata = sqlExecutionMetadata(b, []string{"current_setting"}, []string{"TEXT"}, true)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, setting)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(setting))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sx_utility VALUES(9)", 1))
					sqlExecutionValue(t, p, b, c, "SELECT i+0 AS v FROM sx_utility", "INT4", int64(9))
					userOperatorExec(t, p, b, c, "DROP TABLE sx_utility")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM sx_data", 2))
				}
				userOperatorExec(t, p, b, c, "RESET application_name")
			})
		})
	}
}
