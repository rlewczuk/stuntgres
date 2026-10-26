package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSQLExecutionDestinationErrors(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		selectHint    bool
	}{
		{"select", "SELECT 1", true}, {"select_zero", "SELECT 1 WHERE false", true},
		{"insert", "INSERT INTO sx_returning VALUES(1) RETURNING i", false},
		{"insert_zero", "INSERT INTO sx_returning SELECT 1 WHERE false RETURNING i", false},
		{"update", "UPDATE sx_returning SET i=2 RETURNING i", false},
		{"update_zero", "UPDATE sx_returning SET i=2 WHERE false RETURNING i", false},
		{"delete", "DELETE FROM sx_returning RETURNING i", false},
		{"delete_zero", "DELETE FROM sx_returning WHERE false RETURNING i", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE sx_returning(i integer)", "CREATE TABLE sx_prefix(i integer)",
					"CREATE FUNCTION sx_no_destination() RETURNS integer LANGUAGE plpgsql AS $$BEGIN INSERT INTO sx_prefix VALUES(9); "+tc.command+"; RETURN 1; END$$")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sx_returning VALUES(7)", 1))
				want := plpgsqlRuntimeDiagnostic("42601", "query has no destination for result data", "sx_no_destination()", "SQL statement")
				if tc.selectHint {
					want.Hint = "If you want to discard the results of a SELECT, use PERFORM instead."
				}
				plpgsqlError(t, p, c, "SELECT sx_no_destination() AS v", true, want)
				sqlExecutionCount(t, p, b, c, "sx_prefix", 0)
				sqlExecutionValue(t, p, b, c, "SELECT i+0 AS v FROM sx_returning", "INT4", int64(7))
			})
		})
	}
	t.Run("late_projection_precedes_policy", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE sx_prefix(i integer)",
				"CREATE FUNCTION sx_late() RETURNS integer LANGUAGE plpgsql AS $$BEGIN INSERT INTO sx_prefix VALUES(9); SELECT 10/(3-g) FROM generate_series(1,3) AS g; RETURN 1; END$$")
			want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "sx_late()", "SQL statement")
			want.Context = "SQL statement \"SELECT 10/(3-g) FROM generate_series(1,3) AS g\"\n" + want.Context
			plpgsqlError(t, p, c, "SELECT sx_late() AS v", true, want)
			sqlExecutionCount(t, p, b, c, "sx_prefix", 0)
		})
	})
	t.Run("drain_and_caught_database_suffix", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE SEQUENCE sx_bare_seq", "CREATE TABLE sx_suffix(i integer)",
				"CREATE FUNCTION sx_bare() RETURNS integer LANGUAGE plpgsql AS $$BEGIN INSERT INTO sx_suffix VALUES(9); SELECT nextval('sx_bare_seq') FROM generate_series(1,3); RETURN 1; END$$",
				"CREATE FUNCTION sx_catch() RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer:=10; f boolean; BEGIN INSERT INTO sx_suffix VALUES(8); BEGIN INSERT INTO sx_suffix VALUES(9); x:=7; SELECT 1; EXCEPTION WHEN syntax_error THEN f:=FOUND; END; RETURN x||':'||f; END$$")
			want := plpgsqlRuntimeDiagnostic("42601", "query has no destination for result data", "sx_bare()", "SQL statement")
			want.Hint = "If you want to discard the results of a SELECT, use PERFORM instead."
			for _, prepared := range []bool{false, true} {
				userOperatorExec(t, p, b, c, "ALTER SEQUENCE sx_bare_seq RESTART WITH 1")
				s := fixture.ErrorDiagnostic("SELECT sx_bare() AS v", want)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				sqlExecutionValue(t, p, b, c, "SELECT currval('sx_bare_seq') AS v", "INT8", int64(3))
				sqlExecutionCount(t, p, b, c, "sx_suffix", 0)
				success := sqlExecutionStep(b, "SELECT sx_catch() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"7:true"}})
				success.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, success)
				sqlExecutionValue(t, p, b, c, "SELECT i+0 AS v FROM sx_suffix", "INT4", int64(8))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM sx_suffix", 1))
			}
		})
	})
	t.Run("utf8_perform_query_coordinates", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION sx_utf8() RETURNS integer LANGUAGE plpgsql AS $$DECLARE note text:='é'; BEGIN PERFORM 'é', sx_absent; RETURN 1; END$$")
			want := plpgsqlRuntimeDiagnostic("42703", `column "sx_absent" does not exist`, "sx_utf8()", "PERFORM")
			want.InternalQuery, want.InternalPosition = "SELECT 'é', sx_absent", "13"
			plpgsqlError(t, p, c, "SELECT sx_utf8() AS v", true, want)
		})
	})
}
