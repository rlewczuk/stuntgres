package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCursorCountPrecedence(t *testing.T) {
	run := func(t *testing.T, scenario func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			scenario(t, p, b, c)
		})
	}
	for _, action := range []string{"FETCH", "MOVE"} {
		motion := func(expression string) string {
			if action == "FETCH" {
				return "FETCH ABSOLUTE " + expression + " FROM c INTO x;"
			}
			return "MOVE FORWARD " + expression + " FROM c;"
		}
		for _, tc := range []struct{ name, declaration, expression string }{
			{"null", "n integer;", "n"},
			{"arithmetic_error", "n integer:=0;", "1/n"},
			{"binding_error", "", "missing_count"},
			{"volatile", "", "nextval('pc_count_seq')"},
		} {
			t.Run(action+"_missing_"+tc.name, func(t *testing.T) {
				run(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					sqlCursorStep(t, p, b, c, fixture.Exec("CREATE SEQUENCE pc_count_seq START 10", 0))
					plCursorFunction(t, p, b, c, "pc_count", "", "void", "DECLARE c refcursor:='pc_absent'; x integer; "+tc.declaration+" BEGIN "+motion(tc.expression)+" END")
					sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
					want := plCursorRuntime("34000", `cursor "pc_absent" does not exist`, "pc_count()", action)
					for _, prepared := range []bool{false, true} {
						sqlCursorStep(t, p, b, c, fixture.Select("SELECT setval('pc_count_seq',10,true) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(10)}}))
						sqlCursorStep(t, p, b, c, fixture.Exec("SAVEPOINT pc_count_error", 0))
						step := fixture.ErrorDiagnostic("SELECT pc_count() AS v", want)
						step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
						sqlCursorStep(t, p, b, c, step)
						sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK TO pc_count_error", 0))
						sqlCursorStep(t, p, b, c, fixture.Exec("RELEASE pc_count_error", 0))
						// Sequence effects survive savepoint rollback: unchanged10
						// proves the volatile expression was never reached.
						plCursorValue(t, p, b, c, "SELECT currval('pc_count_seq') AS v", "INT8", int64(10))
						plCursorValue(t, p, b, c, "SELECT 7 AS v", "INT4", int64(7))
					}
					sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
				})
			})
		}
		t.Run(action+"_live_controls", func(t *testing.T) {
			run(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plCursorRows(t, p, b, c)
				sqlCursorStep(t, p, b, c, fixture.Exec("CREATE SEQUENCE pc_count_seq START 10", 0))
				positiveType, metadata, result := "integer", "INT4", "RETURN x;"
				var value any = int64(10)
				if action == "MOVE" {
					positiveType, metadata, result, value = "boolean", "BOOL", "RETURN FOUND;", true
				}
				plCursorFunction(t, p, b, c, "pc_count_live", "c refcursor", positiveType, "DECLARE x integer; BEGIN "+motion("nextval('pc_count_seq')-10")+" "+result+" END")
				for _, tc := range []struct{ name, declaration, expression, code, message string }{
					{"null", "n integer;", "n", "22004", "relative or absolute cursor position is null"},
					{"error", "n integer:=0;", "1/n", "22012", "division by zero"},
				} {
					plCursorFunction(t, p, b, c, "pc_count_"+tc.name, "c refcursor", "void", "DECLARE x integer; "+tc.declaration+" BEGIN "+motion(tc.expression)+" END")
				}
				sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
				for _, prepared := range []bool{false, true} {
					sqlCursorStep(t, p, b, c, fixture.Select("SELECT setval('pc_count_seq',10,true) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(10)}}))
					sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE pc_live SCROLL CURSOR FOR SELECT i FROM pc_rows ORDER BY i", 0))
					step := fixture.Select("SELECT pc_count_live('pc_live'::refcursor) AS v", []string{"v"}, []string{metadata}, [][]any{{value}})
					step.Prepared = prepared
					sqlCursorStep(t, p, b, c, step)
					plCursorValue(t, p, b, c, "SELECT currval('pc_count_seq') AS v", "INT8", int64(11))
					sqlCursorStep(t, p, b, c, fixture.Select("FETCH NEXT FROM pc_live", []string{"i"}, []string{"INT4"}, [][]any{{int64(20)}}))
					sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE pc_live", 0))
					for _, tc := range []struct{ name, code, message string }{
						{"null", "22004", "relative or absolute cursor position is null"},
						{"error", "22012", "division by zero"},
					} {
						sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE pc_live SCROLL CURSOR FOR SELECT i FROM pc_rows ORDER BY i", 0))
						sqlCursorStep(t, p, b, c, fixture.Exec("SAVEPOINT pc_live_error", 0))
						want := plCursorRuntime(tc.code, tc.message, fmt.Sprintf("pc_count_%s(refcursor)", tc.name), action)
						errorStep := fixture.ErrorDiagnostic("SELECT pc_count_"+tc.name+"('pc_live'::refcursor) AS v", want)
						errorStep.SelectRows, errorStep.Prepared, errorStep.ErrorStage = true, prepared, "execute"
						if prepared && tc.name == "error" {
							// PostgreSQL's cached expression falls back to SPI on
							// this repeated same-transaction route; native's
							// ordinary simple execution remains action-only.
							cached := want
							cached.Context = "PL/pgSQL expression \"1/n\"\n" + want.Context
							errorStep.PostgresDiagnostic = &cached
						}
						sqlCursorStep(t, p, b, c, errorStep)
						sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK TO pc_live_error", 0))
						sqlCursorStep(t, p, b, c, fixture.Exec("RELEASE pc_live_error", 0))
						sqlCursorStep(t, p, b, c, fixture.Select("FETCH NEXT FROM pc_live", []string{"i"}, []string{"INT4"}, [][]any{{int64(10)}}))
						sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE pc_live", 0))
					}
				}
				sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
			})
		})
	}
}
