package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitStringFunctionsTypingDiagnostics(t *testing.T) {
	bitStringFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE bsf_types(v VARBIT,i SMALLINT,b BIGINT,x TEXT,n INTEGER)", 0))
		run(fixture.Exec("INSERT INTO bsf_types VALUES(B'10110',2,2,'2',-1),(NULL,2,2,'2',-1)", 2))
		s := fixture.Select("SELECT substring(v FROM i FOR 2) AS result FROM bsf_types", []string{"result"}, []string{"BIT"}, [][]any{{"01"}, {nil}})
		s.Bag = true
		bitStringFunctionsBoth(t, p, b, c, s)
		s = fixture.Select("SELECT substring(v FOR b) AS result FROM bsf_types", []string{"result"}, []string{"BIT"}, [][]any{{"10"}, {nil}})
		s.Bag = true
		bitStringFunctionsBoth(t, p, b, c, s)
		const missingHint = "No function matches the given name and argument types. You might need to add explicit type casts."
		const ambiguousHint = "Could not choose a best candidate function. You might need to add explicit type casts."
		for _, tc := range []struct {
			q, code, message, hint string
			position               int
		}{
			{"SELECT substring(v FROM b) FROM bsf_types", "42883", "function pg_catalog.substring(bit varying, bigint) does not exist", missingHint, 8},
			{"SELECT substring(v FROM x) FROM bsf_types", "42883", "function pg_catalog.substring(bit varying, text) does not exist", missingHint, 8},
			{"SELECT substring(B'1' FROM 2147483648)", "42883", "function pg_catalog.substring(bit, bigint) does not exist", missingHint, 8},
			{"SELECT substring(B'1' FROM 'bad')", "22P02", `invalid input syntax for type integer: "bad"`, "", 28},
			{"SELECT substring(B'1' FROM '2147483648')", "22003", `value "2147483648" is out of range for type integer`, "", 28},
			{"SELECT bit_count('101')", "42725", "function bit_count(unknown) is not unique", ambiguousHint, 8},
			{"SELECT bit_count(NULL)", "42725", "function bit_count(unknown) is not unique", ambiguousHint, 8},
			{"SELECT bit_count(B'1',1)", "42883", "function bit_count(bit, integer) does not exist", missingHint, 8},
			{"SELECT substr(B'101',1,2)", "42883", "function substr(bit, integer, integer) does not exist", missingHint, 8},
			{`SELECT "BIT_COUNT"(B'1')`, "42883", "function BIT_COUNT(bit) does not exist", missingHint, 8},
			{"SELECT substring(v FROM absent) FROM bsf_types", "42703", `column "absent" does not exist`, "", 25},
			{"SELECT substring(B'102' FROM 1)", "22P02", `"2" is not a valid binary digit`, "", 18},
			{"SELECT substring(v FROM 'bad') FROM bsf_types WHERE v IS NULL", "22P02", `invalid input syntax for type integer: "bad"`, "", 25},
		} {
			for _, prefix := range []string{"", "/*é*/ "} {
				for _, prepared := range []bool{false, true} {
					t.Run(prefix+tc.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						pos := tc.position
						if prefix != "" {
							pos += 6
						}
						s := bitStringFunctionsError(prefix+tc.q, tc.code, tc.message, pos, prepared, true)
						s.Diagnostic.Hint = tc.hint
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					})
				}
			}
		}
		for _, tc := range []struct {
			q, code, message string
			args             []any
		}{
			{"SELECT substring(v FROM $1 FOR $2) FROM bsf_types", "22011", "negative substring length not allowed", []any{1, -1}},
			{"SELECT overlay(v PLACING B'00' FROM $1 FOR $2) FROM bsf_types", "22011", "negative substring length not allowed", []any{0, 1}},
			{"SELECT overlay(v PLACING B'00' FROM $1 FOR $2) FROM bsf_types", "22003", "integer out of range", []any{2147483647, 1}},
			{"SELECT overlay(v PLACING $1 FROM 1) FROM bsf_types", "22P02", `"2" is not a valid binary digit`, []any{"102"}},
			{"SELECT substring(v FROM $1) FROM bsf_types", "22P02", `invalid input syntax for type integer: "bad"`, []any{"bad"}},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.q+tc.code+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					s := bitStringFunctionsError(tc.q, tc.code, tc.message, 0, prepared, false, tc.args...)
					if strings.HasPrefix(tc.code, "22P") {
						s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				})
			}
		}
	})
}
