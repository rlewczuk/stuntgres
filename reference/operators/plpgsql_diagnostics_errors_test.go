package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLDiagnosticsErrors(t *testing.T) {
	for _, tc := range []struct{ name, declaration, command, code, message, token string }{
		{"unknown_item", "x text;", "GET DIAGNOSTICS x=bogus;", "42601", `unrecognized GET DIAGNOSTICS item at or near "bogus"`, "bogus"},
		{"current_exception", "x text;", "GET CURRENT DIAGNOSTICS x=MESSAGE_TEXT;", "42601", "diagnostics item MESSAGE_TEXT is not allowed in GET CURRENT DIAGNOSTICS", "GET CURRENT"},
		{"stacked_count", "x bigint;", "GET STACKED DIAGNOSTICS x=ROW_COUNT;", "42601", "diagnostics item ROW_COUNT is not allowed in GET STACKED DIAGNOSTICS", "GET STACKED"},
		{"stacked_oid", "x oid;", "GET STACKED DIAGNOSTICS x=PG_ROUTINE_OID;", "42601", "diagnostics item PG_ROUTINE_OID is not allowed in GET STACKED DIAGNOSTICS", "GET STACKED"},
		{"unknown_target", "", "GET DIAGNOSTICS x=ROW_COUNT;", "42601", `"x" is not a known variable`, "x=ROW_COUNT"},
		{"record_target", "x record;", "GET DIAGNOSTICS x=ROW_COUNT;", "42601", `"x" is not a scalar variable`, "x=ROW_COUNT"},
		{"array_target", "x integer[];", "GET DIAGNOSTICS x[1]=ROW_COUNT;", "42601", `"x" is not a scalar variable`, "x[1]"},
		{"constant_target", "x CONSTANT bigint:=1;", "GET DIAGNOSTICS x=ROW_COUNT;", "22005", `variable "x" is declared CONSTANT`, "x=ROW_COUNT"},
		{"missing_item", "x text;", "GET DIAGNOSTICS x=;", "42601", `unrecognized GET DIAGNOSTICS item at or near ";"`, "; END IF"},
		{"trailing_item", "x text;", "GET DIAGNOSTICS x=PG_CONTEXT trailing;", "42601", `syntax error at or near "trailing"`, "trailing"},
		{"server_file_unknown", "x text;", "GET STACKED DIAGNOSTICS x=PG_EXCEPTION_FILE;", "42601", `unrecognized GET DIAGNOSTICS item at or near "PG_EXCEPTION_FILE"`, "PG_EXCEPTION_FILE"},
		{"server_line_unknown", "x text;", "GET STACKED DIAGNOSTICS x=PG_EXCEPTION_LINE;", "42601", `unrecognized GET DIAGNOSTICS item at or near "PG_EXCEPTION_LINE"`, "PG_EXCEPTION_LINE"},
		{"server_routine_unknown", "x text;", "GET STACKED DIAGNOSTICS x=PG_EXCEPTION_ROUTINE;", "42601", `unrecognized GET DIAGNOSTICS item at or near "PG_EXCEPTION_ROUTINE"`, "PG_EXCEPTION_ROUTINE"},
		{"utf8_unknown_item", "\"é\" text;", "GET DIAGNOSTICS \"é\"=bogus;", "42601", `unrecognized GET DIAGNOSTICS item at or near "bogus"`, "bogus"},
		{"quoted_lower_count", "x text;", `GET DIAGNOSTICS x="row_count";`, "42601", `unrecognized GET DIAGNOSTICS item at or near ""row_count""`, `"row_count"`},
		{"quoted_upper_count", "x text;", `GET DIAGNOSTICS x="ROW_COUNT";`, "42601", `unrecognized GET DIAGNOSTICS item at or near ""ROW_COUNT""`, `"ROW_COUNT"`},
		{"quoted_lower_context", "x text;", `GET DIAGNOSTICS x="pg_context";`, "42601", `unrecognized GET DIAGNOSTICS item at or near ""pg_context""`, `"pg_context"`},
		{"quoted_upper_context", "x text;", `GET DIAGNOSTICS x="PG_CONTEXT";`, "42601", `unrecognized GET DIAGNOSTICS item at or near ""PG_CONTEXT""`, `"PG_CONTEXT"`},
		{"quoted_lower_stacked", "x text;", `GET STACKED DIAGNOSTICS x="message_text";`, "42601", `unrecognized GET DIAGNOSTICS item at or near ""message_text""`, `"message_text"`},
		{"quoted_upper_stacked", "x text;", `GET STACKED DIAGNOSTICS x="MESSAGE_TEXT";`, "42601", `unrecognized GET DIAGNOSTICS item at or near ""MESSAGE_TEXT""`, `"MESSAGE_TEXT"`},
		{"quoted_unicode_count", `"é" text;`, `GET DIAGNOSTICS "é"=U&"row\005fcount";`, "42601", `unrecognized GET DIAGNOSTICS item at or near "U&"row\005fcount""`, `U&"row\005fcount"`},
		{"quoted_unicode_context", `"é" text;`, `GET DIAGNOSTICS "é"=U&"PG\005fCONTEXT";`, "42601", `unrecognized GET DIAGNOSTICS item at or near "U&"PG\005fCONTEXT""`, `U&"PG\005fCONTEXT"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE dg_error_marks(i integer)", "CREATE FUNCTION dg_intact() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dg_error_marks VALUES(7)", 1))
				q := "CREATE FUNCTION dg_bad() RETURNS integer LANGUAGE plpgsql AS $$DECLARE " + tc.declaration + " BEGIN IF false THEN " + tc.command + " END IF; RETURN 1; END$$"
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				// Source-derived character position; root oracle discovery resolves
				// compile Context and scanner-token details, never native output.
				at := strings.LastIndex(q, tc.token)
				if at < 0 {
					t.Fatal("missing diagnostic token", tc.token)
				}
				want.Position = fixture.Position(utf8.RuneCountInString(q[:at]) + 1)
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					sqlExecutionCount(t, p, b, c, "pg_proc WHERE proname='dg_bad'", 0)
					sqlExecutionValue(t, p, b, c, "SELECT dg_intact() AS v", "INT4", int64(7))
					sqlExecutionValue(t, p, b, c, "SELECT i+0 AS v FROM dg_error_marks", "INT4", int64(7))
				}
			})
		})
	}
	for _, item := range []string{"MESSAGE_TEXT", "PG_CONTEXT"} {
		t.Run("no_handler_"+item, func(t *testing.T) {
			diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION dg_outside(reached boolean) RETURNS text LANGUAGE plpgsql AS $$DECLARE x text; BEGIN IF reached THEN GET STACKED DIAGNOSTICS x="+item+"; END IF; RETURN 'healthy'; END$$")
				sqlExecutionValue(t, p, b, c, "SELECT dg_outside(false) AS v", "TEXT", "healthy")
				want := plpgsqlRuntimeDiagnostic("0Z002", "GET STACKED DIAGNOSTICS cannot be used outside an exception handler", "dg_outside(boolean)", "GET STACKED DIAGNOSTICS")
				plpgsqlError(t, p, c, "SELECT dg_outside(true) AS v", true, want)
				sqlExecutionValue(t, p, b, c, "SELECT dg_outside(false) AS v", "TEXT", "healthy")
			})
		})
	}
	t.Run("scalar_fields_aliases_and_ordered_conversion", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TYPE dg_row AS (n bigint)", "CREATE FUNCTION dg_targets() RETURNS text LANGUAGE plpgsql AS $$<<outer_block>> DECLARE x bigint; a ALIAS FOR x; r dg_row; good text; bad integer; BEGIN PERFORM 1 FROM (VALUES(1),(2),(3)) q(i); GET DIAGNOSTICS a=ROW_COUNT,r.n=ROW_COUNT,outer_block.x=ROW_COUNT; BEGIN RAISE EXCEPTION USING MESSAGE='assigned first',DETAIL='not integer'; EXCEPTION WHEN OTHERS THEN BEGIN GET STACKED DIAGNOSTICS good=MESSAGE_TEXT,bad=PG_EXCEPTION_DETAIL; EXCEPTION WHEN invalid_text_representation THEN bad:=bad; END; END; RETURN x::text || ':' || r.n::text || ':' || good; END$$")
			sqlExecutionValue(t, p, b, c, "SELECT dg_targets() AS v", "TEXT", "3:3:assigned first")
		})
	})
}
