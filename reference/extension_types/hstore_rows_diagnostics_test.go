package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreRowsDiagnostics(t *testing.T) {
	// Each leaf owns its scenario so native's missing positive behavior cannot
	// prevent the root from observing the independent oracle diagnostic leaves.
	for _,tc:=range []struct{name,sql,token,code,message,hint string}{
		{"integer_keys",`SELECT skeys(1)`,"skeys","42883","function skeys(integer) does not exist","No function matches the given name and argument types. You might need to add explicit type casts."},
		{"no_values_arg",`SELECT svals()`,"svals","42883","function svals() does not exist","No function matches the given name and argument types. You might need to add explicit type casts."},
		{"integer_each",`SELECT each(1)`,"each","42883","function each(integer) does not exist","No function matches the given name and argument types. You might need to add explicit type casts."},
		{"typed_text",`SELECT svals('a=>x'::text)`,"svals","42883","function svals(text) does not exist","No function matches the given name and argument types. You might need to add explicit type casts."},
		{"extra_arity",`SELECT each('a=>x'::hstore,'b=>y'::hstore)`,"each","42883","function each(hstore, hstore) does not exist","No function matches the given name and argument types. You might need to add explicit type casts."},
		{"named_keys",`SELECT skeys(hs => 'a=>x'::hstore)`,"skeys","42883","function skeys(hs => hstore) does not exist","No function matches the given name and argument types. You might need to add explicit type casts."},
		{"named_values",`SELECT svals(hs => 'a=>x'::hstore)`,"svals","42883","function svals(hs => hstore) does not exist","No function matches the given name and argument types. You might need to add explicit type casts."},
		{"named_each_invalid",`SELECT each(missing => 'a=>x'::hstore)`,"each","42883","function each(missing => hstore) does not exist","No function matches the given name and argument types. You might need to add explicit type casts."},
		{"bad_literal",`SELECT skeys('a=b'::hstore)`,"'a=b'","42601",`syntax error in hstore, near "b" at position 2`,""},
		{"where",`SELECT 1 WHERE skeys('a=>x'::hstore) IS NOT NULL`,"skeys","0A000","set-returning functions are not allowed in WHERE",""},
		{"typed_out",`SELECT * FROM each('a=>x'::hstore) AS e(key text,value text)`,"key text","42601","a column definition list is redundant for a function with OUT parameters",""},
		{"case",`SELECT CASE WHEN true THEN skeys('a=>x'::hstore) ELSE 'z' END`,"skeys","0A000","set-returning functions are not allowed in CASE","You might be able to move the set-returning function into a LATERAL FROM item."},
		{"coalesce",`SELECT COALESCE(svals('a=>x'::hstore),'z')`,"svals","0A000","set-returning functions are not allowed in COALESCE","You might be able to move the set-returning function into a LATERAL FROM item."},
	} {
		t.Run(tc.name,func(t *testing.T){
			hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
				hstoreInstall(t,p,b,c)
				q:="/*雪*/ "+tc.sql
				want:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message)
				want.Position=strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q,tc.token)])+1)
				want.Hint=tc.hint
				for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,q,prepared,true,true,want)}
			})
		})
	}
}
