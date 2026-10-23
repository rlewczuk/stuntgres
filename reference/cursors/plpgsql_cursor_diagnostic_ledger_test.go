package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Released PL datum allocation is independent of native executable slots:
// cursor formal lists and scalar FETCH lists each add one private ROW;
// compound fields are deduplicated by their real lexical parent identity.
func TestReferencePLpgSQLCursorDiagnosticLedger(t *testing.T) {
	for _, tc := range []struct {
		name, declarations, statements, body string
		datum                                int
		typeError                            bool
		setup                                []string
	}{
		{name: "bound_no_formals", declarations: "c CURSOR FOR SELECT 7;", statements: "OPEN c; CLOSE c;", datum: 2},
		{name: "bound_one_formal", declarations: "c CURSOR(a integer) FOR SELECT a;", statements: "OPEN c(7); CLOSE c;", datum: 4},
		{name: "bound_two_formals", declarations: "c CURSOR(a integer,b integer) FOR SELECT a+b;", statements: "OPEN c(7,8); CLOSE c;", datum: 5},
		{name: "fetch_scalar", declarations: "c refcursor; x integer;", statements: "OPEN c FOR SELECT 7; FETCH c INTO x; CLOSE c;", datum: 4},
		{name: "fetch_list", declarations: "c refcursor; x integer; y integer;", statements: "OPEN c FOR SELECT 7,8; FETCH c INTO x,y; CLOSE c;", datum: 5},
		{name: "fetch_whole_record", declarations: "c refcursor; r record;", statements: "OPEN c FOR SELECT 7 AS f1; FETCH c INTO r; CLOSE c;", datum: 3},
		{name: "fetch_field", declarations: "c refcursor; r record;", statements: "r:=ROW(0); OPEN c FOR SELECT 7; FETCH c INTO r.f1; CLOSE c;", datum: 5},
		{name: "fetch_duplicate_field_two_rows", declarations: "c refcursor; r record;", statements: "r:=ROW(0); OPEN c FOR SELECT 7; FETCH c INTO r.f1; CLOSE c; OPEN c FOR SELECT 8; FETCH c INTO r.f1; CLOSE c;", datum: 6},
		{name: "open_query_duplicate_field", declarations: "r record; c refcursor;", statements: "r:=ROW(7); OPEN c FOR SELECT r.f1+r.f1; CLOSE c;", datum: 4},
		{name: "unreachable_query_duplicate_field", declarations: "r record; c refcursor;", statements: "IF false THEN OPEN c FOR SELECT r.f1+r.f1; END IF;", datum: 4},
		{name: "bound_query_preceding_record", declarations: "r record; c CURSOR(a integer) FOR SELECT r.f1+a;", statements: "r:=ROW(7); OPEN c(1); CLOSE c;", datum: 6},
		{name: "using_alias_parent_dedup", declarations: "r record; alias_r ALIAS FOR r; c refcursor;", statements: "r:=ROW(7); OPEN c FOR EXECUTE 'SELECT $1::integer+$2::integer' USING r.f1,alias_r.f1; CLOSE c;", datum: 4},
		{name: "command_record_field", declarations: "r record; c refcursor;", statements: "r:=ROW('SELECT 7'::text); OPEN c FOR EXECUTE r.f1; CLOSE c;", datum: 4},
		{name: "count_before_field_target", declarations: "r record; c refcursor;", statements: "r:=ROW(1,0); OPEN c SCROLL FOR SELECT 7; FETCH ABSOLUTE r.f1 FROM c INTO r.f2; CLOSE c;", datum: 6},
		{name: "named_actuals_written_scope", declarations: "r record; c CURSOR(a integer,b integer) FOR SELECT a+b;", statements: "r:=ROW(7,8); OPEN c(b=>r.f2,a=>r.f1); CLOSE c;", datum: 8},
		{name: "for_actual_outer_record", body: "DECLARE r record; c CURSOR(a integer) FOR SELECT a AS f1; BEGIN r:=ROW(7); FOR r IN c(r.f1) LOOP CASE 2 WHEN 1/0 THEN RETURN r.f1; ELSE RETURN 2; END CASE; END LOOP; RETURN 0; END", datum: 8},
		{name: "fetch_whole_named_composite", declarations: "c refcursor; r pc_ledger_pair;", statements: "OPEN c FOR SELECT 7 AS f1; FETCH c INTO r; CLOSE c;", datum: 3, setup: []string{"CREATE TYPE pc_ledger_pair AS (f1 integer)"}},
		{name: "fetch_whole_domain_composite", declarations: "c refcursor; r pc_ledger_domain;", statements: "OPEN c FOR SELECT 7 AS f1; FETCH c INTO r; CLOSE c;", datum: 3, setup: []string{"CREATE TYPE pc_ledger_pair AS (f1 integer)", "CREATE DOMAIN pc_ledger_domain AS pc_ledger_pair"}},
		{name: "bound_formal_type_position", declarations: "c CURSOR(a integer) FOR SELECT a;", statements: "OPEN c(7); CLOSE c;", datum: 4, typeError: true},
		{name: "fetch_six_fields_type_position", declarations: "c refcursor; r record;", statements: "r:=ROW(0,0,0,0,0,0); OPEN c FOR SELECT 1,2,3,4,5,6; FETCH c INTO r.f1,r.f2,r.f3,r.f4,r.f5,r.f6; CLOSE c;", datum: 10, typeError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, setup := range tc.setup {
					sqlCursorStep(t, p, b, c, fixture.Exec(setup, 0))
				}
				item := "1/0"
				if tc.typeError {
					item = "true"
				}
				body := tc.body
				if body == "" {
					body = "DECLARE " + tc.declarations + " BEGIN " + tc.statements + " CASE 2 WHEN " + item + " THEN RETURN 1; ELSE RETURN 2; END CASE; END"
				}
				plCursorFunction(t, p, b, c, "pc_ledger", "", "integer", body)
				want := sqlCursorDiagnostic("22012", "division by zero")
				want.Context = fmt.Sprintf("PL/pgSQL expression \"\"__Case__Variable_%d__\" IN (1/0)\"\nPL/pgSQL function pc_ledger() line 1 at CASE", tc.datum)
				if tc.typeError {
					want = plCursorRuntime("42883", "operator does not exist: integer = boolean", "pc_ledger()", "CASE")
					want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
					want.InternalQuery = fmt.Sprintf(`"__Case__Variable_%d__" IN (true)`, tc.datum)
					want.InternalPosition = "24"
					if tc.datum >= 10 {
						want.InternalPosition = "25"
					}
				}
				// Complete-record mismatches expose any unknown oracle field,
				// synthetic-name count, query coordinate or prepared route frame.
				plCursorError(t, p, b, c, "SELECT pc_ledger() AS v", want, true, false)
				plCursorValue(t, p, b, c, "SELECT 7 AS v", "INT4", int64(7))
			})
		})
	}
}
