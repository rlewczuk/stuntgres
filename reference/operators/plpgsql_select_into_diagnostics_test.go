package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSelectIntoDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, sql, code, message, hint string }{
		{"strict_zero", "SELECT id INTO STRICT x FROM si_diag WHERE false", "P0002", "query returned no rows", ""},
		{"strict_many", "SELECT id INTO STRICT x FROM si_diag ORDER BY id", "P0003", "query returned more than one row", "Make sure the query returns a single row, or use LIMIT 1."},
		{"insert_many", "INSERT INTO si_diag VALUES(3),(4) RETURNING id INTO x", "P0003", "query returned more than one row", "Make sure the query returns a single row, or use LIMIT 1."},
		{"update_many", "UPDATE si_diag SET id=id+10 RETURNING id INTO x", "P0003", "query returned more than one row", "Make sure the query returns a single row, or use LIMIT 1."},
		{"delete_many", "DELETE FROM si_diag RETURNING id INTO x", "P0003", "query returned more than one row", "Make sure the query returns a single row, or use LIMIT 1."},
		{"conversion", "SELECT 'bad'::text INTO x", "22P02", `invalid input syntax for type integer: "bad"`, ""},
		{"execution", "SELECT 10/(id-id) INTO x FROM si_diag WHERE id=1", "22012", "division by zero", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE si_diag(id integer)")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO si_diag VALUES(1),(2)", 2))
				userOperatorExec(t, p, b, c, "CREATE FUNCTION si_diag_error() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer:=99; BEGIN\n /* é中 */ "+tc.sql+";\n RETURN x; END$$")
				// Pinned PostgreSQL 18.6 reports statement line2 and no cursor
				// position, internal query, detail or object identifiers. SQL
				// execution errors additionally identify the SQL with INTO blanked.
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Hint = tc.hint
				want.Context = "PL/pgSQL function si_diag_error() line 2 at SQL statement"
				if tc.name == "execution" {
					want.Context = "SQL statement \"SELECT 10/(id-id)        FROM si_diag WHERE id=1\"\n" + want.Context
				}
				plpgsqlError(t, p, c, "SELECT si_diag_error() AS v", true, want)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM si_diag", "INT8", int64(2))
			})
		})
	}
	t.Run("planning_multibyte", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE si_diag(id integer)")
			source := "SELECT /* é中 */ missing_input INTO x FROM si_diag"
			userOperatorExec(t, p, b, c, "CREATE FUNCTION si_diag_plan() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN\n "+source+"; RETURN x; END$$")
			want := fixture.ControlsReferenceDiagnostic("42703", `column "missing_input" does not exist`)
			// PG replaces the procedural clause with spaces in its prepared SQL.
			// Whether those blanks remain in InternalQuery is an explicit oracle
			// question; this is the released-parser source-derived expectation.
			want.InternalQuery = strings.Replace(source, "INTO x", strings.Repeat(" ", len("INTO x")), 1)
			want.InternalPosition = strconv.Itoa(utf8.RuneCountInString(source[:strings.Index(source, "missing_input")]) + 1)
			want.Context = "PL/pgSQL function si_diag_plan() line 2 at SQL statement"
			plpgsqlError(t, p, c, "SELECT si_diag_plan() AS v", true, want)
		})
	})
	t.Run("handler_condition_names_and_states", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION si_handlers() RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer:=99; s text:=''; BEGIN BEGIN SELECT 1 INTO STRICT x WHERE false; EXCEPTION WHEN SQLSTATE 'P0002' THEN s:=s||x||':'||FOUND; END; BEGIN SELECT i INTO STRICT x FROM (VALUES(1),(2)) q(i); EXCEPTION WHEN too_many_rows THEN s:=s||':'||x||':'||FOUND; END; RETURN s; END$$")
			plpgsqlValue(t, p, b, c, "SELECT si_handlers() AS v", "TEXT", "99:false:99:true")
		})
	})
	for _, tc := range []struct{ name, statement, token, message string }{
		{"trailing_junk", "SELECT 1 INTO x; nonsense;", "nonsense", `syntax error at or near "nonsense"`},
		{"empty_target", "SELECT 1 INTO ;", ";", `syntax error at or near ";"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "CREATE FUNCTION si_bad_syntax() RETURNS void LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN RETURN; " + tc.statement + " END$$"
				want := fixture.ControlsReferenceDiagnostic("42601", tc.message)
				start := strings.Index(q, tc.statement)
				want.Position = strconv.Itoa(utf8.RuneCountInString(q[:start+strings.Index(tc.statement, tc.token)]) + 1)
				plpgsqlError(t, p, c, q, false, want)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='si_bad_syntax'", "INT8", int64(0))
			})
		})
	}
}
