package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineResultsDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, signature, body, result, detail, corrected string }{
		{"scalar_width", "() RETURNS integer", "SELECT 1,2", "integer", "Final statement must return exactly one column.", "SELECT 1"},
		{"scalar_type", "() RETURNS integer", "SELECT 'bad'::text", "integer", "Actual return type is text.", "SELECT 1"},
		{"record_few", "(OUT a integer,OUT b text)", "SELECT 1", "record", "Final statement returns too few columns.", "SELECT 1,'ok'::text"},
		{"record_many", "(OUT a integer,OUT b text)", "SELECT 1,'ok'::text,3", "record", "Final statement returns too many columns.", "SELECT 1,'ok'::text"},
		{"record_order", "(OUT a integer,OUT b text)", "SELECT 'bad'::text,1", "record", "Final statement returns text instead of integer at column 1.", "SELECT 1,'ok'::text"},
		{"record_type", "(OUT a integer,OUT b integer)", "SELECT 1,'bad'::text", "record", "Final statement returns text instead of integer at column 2.", "SELECT 1,2"},
		{"named_few", "() RETURNS rr_pair", "SELECT 1", "rr_pair", "Final statement returns too few columns.", "SELECT 1,2"},
		{"named_many", "() RETURNS rr_pair", "SELECT 1,2,3", "rr_pair", "Final statement returns too many columns.", "SELECT 1,2"},
		{"named_type", "() RETURNS rr_pair", "SELECT 1,'bad'::text", "rr_pair", "Final statement returns text instead of integer at column 2.", "SELECT 1,2"},
		{"table_few", "() RETURNS TABLE(a integer,b integer)", "SELECT 1", "record", "Final statement returns too few columns.", "SELECT 1,2"},
		{"set_type", "() RETURNS SETOF integer", "SELECT 'bad'::text", "integer", "Actual return type is text.", "SELECT 1"},
		{"empty_bad_schema", "() RETURNS TABLE(a integer,b integer)", "SELECT v,'bad'::text FROM rr_rows WHERE k<0", "record", "Final statement returns text instead of integer at column 2.", "SELECT 1,2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsSetSetup(t, p, b, c)
				name := "rr_bad_" + tc.name
				q := "CREATE FUNCTION " + name + tc.signature + " LANGUAGE SQL AS $$" + tc.body + "$$"
				want := fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return "+tc.result)
				want.Detail, want.Context = tc.detail, `SQL function "`+name+`"`
				resultsError(t, p, c, q, false, "execute", want)
				resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='"+name+"'", 0)
				resultsExec(t, p, b, c, "CREATE FUNCTION "+name+tc.signature+" LANGUAGE SQL AS $$"+tc.corrected+"$$")
				resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM "+name+"()", 1)
			})
		})
	}
	t.Run("declarations_and_scalar_context", func(t *testing.T) {
		resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsSetSetup(t, p, b, c)
			resultsMultiSetup(t, p, b, c)
			want := fixture.ControlsReferenceDiagnostic("42P13", "function result type must be record because of OUT parameters")
			resultsError(t, p, c, "CREATE FUNCTION rr_bad_decl(IN x integer,OUT a integer,OUT b integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1,$1'", false, "execute", want)
			resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rr_bad_decl'", 0)
			resultsExec(t, p, b, c, "CREATE FUNCTION rr_bad_decl(IN x integer,OUT a integer,OUT b integer) RETURNS record LANGUAGE SQL AS 'SELECT $1,$1'")
			resultsBoth(t, p, b, c, "SELECT * FROM rr_bad_decl(4)", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(4)}})
			q := "CREATE FUNCTION rr_bad_table(OUT x integer) RETURNS TABLE(y integer) LANGUAGE SQL AS 'SELECT 1'"
			want = fixture.ControlsReferenceDiagnostic("42601", "OUT and INOUT arguments aren't allowed in TABLE functions")
			want.Position = fixture.Position(30)
			resultsError(t, p, c, q, false, "prepare", want)
			for _, q := range []string{"SELECT 1 WHERE rr_set(2)>0", "SELECT 'é' AS label WHERE rr_set(2)>0"} {
				want = fixture.ControlsReferenceDiagnostic("0A000", "set-returning functions are not allowed in WHERE")
				want.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "rr_set")]) + 1)
				resultsError(t, p, c, q, true, "prepare", want)
			}
			q = "SELECT (rr_empty_scalar(1)).bad"
			resultsExec(t, p, b, c, "CREATE FUNCTION rr_empty_scalar(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'")
			want = fixture.ControlsReferenceDiagnostic("42809", "column notation .bad applied to type integer, which is not a composite type")
			want.Position = fixture.Position(9)
			resultsError(t, p, c, q, true, "prepare", want)
			want = fixture.ControlsReferenceDiagnostic("21000", "more than one row returned by a subquery used as an expression")
			resultsError(t, p, c, "SELECT (SELECT v FROM rr_set($1::integer) AS v)", true, "execute", want, 2)
			resultsMultiCheck(t, p, b, c)
		})
	})
	t.Run("standard_definition", func(t *testing.T) {
		resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			want := fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return record")
			want.Detail = "Final statement returns too few columns."
			want.Context = `SQL function "rr_checked_bad"`
			resultsError(t, p, c, "CREATE FUNCTION rr_checked_bad(OUT a integer,OUT b integer) BEGIN ATOMIC SELECT 1; END", false, "execute", want)
			resultsExec(t, p, b, c, "CREATE FUNCTION rr_checked_bad(OUT a integer,OUT b integer) BEGIN ATOMIC SELECT 1,2; END")
			resultsBoth(t, p, b, c, "SELECT * FROM rr_checked_bad()", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}})
		})
	})
}
