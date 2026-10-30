package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreConstructDiagnostics(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		// Full empty auxiliary fields and ERROR severities are independent hypotheses
		// for pinned-oracle discovery; constructor-body errors have no SQL position.
		for _, tc := range []struct{ call, code, message string }{
			{"hstore(ARRAY['a'])", "2202E", "array must have even number of elements"},
			{"hstore(ARRAY[['a','1','x']])", "2202E", "array must have two columns"},
			{"hstore(ARRAY[[['a','1']]])", "2202E", "wrong number of array subscripts"},
			{"hstore(ARRAY[['a']],ARRAY['1'])", "2202E", "wrong number of array subscripts"},
			{"hstore(ARRAY['a'],ARRAY[['1']])", "2202E", "wrong number of array subscripts"},
			{"hstore(ARRAY['a','b'],ARRAY['1'])", "2202E", "arrays must have same bounds"},
			{"hstore('[0:0]={a}'::text[],'[1:1]={1}'::text[])", "2202E", "arrays must have same bounds"},
			{"hstore(ARRAY[NULL::text,'1'])", "22004", "null value not allowed for hstore key"},
			{"hstore(ARRAY[NULL::text],ARRAY['1'])", "22004", "null value not allowed for hstore key"},
			{"hstore(ARRAY[NULL::text])", "2202E", "array must have even number of elements"},
			{"hstore(ARRAY[[NULL::text,'1','x']])", "2202E", "array must have two columns"},
			{"hstore(ARRAY[[[NULL::text,'1']]])", "2202E", "wrong number of array subscripts"},
			{"hstore(ARRAY[NULL::text,'b'],ARRAY['1'])", "2202E", "arrays must have same bounds"},
		} {
			for _, prepared := range []bool{false, true} {
				hstoreError(t, p, b, c, "SELECT "+tc.call, prepared, true, false, fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
			}
		}
		q := "SELECT hstore($1::text[],$2::text[]) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ keys, values, code, message string }{
			{"{a,b}", "{1}", "2202E", "arrays must have same bounds"},
			{"{NULL}", "{1}", "22004", "null value not allowed for hstore key"},
			{"{{a}}", "{1}", "2202E", "wrong number of array subscripts"},
		} {
			want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			for _, prepared := range []bool{false, true} {
				hstoreError(t, p, b, c, q, prepared, true, false, want, tc.keys, tc.values)
			}
			hstoreError(t, p, b, held, q, false, true, false, want, tc.keys, tc.values)
			hstoreRun(t, p, b, held, hstoreSelect(b, q, []string{"v"}, [][]any{{`"a"=>"1"`}}, nil, "{a}", "{1}"))
		}
		malformed := hstoreParameterDiagnostic("22P02", `malformed array literal: "not-array"`, 1)
		malformed.Detail = `Array value must start with "{" or dimension information.`
		hstoreError(t, p, b, held, q, false, true, false, malformed, "not-array", "{1}")
		for _, tc := range []struct{ input, message string }{
			{"{a,\x00}", `invalid byte sequence for encoding "UTF8": 0x00`},
			{"{a,\xff}", `invalid byte sequence for encoding "UTF8": 0xff`},
		} {
			want := fixture.ControlsReferenceDiagnostic("22021", tc.message)
			want.Context = "unnamed portal parameter $1"
			for _, prepared := range []bool{false, true} {
				hstoreError(t, p, b, c, q, prepared, true, false, want, tc.input, "{1,2}")
			}
			hstoreError(t, p, b, held, q, false, true, false, want, tc.input, "{1,2}")
		}
		hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_construct_atomic(id integer,v hstore)", 0))
		hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_construct_atomic VALUES (1,hstore('sentinel','kept'))", 1))
		for _, prepared := range []bool{false, true} {
			hstoreError(t, p, b, c, "INSERT INTO hs_construct_atomic VALUES (2,hstore($1::text[])),(3,hstore($2::text[]))", prepared, false, false, fixture.ControlsReferenceDiagnostic("22004", "null value not allowed for hstore key"), "{a,1}", "{NULL,2}")
		}
		hstoreRun(t, p, b, c, hstoreSelect(b, "SELECT id,v FROM hs_construct_atomic ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), `"sentinel"=>"kept"`}}, []bool{true, true}))
	})
}

func TestReferenceHstoreConstructResolutionDiagnostics(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreInstall(t, p, b, c)
		for _, tc := range []struct {
			expression, token, message string
			operator                   bool
		}{
			{"exist('a=>1'::hstore,1)", "exist", "function exist(hstore, integer) does not exist", false},
			{"defined('a=>1'::hstore,1)", "defined", "function defined(hstore, integer) does not exist", false},
			{"hstore(1,2)", "hstore", "function hstore(integer, integer) does not exist", false},
			{"hstore()", "hstore", "function hstore() does not exist", false},
			{"hstore('a','b','c')", "hstore", "function hstore(unknown, unknown, unknown) does not exist", false},
			{"'a=>1'::hstore -> 1", "->", "operator does not exist: hstore -> integer", true},
			{"'a'::text -> 'b'::text", "->", "operator does not exist: text -> text", true},
		} {
			q := "/*雪*/ SELECT " + tc.expression
			want := fixture.ControlsReferenceDiagnostic("42883", tc.message)
			want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			if tc.operator {
				want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			}
			for _, prepared := range []bool{false, true} {
				hstoreError(t, p, b, c, q, prepared, true, true, want)
			}
		}
	})
}
