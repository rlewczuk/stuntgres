package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLDiagnosticsStatus(t *testing.T) {
	diagnosticsDynamicStatus(t)
	for _, tc := range []struct{ name, setup, body, want string }{
		{"initial_perform", "", diagnosticsSnapshot + "PERFORM NULL FROM (VALUES(1),(2),(3)) AS q(i);" + diagnosticsSnapshot + "x := 7;" + diagnosticsSnapshot + "GET CURRENT DIAGNOSTICS n := ROW_COUNT;" + diagnosticsSnapshot + "PERFORM 1 WHERE false;" + diagnosticsSnapshot, "0:false;3:true;3:true;3:true;0:false;"},
		{"dml", "CREATE TABLE dg_data(i integer)", "INSERT INTO dg_data VALUES(1),(2),(3);" + diagnosticsSnapshot + "UPDATE dg_data SET i=i+10 WHERE i<3;" + diagnosticsSnapshot + "DELETE FROM dg_data WHERE i=99;" + diagnosticsSnapshot + "DELETE FROM dg_data;", "3:true;2:true;0:false;"},
		{"utility", "", "PERFORM 1; SET application_name='diagnostics';" + diagnosticsSnapshot + "PERFORM 1 WHERE false; SET application_name='diagnostics';" + diagnosticsSnapshot, "0:true;0:false;"},
		{"expressions", "CREATE FUNCTION dg_child() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM 1 WHERE false; RETURN 7; END$$", "PERFORM 1 FROM (VALUES(1),(2),(3)) q(i); x:=dg_child();" + diagnosticsSnapshot + "x := (SELECT 8);" + diagnosticsSnapshot + "PERFORM 1 FROM (VALUES(1),(2),(3)) q(i); x := (SELECT 8 WHERE false);" + diagnosticsSnapshot + "IF x IS NOT NULL THEN RAISE EXCEPTION 'expected NULL'; END IF;", "3:true;1:true;1:true;"},
		{"call", "CREATE PROCEDURE dg_child_call() LANGUAGE plpgsql AS $$BEGIN PERFORM 1 WHERE false; END$$", "PERFORM 1 FROM (VALUES(1),(2),(3)) q(i); CALL dg_child_call();" + diagnosticsSnapshot, "3:true;"},
		{"recursive", "CREATE FUNCTION dg_rec(depth integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE n bigint; child text; BEGIN PERFORM 1 FROM (VALUES(1),(2),(3)) q(i) WHERE i<=depth; IF depth>1 THEN child:=dg_rec(depth-1); END IF; GET DIAGNOSTICS n=ROW_COUNT; RETURN n::text || ':' || found::text || COALESCE('/' || child,''); END$$", "s:=dg_rec(3);", "3:true/1:true/1:true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if tc.setup != "" {
					userOperatorExec(t, p, b, c, tc.setup)
				}
				diagnosticsTrace(t, p, b, c, tc.body, tc.want)
			})
		})
	}
	t.Run("bigint_metadata", func(t *testing.T) {
		diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION dg_count() RETURNS bigint LANGUAGE plpgsql AS $$DECLARE n bigint; BEGIN GET DIAGNOSTICS n=ROW_COUNT; RETURN n; END$$")
			sqlExecutionValue(t, p, b, c, "SELECT dg_count() AS v", "INT8", int64(0))
		})
	})
}

func TestReferencePLpgSQLDiagnosticsCaptureCursorFound(t *testing.T) {
	diagnosticsStaticCaptureBoundary(t)
	for _, tc := range []struct{ name, setup, body, want string }{
		{"capture_demand", "", "SELECT i INTO x FROM (VALUES(1),(2),(3)) q(i);" + diagnosticsSnapshot + "BEGIN SELECT i INTO STRICT x FROM (VALUES(1),(2),(3)) q(i); EXCEPTION WHEN too_many_rows THEN " + diagnosticsSnapshot + " END; BEGIN SELECT i INTO STRICT x FROM (VALUES(1)) q(i) WHERE false; EXCEPTION WHEN no_data_found THEN " + diagnosticsSnapshot + " END;", "1:true;2:true;0:false;"},
		{"conversion_publication", "", "PERFORM 1 FROM (VALUES(1),(2),(3)) q(i); BEGIN SELECT 'bad'::text INTO x; EXCEPTION WHEN invalid_text_representation THEN " + diagnosticsSnapshot + " END; PERFORM 1 FROM (VALUES(1),(2),(3)) q(i); BEGIN PERFORM 1/q.z FROM (VALUES(0)) q(z); EXCEPTION WHEN division_by_zero THEN " + diagnosticsSnapshot + " END;", "1:true;3:true;"},
		{"second_projection", "", "PERFORM 1 FROM (VALUES(1),(2),(3)) q(i); BEGIN SELECT 10/i INTO STRICT x FROM (VALUES(1),(0),(2)) q(i); EXCEPTION WHEN division_by_zero THEN " + diagnosticsSnapshot + " END;", "3:true;"},
		{"dml_cardinality", "CREATE TABLE dg_cap(i integer)", "INSERT INTO dg_cap VALUES(1),(2),(3); BEGIN UPDATE dg_cap SET i=i+10 RETURNING i INTO x; EXCEPTION WHEN too_many_rows THEN " + diagnosticsSnapshot + " END; SELECT count(*) INTO x FROM dg_cap WHERE i<10; s:=s || x::text; DELETE FROM dg_cap;", "2:true;3"},
		{"fetch_move", "", "DECLARE cur CURSOR FOR SELECT i FROM (VALUES(1),(2),(3)) q(i); BEGIN OPEN cur; FETCH cur INTO x;" + diagnosticsSnapshot + "MOVE FORWARD 2 FROM cur;" + diagnosticsSnapshot + "MOVE FORWARD 2 FROM cur;" + diagnosticsSnapshot + "FETCH cur INTO x;" + diagnosticsSnapshot + "CLOSE cur; END;", "1:true;2:true;0:false;0:false;"},
		{"integer_for", "", "PERFORM 1 WHERE false; FOR x IN 1..2 LOOP " + diagnosticsSnapshot + " EXIT; END LOOP;" + diagnosticsSnapshot + "FOR x IN 2..1 LOOP s:=s || 'bad'; END LOOP;" + diagnosticsSnapshot, "0:false;0:true;0:false;"},
		{"query_for", "", "DECLARE r record; BEGIN PERFORM 1 WHERE false; FOR r IN SELECT i FROM (VALUES(1),(2)) q(i) LOOP " + diagnosticsSnapshot + " EXIT; END LOOP;" + diagnosticsSnapshot + "FOR r IN SELECT 1 WHERE false LOOP s:=s || 'bad'; END LOOP;" + diagnosticsSnapshot + "END;", "0:false;0:true;0:false;"},
		{"cursor_for", "", "DECLARE cur CURSOR FOR SELECT 1; empty_cur CURSOR FOR SELECT 1 WHERE false; BEGIN PERFORM 1 WHERE false; FOR x IN cur LOOP " + diagnosticsSnapshot + " EXIT; END LOOP;" + diagnosticsSnapshot + "FOR x IN empty_cur LOOP s:=s || 'bad'; END LOOP;" + diagnosticsSnapshot + "END;", "0:false;0:true;0:false;"},
		{"foreach", "", "PERFORM 1 WHERE false; FOREACH x IN ARRAY ARRAY[1,2] LOOP " + diagnosticsSnapshot + " EXIT; END LOOP;" + diagnosticsSnapshot + "FOREACH x IN ARRAY ARRAY[]::integer[] LOOP s:=s || 'bad'; END LOOP;" + diagnosticsSnapshot, "0:false;0:true;0:false;"},
		{"integer_for_completed", "", "PERFORM 1 WHERE false; FOR x IN 1..2 LOOP " + diagnosticsSnapshot + " END LOOP;" + diagnosticsSnapshot, "0:false;0:false;0:true;"},
		{"query_for_completed", "", "DECLARE r record; BEGIN PERFORM 1 WHERE false; FOR r IN SELECT i FROM (VALUES(1),(2)) q(i) LOOP " + diagnosticsSnapshot + " END LOOP;" + diagnosticsSnapshot + "END;", "0:false;0:false;0:true;"},
		{"cursor_for_completed", "", "DECLARE cur CURSOR FOR SELECT i FROM (VALUES(1),(2)) q(i); BEGIN PERFORM 1 WHERE false; FOR x IN cur LOOP " + diagnosticsSnapshot + " END LOOP;" + diagnosticsSnapshot + "END;", "0:false;0:false;0:true;"},
		{"foreach_completed", "", "PERFORM 1 WHERE false; FOREACH x IN ARRAY ARRAY[1,2] LOOP " + diagnosticsSnapshot + " END LOOP;" + diagnosticsSnapshot, "0:false;0:false;0:true;"},
		{"nonstrict_projection_demand", "", "SELECT 10/i INTO x FROM (VALUES(1),(0)) q(i);" + diagnosticsSnapshot, "1:true;"},
		{"while_loop_body", "", "PERFORM 1; WHILE false LOOP s:=s || 'bad'; END LOOP;" + diagnosticsSnapshot + "LOOP " + diagnosticsSnapshot + " PERFORM 1 WHERE false; EXIT; END LOOP;" + diagnosticsSnapshot, "1:true;1:true;0:false;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if tc.setup != "" {
					userOperatorExec(t, p, b, c, tc.setup)
				}
				diagnosticsTrace(t, p, b, c, tc.body, tc.want)
			})
		})
	}
	for _, tc := range []struct {
		name, decl, open, prior, command, close, condition, want, code, message, action, hint string
	}{
		{
			name:      "fetch_destination_failure_prior_true",
			decl:      "cur CURSOR FOR SELECT 'bad'::text;",
			open:      "OPEN cur;",
			prior:     "PERFORM 1 FROM (VALUES(1),(2),(3)) q(i);",
			command:   "FETCH cur INTO x;",
			close:     "CLOSE cur;",
			condition: "invalid_text_representation",
			want:      "3:true;99",
			code:      "22P02",
			message:   `invalid input syntax for type integer: "bad"`,
			action:    "FETCH",
		},
		{
			name:      "fetch_destination_failure_prior_false",
			decl:      "cur CURSOR FOR SELECT 'bad'::text;",
			open:      "OPEN cur;",
			prior:     "PERFORM 1 WHERE false;",
			command:   "FETCH cur INTO x;",
			close:     "CLOSE cur;",
			condition: "invalid_text_representation",
			want:      "0:false;99",
			code:      "22P02",
			message:   `invalid input syntax for type integer: "bad"`,
			action:    "FETCH",
		},
		{
			name:      "bare_select_nonempty_prior_false",
			prior:     "PERFORM 1 WHERE false;",
			command:   "SELECT i FROM (VALUES(1),(2),(3)) q(i);",
			condition: "syntax_error",
			want:      "3:true;99",
			code:      "42601",
			message:   "query has no destination for result data",
			action:    "SQL statement",
			hint:      "If you want to discard the results of a SELECT, use PERFORM instead.",
		},
		{
			name:      "bare_select_empty_prior_true",
			prior:     "PERFORM 1 FROM (VALUES(1),(2),(3)) q(i);",
			command:   "SELECT i FROM (VALUES(1),(2),(3)) q(i) WHERE false;",
			condition: "syntax_error",
			want:      "0:false;99",
			code:      "42601",
			message:   "query has no destination for result data",
			action:    "SQL statement",
			hint:      "If you want to discard the results of a SELECT, use PERFORM instead.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diagnosticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				// Snapshot before any handler SQL can replace status. The failed
				// destination also leaves x intact; only protected database work
				// rolls back, while the outer prefix remains.
				userOperatorExec(t, p, b, c,
					"CREATE TABLE dg_boundary_data(i integer)",
					"CREATE FUNCTION dg_boundary_caught() RETURNS text LANGUAGE plpgsql AS $$DECLARE n bigint; x integer:=99; s text:=''; "+tc.decl+" BEGIN INSERT INTO dg_boundary_data VALUES(8); "+tc.open+" BEGIN INSERT INTO dg_boundary_data VALUES(9); "+tc.prior+tc.command+" RAISE EXCEPTION 'expected command failure'; EXCEPTION WHEN "+tc.condition+" THEN "+diagnosticsSnapshot+" s:=s || x::text; END; "+tc.close+" RETURN s; END$$",
					"CREATE FUNCTION dg_boundary_failed() RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer:=99; "+tc.decl+" BEGIN INSERT INTO dg_boundary_data VALUES(9); "+tc.open+tc.prior+tc.command+tc.close+" RETURN 'unexpected success'; END$$")
				wantError := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "dg_boundary_failed()", tc.action)
				wantError.Hint = tc.hint
				for _, prepared := range []bool{false, true} {
					success := sqlExecutionStep(b, "SELECT dg_boundary_caught() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}})
					success.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, success)
					sqlExecutionValue(t, p, b, c, "SELECT i+0 AS v FROM dg_boundary_data", "INT4", int64(8))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM dg_boundary_data", 1))
					failure := fixture.ErrorDiagnostic("SELECT dg_boundary_failed() AS v", wantError)
					failure.SelectRows, failure.Prepared, failure.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, failure)
					sqlExecutionCount(t, p, b, c, "dg_boundary_data", 0)
				}
			})
		})
	}
}
