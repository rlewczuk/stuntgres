package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func diagnosticsDynamicStatus(t *testing.T) {
	const many = "SELECT i FROM (VALUES(1),(2),(3),(4)) q(i) ORDER BY i"
	const priorTrue = "PERFORM 1 FROM (VALUES(1),(2),(3)) q(i);"
	const priorFalse = "PERFORM 1 WHERE false;"
	for _, tc := range []struct{ name, setup, body, want string }{
		{"dynamic_zero_preserves_found", "", priorTrue + "EXECUTE 'SELECT 1 WHERE false';" + diagnosticsSnapshot, "0:true;"},
		{"dynamic_many_preserves_found", "", priorFalse + "EXECUTE '" + many + "';" + diagnosticsSnapshot, "4:false;"},
		{"dynamic_into_complete", "", priorFalse + "EXECUTE '" + many + "' INTO x;" + diagnosticsSnapshot + "s:=s || x::text;", "4:false;1"},
		{"dynamic_dml_complete", "CREATE TABLE dg_dyn_data(i integer)", priorFalse + "EXECUTE 'INSERT INTO dg_dyn_data VALUES(1),(2),(3),(4)';" + diagnosticsSnapshot + "EXECUTE 'UPDATE dg_dyn_data SET i=i+10 WHERE i<4';" + diagnosticsSnapshot + "EXECUTE 'DELETE FROM dg_dyn_data WHERE i=99';" + diagnosticsSnapshot + "EXECUTE 'DELETE FROM dg_dyn_data';" + diagnosticsSnapshot, "4:false;3:false;0:false;4:false;"},
		{"dynamic_returning_complete", "CREATE TABLE dg_dyn_data(i integer)", "INSERT INTO dg_dyn_data VALUES(1),(2),(3),(4);" + priorFalse + "EXECUTE 'UPDATE dg_dyn_data SET i=i+10 RETURNING i' INTO x;" + diagnosticsSnapshot + "IF x<11 OR x>14 THEN RAISE EXCEPTION 'invalid returned target'; END IF; DELETE FROM dg_dyn_data;", "4:false;"},
		{"dynamic_multiple_final", "", priorFalse + "EXECUTE 'SELECT 1; " + many + "';" + diagnosticsSnapshot, "4:false;"},
		{"dynamic_multiple_empty_final", "", priorFalse + "EXECUTE '" + many + "; SELECT 1 WHERE false';" + diagnosticsSnapshot, "0:false;"},
		{"dynamic_empty_utility", "", priorTrue + "EXECUTE '';" + diagnosticsSnapshot + "EXECUTE 'SET application_name=''diagnostics''';" + diagnosticsSnapshot, "0:true;0:true;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if tc.setup != "" { userOperatorExec(t, p, b, c, tc.setup) }
				diagnosticsTrace(t, p, b, c, tc.body, tc.want)
				if tc.setup != "" { sqlExecutionCount(t, p, b, c, "dg_dyn_data", 0) }
			})
		})
	}
	for _, tc := range []struct{ name, prior, command, sql, condition, want, code, message string }{
		{"dynamic_strict_many", priorFalse, "EXECUTE '" + many + "' INTO STRICT x;", "", "too_many_rows", "4:false;99", "P0003", "query returned more than one row"},
		{"dynamic_strict_zero", priorTrue, "EXECUTE 'SELECT 1 WHERE false' INTO STRICT x;", "", "no_data_found", "0:true;99", "P0002", "query returned no rows"},
		{"dynamic_conversion", priorFalse, "EXECUTE 'SELECT ''bad''::text FROM (VALUES(1),(2),(3),(4)) q(i)' INTO x;", "", "invalid_text_representation", "4:false;99", "22P02", "invalid input syntax for type integer: \"bad\""},
		{"dynamic_no_descriptor", priorFalse, "EXECUTE 'INSERT INTO dg_dyn_marks VALUES(1),(2),(3),(4)' INTO x;", "", "syntax_error", "4:false;99", "42601", "INTO used with a command that cannot return data"},
		{"dynamic_execution_failure", priorTrue, "EXECUTE 'SELECT 10/i FROM (VALUES(1),(0),(2)) q(i)' INTO x;", "SELECT 10/i FROM (VALUES(1),(0),(2)) q(i)", "division_by_zero", "3:true;99", "22012", "division by zero"},
		{"dynamic_multiple_failure", priorTrue, "EXECUTE '" + many + "; SELECT 1/0';", many + "; SELECT 1/0", "division_by_zero", "3:true;99", "22012", "division by zero"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE dg_dyn_marks(i integer)",
					"CREATE FUNCTION dg_dyn_caught() RETURNS text LANGUAGE plpgsql AS $$DECLARE n bigint; x integer:=99; s text:=''; BEGIN INSERT INTO dg_dyn_marks VALUES(8); BEGIN INSERT INTO dg_dyn_marks VALUES(9); " + tc.prior + tc.command + " RAISE EXCEPTION 'expected failure'; EXCEPTION WHEN " + tc.condition + " THEN " + diagnosticsSnapshot + "s:=s || x::text; END; RETURN s; END$$",
					"CREATE FUNCTION dg_dyn_failed() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer:=99; BEGIN INSERT INTO dg_dyn_marks VALUES(9); " + tc.prior + tc.command + " RETURN x; END$$")
				d := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "dg_dyn_failed()", "EXECUTE")
				if tc.sql != "" { d.Context = "SQL statement \"" + tc.sql + "\"\n" + d.Context }
				for _, prepared := range []bool{false, true} {
					s := sqlExecutionStep(b, "SELECT dg_dyn_caught() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					sqlExecutionValue(t, p, b, c, "SELECT i+0 AS v FROM dg_dyn_marks", "INT4", int64(8))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM dg_dyn_marks", 1))
					failure := fixture.ErrorDiagnostic("SELECT dg_dyn_failed() AS v", d)
					failure.SelectRows, failure.Prepared, failure.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, failure)
					sqlExecutionCount(t, p, b, c, "dg_dyn_marks", 0)
				}
			})
		})
	}
	t.Run("dynamic_return_query_status", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE dg_dyn_trace(s text)",
				"CREATE FUNCTION dg_dyn_set() RETURNS SETOF integer LANGUAGE plpgsql AS $$DECLARE n bigint; s text:=''; BEGIN PERFORM 1 WHERE false; RETURN QUERY EXECUTE '" + many + "';" + diagnosticsSnapshot + "RETURN QUERY EXECUTE 'SELECT 8';" + diagnosticsSnapshot + "RETURN QUERY EXECUTE 'SELECT 9 WHERE false';" + diagnosticsSnapshot + "INSERT INTO dg_dyn_trace VALUES(s); END$$")
			for _, prepared := range []bool{false, true} {
				s := sqlExecutionStep(b, "SELECT dg_dyn_set() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(8)}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				diagnosticsDynamicTraceValue(t, p, b, c, "4:true;1:true;0:false;")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM dg_dyn_trace", 1))
			}
		})
	})
	t.Run("dynamic_return_query_multiple_delta", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE dg_dyn_trace(s text)",
				"CREATE FUNCTION dg_dyn_set() RETURNS SETOF integer LANGUAGE plpgsql AS $$DECLARE n bigint; s text:=''; BEGIN RETURN QUERY EXECUTE 'SELECT 8'; RETURN QUERY EXECUTE 'SELECT 1; "+many+"';"+diagnosticsSnapshot+"INSERT INTO dg_dyn_trace VALUES(s); END$$")
			for _, prepared := range []bool{false, true} {
				s := sqlExecutionStep(b, "SELECT dg_dyn_set() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				diagnosticsDynamicTraceValue(t, p, b, c, "5:true;")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM dg_dyn_trace", 1))
			}
		})
	})
	for _, tc := range []struct{ name, command, condition, code, message, detail string }{
		{"dynamic_return_query_execution_failure", "SELECT 10/i FROM (VALUES(1),(0),(2)) q(i)", "division_by_zero", "22012", "division by zero", ""},
		{"dynamic_return_query_shape_failure", "SELECT 1::bigint", "datatype_mismatch", "42804", "structure of query does not match function result type", "Returned type bigint does not match expected type integer in column \"\" (position 1)."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE dg_dyn_trace(s text)",
					"CREATE FUNCTION dg_dyn_set_caught() RETURNS SETOF integer LANGUAGE plpgsql AS $$DECLARE n bigint; s text:=''; BEGIN "+priorTrue+" BEGIN RETURN QUERY EXECUTE '"+tc.command+"'; RAISE EXCEPTION 'expected failure'; EXCEPTION WHEN "+tc.condition+" THEN "+diagnosticsSnapshot+" END; INSERT INTO dg_dyn_trace VALUES(s); END$$",
					"CREATE FUNCTION dg_dyn_set_failed() RETURNS SETOF integer LANGUAGE plpgsql AS $$BEGIN "+priorTrue+" RETURN QUERY EXECUTE '"+tc.command+"'; END$$")
				d := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "dg_dyn_set_failed()", "RETURN QUERY")
				d.Detail = tc.detail
				d.Context = "SQL statement \""+tc.command+"\"\n"+d.Context
				for _, prepared := range []bool{false, true} {
					// Tuple-store rows appended before an error are not rolled back
					// by the exception block; status still describes prior PERFORM.
					wantRows := [][]any{}
					if tc.code == "22012" { wantRows = [][]any{{int64(10)}} }
					s := sqlExecutionStep(b, "SELECT dg_dyn_set_caught() AS v", []string{"v"}, []string{"INT4"}, wantRows)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					diagnosticsDynamicTraceValue(t, p, b, c, "3:true;")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM dg_dyn_trace", 1))
					failure := fixture.ErrorDiagnostic("SELECT dg_dyn_set_failed() AS v", d)
					failure.SelectRows, failure.Prepared, failure.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, failure)
				}
			})
		})
	}
}

func diagnosticsDynamicTraceValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, want string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := sqlExecutionStep(b, "SELECT s AS v FROM dg_dyn_trace", []string{"v"}, []string{"TEXT"}, [][]any{{want}})
		s.Prepared = prepared
		s.CheckMetadata = sqlExecutionMetadata(b, []string{"v"}, []string{"TEXT"}, true)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}
