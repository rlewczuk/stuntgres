package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const diagnosticsFieldDeclarations = "a text; b text; c text; d text; e text; f text; g text; h text; i text; j text; "
const diagnosticsReadFields = "GET STACKED DIAGNOSTICS a=RETURNED_SQLSTATE,b=MESSAGE_TEXT,c=PG_EXCEPTION_DETAIL,d=PG_EXCEPTION_HINT,e=PG_EXCEPTION_CONTEXT,f=SCHEMA_NAME,g=TABLE_NAME,h=COLUMN_NAME,i=PG_DATATYPE_NAME,j=CONSTRAINT_NAME; "
const diagnosticsJoinFields = "a || '|' || b || '|' || c || '|' || d || '|' || e || '|' || f || '|' || g || '|' || h || '|' || i || '|' || j"

func TestReferencePLpgSQLDiagnosticsStackedFields(t *testing.T) {
	for _, tc := range []struct{ name, setup, action, want string }{
		{"explicit_all_ten", "", "RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='caught diagnostics',DETAIL='diagnostic detail',HINT='diagnostic hint',SCHEMA='diag_schema',TABLE='diag_table',COLUMN='diag_column',DATATYPE='diag_type',CONSTRAINT='diag_constraint';", "P0001|caught diagnostics|diagnostic detail|diagnostic hint|PL/pgSQL function dg_fields() line 1 at RAISE|diag_schema|diag_table|diag_column|diag_type|diag_constraint"},
		{"division_empty_not_null", "", "PERFORM 10/z;", "22012|division by zero|||SQL statement \"SELECT 10/z\"\nPL/pgSQL function dg_fields() line 1 at PERFORM|||||"},
		{"unique_names", "CREATE TABLE dg_unique(i integer CONSTRAINT dg_unique_key UNIQUE)", "INSERT INTO dg_unique VALUES(1);", "23505|duplicate key value violates unique constraint \"dg_unique_key\"|Key (i)=(1) already exists.||SQL statement \"INSERT INTO dg_unique VALUES(1)\"\nPL/pgSQL function dg_fields() line 1 at SQL statement|public|dg_unique|||dg_unique_key"},
		{"not_null_names", "CREATE TABLE dg_notnull(i integer NOT NULL)", "INSERT INTO dg_notnull VALUES(NULL);", "23502|null value in column \"i\" of relation \"dg_notnull\" violates not-null constraint|Failing row contains (null).||SQL statement \"INSERT INTO dg_notnull VALUES(NULL)\"\nPL/pgSQL function dg_fields() line 1 at SQL statement|public|dg_notnull|i||"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if tc.setup != "" {
					userOperatorExec(t, p, b, c, tc.setup)
				}
				if tc.name == "unique_names" {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dg_unique VALUES(1)", 1))
				}
				userOperatorExec(t, p, b, c, "CREATE FUNCTION dg_fields() RETURNS text LANGUAGE plpgsql AS $$DECLARE "+diagnosticsFieldDeclarations+"z integer:=0; BEGIN "+tc.action+" RETURN 'not caught'; EXCEPTION WHEN OTHERS THEN "+diagnosticsReadFields+" PERFORM 1; b:='overwritten local'; "+diagnosticsReadFields+" RETURN "+diagnosticsJoinFields+"; END$$")
				sqlExecutionValue(t, p, b, c, "SELECT dg_fields() AS v", "TEXT", tc.want)
			})
		})
	}
	t.Run("ordered_destinations", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION dg_order() RETURNS text LANGUAGE plpgsql AS $$DECLARE x text; BEGIN RAISE EXCEPTION USING MESSAGE='first',DETAIL='second'; EXCEPTION WHEN OTHERS THEN GET STACKED DIAGNOSTICS x=MESSAGE_TEXT,x=PG_EXCEPTION_DETAIL; RETURN x; END$$")
			sqlExecutionValue(t, p, b, c, "SELECT dg_order() AS v", "TEXT", "second")
		})
	})
}

func TestReferencePLpgSQLDiagnosticsHandlerLifetime(t *testing.T) {
	t.Run("nested_restore_and_child_isolation", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION dg_without_handler() RETURNS text LANGUAGE plpgsql AS $$DECLARE x text; BEGIN GET STACKED DIAGNOSTICS x=MESSAGE_TEXT; RETURN x; END$$",
				"CREATE FUNCTION dg_handlers() RETURNS text LANGUAGE plpgsql AS $$DECLARE a text; b text; c text; d text; z integer:=0; BEGIN BEGIN PERFORM 1/z; EXCEPTION WHEN division_by_zero THEN GET STACKED DIAGNOSTICS a=RETURNED_SQLSTATE; BEGIN z:='bad'::text; EXCEPTION WHEN invalid_text_representation THEN GET STACKED DIAGNOSTICS b=RETURNED_SQLSTATE; END; GET STACKED DIAGNOSTICS c=RETURNED_SQLSTATE; BEGIN PERFORM dg_without_handler(); EXCEPTION WHEN SQLSTATE '0Z002' THEN GET STACKED DIAGNOSTICS d=RETURNED_SQLSTATE; END; GET STACKED DIAGNOSTICS a=MESSAGE_TEXT; END; RETURN a || '|' || b || '|' || c || '|' || d; END$$")
			sqlExecutionValue(t, p, b, c, "SELECT dg_handlers() AS v", "TEXT", "division by zero|22P02|22012|0Z002")
		})
	})
	t.Run("rollback_before_handler", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE dg_marks(i integer)",
				"CREATE FUNCTION dg_rollback() RETURNS text LANGUAGE plpgsql AS $$DECLARE n bigint; f boolean; code text; seen bigint; BEGIN GET DIAGNOSTICS n=ROW_COUNT; f:=found; INSERT INTO dg_marks VALUES(1); BEGIN INSERT INTO dg_marks VALUES(99); RAISE EXCEPTION 'rollback diagnostics'; EXCEPTION WHEN OTHERS THEN SELECT count(*) INTO seen FROM dg_marks WHERE i=99; GET STACKED DIAGNOSTICS code=RETURNED_SQLSTATE; INSERT INTO dg_marks VALUES(3); END; RETURN n::text || ':' || f::text || '|' || seen::text || '|' || code; END$$")
			for _, prepared := range []bool{false, true} {
				s := sqlExecutionStep(b, "SELECT dg_rollback() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"0:false|0|P0001"}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				sqlExecutionBoth(t, p, b, c, "SELECT i+0 AS v FROM dg_marks ORDER BY i", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM dg_marks", 2))
			}
		})
	})
	t.Run("rethrow_full_record", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION dg_rethrow() RETURNS text LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION USING ERRCODE='P0001',MESSAGE='caught diagnostics',DETAIL='diagnostic detail',HINT='diagnostic hint',SCHEMA='diag_schema',TABLE='diag_table',COLUMN='diag_column',DATATYPE='diag_type',CONSTRAINT='diag_constraint'; EXCEPTION WHEN OTHERS THEN PERFORM 1; RAISE; END$$")
			want := plpgsqlRuntimeDiagnostic("P0001", "caught diagnostics", "dg_rethrow()", "RAISE")
			want.Detail, want.Hint = "diagnostic detail", "diagnostic hint"
			want.Schema, want.Table, want.Column, want.DataType, want.Constraint = "diag_schema", "diag_table", "diag_column", "diag_type", "diag_constraint"
			plpgsqlError(t, p, c, "SELECT dg_rethrow() AS v", true, want)
		})
	})
	t.Run("escaping_handler_and_fresh_invocation", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE dg_escape_marks(i integer)", "CREATE FUNCTION dg_escape(fail boolean) RETURNS text LANGUAGE plpgsql AS $$DECLARE n bigint; f boolean; x text; BEGIN GET DIAGNOSTICS n=ROW_COUNT; f:=found; IF fail THEN BEGIN INSERT INTO dg_escape_marks VALUES(99); RAISE EXCEPTION 'first'; EXCEPTION WHEN OTHERS THEN INSERT INTO dg_escape_marks VALUES(98); RAISE EXCEPTION 'handler escaped'; END; END IF; BEGIN GET STACKED DIAGNOSTICS x=MESSAGE_TEXT; EXCEPTION WHEN SQLSTATE '0Z002' THEN x:='no active error'; END; RETURN n::text || ':' || f::text || '|' || x; END$$")
			plpgsqlError(t, p, c, "SELECT dg_escape(true) AS v", true, plpgsqlRuntimeDiagnostic("P0001", "handler escaped", "dg_escape(boolean)", "RAISE"))
			sqlExecutionCount(t, p, b, c, "dg_escape_marks", 0)
			sqlExecutionValue(t, p, b, c, "SELECT dg_escape(false) AS v", "TEXT", "0:false|no active error")
		})
	})
}
