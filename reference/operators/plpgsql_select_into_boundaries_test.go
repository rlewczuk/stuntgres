package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSelectIntoBoundaries(t *testing.T) {
	// Keep SQL tails outside the destination. PostgreSQL's INTO reader accepts
	// scalar datums and comma-separated lists, not general assignment targets.
	// Quoted keyword fields below are discovery expectations; the error cases
	// retain the oracle's rejection of unquoted fields and array subscripts.
	for _, tc := range []struct{ name, declarations, statement, result, want string }{
		{"fetch_first", "x integer:=99;", "SELECT id INTO x FROM si_boundary_rows ORDER BY id DESC FETCH FIRST ROW ONLY;", "x||':'||FOUND", "3:true"},
		{"fetch_after_target", "x integer:=99;", "SELECT 7 INTO STRICT x FETCH FIRST ROW ONLY;", "x||':'||FOUND", "7:true"},
		{"fetch_zero", "x integer:=99;", "SELECT 7 INTO x FETCH FIRST 0 ROWS ONLY;", "coalesce(x::text,'NULL')||':'||FOUND", "NULL:false"},
		{"for_update", "x integer:=99;", "SELECT id FROM si_boundary_rows WHERE id=2 INTO STRICT x FOR UPDATE;", "x||':'||FOUND", "2:true"},
		{"window", "x bigint:=99;", "SELECT row_number() OVER w INTO STRICT x WINDOW w AS ();", "x||':'||FOUND", "1:true"},
		{"quoted_keyword_field", "r si_boundary_box;", `SELECT 11 INTO r."from";`, `r."from"||':'||FOUND`, "11:true"},
		{"quoted_keyword_field_before_from", "r si_boundary_box;", `SELECT id INTO r."from" FROM si_boundary_rows WHERE id=2;`, `r."from"||':'||FOUND`, "2:true"},
		{"quoted_keyword_field_list", "r si_boundary_box;", `SELECT 11,12 INTO STRICT r."from",r."where" FETCH FIRST ROW ONLY;`, `r."from"||':'||r."where"||':'||FOUND`, "11:12:true"},
		{"early_quoted_field_list", "r si_boundary_box;", `SELECT INTO r."from",r."where" 17,18;`, `r."from"||':'||r."where"||':'||FOUND`, "17:18:true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c,
					`CREATE TYPE si_boundary_box AS ("from" integer,"where" integer,"order" integer[])`,
					"CREATE TABLE si_boundary_rows(id integer)")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO si_boundary_rows VALUES(1),(2),(3)", 3))
				userOperatorExec(t, p, b, c, "CREATE FUNCTION si_boundary_value() RETURNS text LANGUAGE plpgsql AS $$DECLARE "+tc.declarations+" BEGIN "+tc.statement+" RETURN "+tc.result+"; END$$")
				// plpgsqlValue exercises both direct and fresh prepared execution.
				plpgsqlValue(t, p, b, c, "SELECT si_boundary_value() AS v", "TEXT", tc.want)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM si_boundary_rows", "INT8", int64(3))
			})
		})
	}

	for _, tc := range []struct{ name, statement, token, message string }{
		{"unquoted_keyword_field", "SELECT 11 INTO r.from;", ".", `syntax error at or near "."`},
		{"unquoted_keyword_field_list", "SELECT 11,12 INTO STRICT r.from,r.where FETCH FIRST ROW ONLY;", ".", `syntax error at or near "."`},
		{"early_unquoted_field_list", "SELECT INTO r.from,r.where 17,18;", ".", `syntax error at or near "."`},
		{"field_subscript", "SELECT 13,14 INTO r.order[(SELECT 1)],r.order[2] FETCH FIRST ROW ONLY;", "[", `syntax error at or near "["`},
		{"subscript_list", "SELECT 15,16 INTO a[(SELECT 1 /* FROM INTO , ] */)],x FETCH FIRST ROW ONLY;", "[", `syntax error at or near "["`},
		{"early_subscript_list", "SELECT INTO a[(SELECT 1)],x 19,20;", "[", `syntax error at or near "["`},
		{"incomplete_field", "SELECT 1 INTO r.;", ".", `syntax error at or near "."`},
		{"double_field_separator", "SELECT 1 INTO r..from;", "..", `syntax error at or near ".."`},
		{"incomplete_subscript", "SELECT 1 INTO a[1+];", "[", `syntax error at or near "["`},
		{"empty_list_item", "SELECT 1,2 INTO x,,x;", ",,", `syntax error at or near ","`},
		{"duplicate_into", "SELECT 1 INTO x INTO x;", "INTO x;", `INTO specified more than once at or near "INTO"`},
		{"malformed_fetch_tail", "SELECT 1 INTO x FETCH FIRST ROW ONLY WHERE false;", "WHERE", `syntax error at or near "WHERE"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, `CREATE TYPE si_boundary_box AS ("from" integer,"where" integer,"order" integer[])`)
				q := "CREATE FUNCTION si_boundary_bad() RETURNS void LANGUAGE plpgsql AS $$DECLARE x integer; a integer[]; r si_boundary_box; BEGIN " + tc.statement + " END$$"
				want := fixture.ControlsReferenceDiagnostic("42601", tc.message)
				start := strings.Index(q, "BEGIN ") + len("BEGIN ")
				i := strings.Index(q[start:], tc.token)
				if tc.name == "empty_list_item" {
					// The second comma is the missing scalar destination.
					i++
				}
				want.Position = strconv.Itoa(utf8.RuneCountInString(q[:start+i]) + 1)
				// Existing oracle observations ground these rejection locations;
				// pinned PostgreSQL resolves any remaining discovery expectations.
				plpgsqlError(t, p, c, q, false, want)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='si_boundary_bad'", "INT8", int64(0))
				// A rejected continuation must not poison later direct/prepared work.
				plpgsqlValue(t, p, b, c, "SELECT 23 AS v", "INT4", int64(23))
			})
		})
	}
}
