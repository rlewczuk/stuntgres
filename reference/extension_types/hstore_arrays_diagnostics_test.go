package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceHstoreArraysDiagnostics(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		for _, tc := range []struct {
			q, message string
			operator   bool
		}{
			{"SELECT hstore_to_array(1)", "function hstore_to_array(integer) does not exist", false},
			{"SELECT hstore_to_matrix(1)", "function hstore_to_matrix(integer) does not exist", false},
			{"SELECT hstore_to_array('a=>1'::text)", "function hstore_to_array(text) does not exist", false},
			{"SELECT hstore_to_matrix('a=>1'::text)", "function hstore_to_matrix(text) does not exist", false},
			{"SELECT hstore_to_array()", "function hstore_to_array() does not exist", false},
			{"SELECT hstore_to_matrix('a=>1'::hstore,1)", "function hstore_to_matrix(hstore, integer) does not exist", false},
			{"SELECT %% 1", "operator does not exist: %% integer", true}, {"SELECT %# 1", "operator does not exist: %# integer", true},
			{"SELECT %% 'a=>1'::text", "operator does not exist: %% text", true}, {"SELECT %# 'a=>1'::text", "operator does not exist: %# text", true},
		} {
			want := fixture.ControlsReferenceDiagnostic("42883", tc.message)
			want.Position = "8"
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			if tc.operator {
				want.Hint = "No operator matches the given name and argument type. You might need to add an explicit type cast."
			}
			for _, prepared := range []bool{false, true} {
				hstoreError(t, p, b, c, tc.q, prepared, true, true, want)
			}
		}
		for _, call := range []string{"hstore_to_array('a=b')", "hstore_to_matrix('a=b'::hstore)", "%% 'a=b'::hstore", "%# 'a=b'::hstore"} {
			q := "/*é*/ SELECT " + call
			want := fixture.ControlsReferenceDiagnostic("42601", `syntax error in hstore, near "b" at position 2`)
			want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'a=b'")]) + 1)
			for _, prepared := range []bool{false, true} {
				hstoreError(t, p, b, c, q, prepared, true, true, want)
			}
		}
		q := hstoreArraysQuery("$1::hstore")
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		want := hstoreParameterDiagnostic("42601", `syntax error in hstore, near "b" at position 2`, 1)
		hstoreError(t, p, b, held, q, false, true, false, want, "a=b")
		for _, tc := range []struct{ input, message string }{{"a=>\x00", `invalid byte sequence for encoding "UTF8": 0x00`}, {"a=>\xff", `invalid byte sequence for encoding "UTF8": 0xff`}} {
			diagnostic := fixture.ControlsReferenceDiagnostic("22021", tc.message)
			diagnostic.Context = "unnamed portal parameter $1"
			for _, prepared := range []bool{false, true} {
				hstoreError(t, p, b, c, q, prepared, true, false, diagnostic, tc.input)
			}
			hstoreError(t, p, b, held, q, false, true, false, diagnostic, tc.input)
		}
		hstoreRun(t, p, b, held, hstoreArraysSelect(b, q, [][]any{{"{a,1}", "{{a,1}}", "{a,1}", "{{a,1}}"}}, "a=>1"))
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_array_atomic(id integer,v text[])", 0))
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_array_atomic VALUES (1,ARRAY['sentinel'])", 1))
		insert := "INSERT INTO hs_array_atomic VALUES (2,hstore_to_array($1::hstore)),(3,hstore_to_array($2::hstore))"
		for _, prepared := range []bool{false, true} {
			hstoreError(t, p, b, c, insert, prepared, false, false, hstoreParameterDiagnostic("42601", `syntax error in hstore, near "b" at position 2`, 2), "a=>1", "a=b")
		}
		hstoreRun(t, p, b, c, fixture.ArrayReferenceSelect("SELECT id,v FROM hs_array_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "_TEXT"}, []string{"INT4", "_TEXT"}, [][]any{{int64(1), "{sentinel}"}}, b.PG))
	})
}
