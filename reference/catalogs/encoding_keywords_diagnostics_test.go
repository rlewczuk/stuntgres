package reference_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
	"stuntgres/reference/fixture"
)

// Complete diagnostics are independently checked against pinned PostgreSQL18.6.
func TestReferenceEncodingKeywordsDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name,func(t *testing.T) {
			conn,err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer conn.Close()
			type sample struct { sql,code,message,hint,at,context string; args []any }
			var cases []sample
			hint := "No function matches the given name and argument types. You might need to add explicit type casts."
			for _, c := range []struct{call,signature string}{
				{"pg_char_to_encoding()","pg_char_to_encoding()"},
				{"pg_char_to_encoding(1)","pg_char_to_encoding(integer)"},
				{"pg_char_to_encoding(true)","pg_char_to_encoding(boolean)"},
				{"pg_char_to_encoding('UTF8','UTF8')","pg_char_to_encoding(unknown, unknown)"},
				{"pg_encoding_to_char()","pg_encoding_to_char()"},
				{"pg_encoding_to_char(true)","pg_encoding_to_char(boolean)"},
				{"pg_encoding_to_char(1::bigint)","pg_encoding_to_char(bigint)"},
				{"pg_encoding_to_char('6'::text)","pg_encoding_to_char(text)"},
				{"pg_encoding_to_char(1,2)","pg_encoding_to_char(integer, integer)"},
				{"pg_get_keywords(NULL)","pg_get_keywords(unknown)"},
				{"pg_get_keywords(1)","pg_get_keywords(integer)"},
				{"public.pg_char_to_encoding('UTF8')","public.pg_char_to_encoding(unknown)"},
				{"public.pg_get_keywords()","public.pg_get_keywords()"},
				{`"PG_CHAR_TO_ENCODING"('UTF8')`,"PG_CHAR_TO_ENCODING(unknown)"},
				{"pg_char_to_encoding_missing('UTF8')","pg_char_to_encoding_missing(unknown)"},
				{"pg_char_to_encoding(encoding => 'UTF8')","pg_char_to_encoding(encoding => unknown)"},
				{"pg_encoding_to_char(encoding => 6)","pg_encoding_to_char(encoding => integer)"},
				{"pg_get_keywords(word => 'select')","pg_get_keywords(word => unknown)"},
				{"pg_encoding_to_char(DISTINCT true)","pg_encoding_to_char(boolean)"},
			} { cases = append(cases,sample{sql:"SELECT "+c.call,code:"42883",message:"function "+c.signature+" does not exist",hint:hint,at:c.call}) }
			for _, prefix := range []string{"SELECT ","SELECT /* é界 */ "} {
				cases = append(cases,
					sample{sql:prefix+"pg_encoding_to_char(true)",code:"42883",message:"function pg_encoding_to_char(boolean) does not exist",hint:hint,at:"pg_encoding_to_char"},
					sample{sql:prefix+"pg_encoding_to_char('bad')",code:"22P02",message:`invalid input syntax for type integer: "bad"`,at:"'bad'"},
					sample{sql:prefix+"pg_encoding_to_char('2147483648')",code:"22003",message:`value "2147483648" is out of range for type integer`,at:"'2147483648'"},
				)
			}
			for _, c := range []struct{value,code,message string}{
				{"bad","22P02",`invalid input syntax for type integer: "bad"`},
				{"2147483648","22003",`value "2147483648" is out of range for type integer`},
			} { cases = append(cases,sample{sql:"SELECT pg_encoding_to_char($1)",code:c.code,message:c.message,context:`unnamed portal parameter $1 = '...'`,args:[]any{c.value}}) }
			for _, c := range []struct{sql,message string}{
				{"pg_get_keywords(*)","pg_get_keywords(*) specified, but pg_get_keywords is not an aggregate function"},
				{"pg_encoding_to_char(DISTINCT 6)","DISTINCT specified, but pg_encoding_to_char is not an aggregate function"},
				{"pg_encoding_to_char(6 ORDER BY 6)","ORDER BY specified, but pg_encoding_to_char is not an aggregate function"},
				{"pg_encoding_to_char(6) FILTER (WHERE true)","FILTER specified, but pg_encoding_to_char is not an aggregate function"},
				{"pg_encoding_to_char(6) OVER ()","OVER specified, but pg_encoding_to_char is not a window function nor an aggregate function"},
				{"pg_get_keywords() FILTER (WHERE true)","FILTER specified, but pg_get_keywords is not an aggregate function"},
				{"pg_get_keywords() OVER ()","OVER specified, but pg_get_keywords is not a window function nor an aggregate function"},
			} { cases = append(cases,sample{sql:"SELECT "+c.sql,code:"42809",message:c.message,at:strings.Split(c.sql,"(")[0]}) }
			cases = append(cases,
				sample{sql:"SELECT 1 WHERE pg_get_keywords() IS NOT NULL",code:"0A000",message:"set-returning functions are not allowed in WHERE",at:"pg_get_keywords"},
				sample{sql:"SELECT /* é界 */ 1 WHERE pg_get_keywords() IS NOT NULL",code:"0A000",message:"set-returning functions are not allowed in WHERE",at:"pg_get_keywords"},
				sample{sql:"SELECT * FROM pg_get_keywords() AS k(a,b,c,d,e,f)",code:"42P10",message:`table "k" has 5 columns available but 6 columns specified`},
				sample{sql:"SELECT * FROM pg_get_keywords() AS k(word text)",code:"42601",message:"a column definition list is redundant for a function with OUT parameters",at:"word text"},
			)
			for i,c := range cases {
				for _, prepared := range []bool{false,true} {
					t.Run(fmt.Sprintf("case%d/prepared=%t",i,prepared),func(t *testing.T) {
						position := ""
						if c.at != "" { index := strings.Index(c.sql,c.at); if index < 0 { t.Fatal("missing position anchor") }; position = fixture.Position(utf8.RuneCountInString(c.sql[:index])+1) }
						s := fixture.ImplicitError(c.sql,fixture.Diagnostic{Code:c.code,Message:c.message,Hint:c.hint,Position:position,Context:c.context},c.args...)
						s.SelectRows,s.Prepared,s.ErrorStage = true,prepared,"execute"
						if prepared && len(c.args) == 0 { s.ErrorStage = "prepare" }
						fixture.RunStep(t,p.Ctx,conn,b.Recorder,s)
					})
				}
			}
			fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Select("SELECT pg_encoding_to_char(6) AS encoding",[]string{"encoding"},[]string{"NAME"},[][]any{{"UTF8"}}))
		})
	}
}
