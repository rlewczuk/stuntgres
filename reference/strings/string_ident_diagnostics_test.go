package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func stringIdentDiagnostic(input, detail string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("22023", `string is not a valid identifier: "`+input+`"`)
	d.Detail = detail
	return d
}

func TestReferenceStringIdentDiagnostics(t *testing.T) {
	stringIdentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ input, detail string }{
			{"", ""}, {" \t\r\n\v\f", ""}, {"123", ""}, {"$foo", ""}, {"()", ""},
			{"foo.", `No valid identifier after ".".`}, {"foo.123", `No valid identifier after ".".`}, {"foo.()", `No valid identifier after ".".`},
			{".foo", `No valid identifier before ".".`}, {"foo..bar", `No valid identifier before ".".`}, {"foo. .bar", `No valid identifier before ".".`},
			{`""`, "Quoted identifier must not be empty."}, {`foo.""`, "Quoted identifier must not be empty."},
			{`"foo`, "String has unclosed double quotes."}, {`foo."bar`, "String has unclosed double quotes."}, {`"""`, "String has unclosed double quotes."},
		} {
			t.Run(tc.input, func(t *testing.T) {
				for _, strict := range []bool{false, true} {
					for _, prefix := range []string{"", "/*é🙂*/ "} {
						for _, prepared := range []bool{false, true} {
							s := fixture.ErrorDiagnostic(prefix+"SELECT parse_ident($1,$2)", stringIdentDiagnostic(tc.input, tc.detail), tc.input, strict)
							s.SelectRows = true
							s.ErrorStage = "execute"
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						}
					}
				}
			})
		}
		for _, input := range []string{`foo.boo[]`, `foo(integer)`, `foo bar`, `foo/*x*/`, `foo"unterminated`, `U&"x"`} {
			for _, q := range []string{"SELECT parse_ident($1)", "SELECT parse_ident($1,true)"} {
				s := fixture.ErrorDiagnostic(q, stringIdentDiagnostic(input, ""), input)
				s.SelectRows = true
				s.ErrorStage = "execute"
				stringReplaceBoth(t, p, b, c, s)
			}
		}
		// Constants retain the ordinary execute-stage function error, not a
		// fabricated parser position. Prepare itself must remain successful.
		stringReplaceFailure(t, p, b, c, `SELECT parse_ident('a.')`, stringIdentDiagnostic("a.", `No valid identifier after ".".`), false)
	})
}

func TestReferenceStringIdentBindingDiagnostics(t *testing.T) {
	stringIdentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ call, signature string }{
			{`parse_ident()`, `parse_ident()`},
			{`parse_ident('a',true,false)`, `parse_ident(unknown, boolean, boolean)`},
			{`parse_ident('a',1)`, `parse_ident(unknown, integer)`},
			{`parse_ident(1)`, `parse_ident(integer)`},
			{`parse_ident(true)`, `parse_ident(boolean)`},
			{`parse_ident('a'::bytea)`, `parse_ident(bytea)`},
			{`parse_ident('a','false'::text)`, `parse_ident(unknown, text)`},
			{`parse_ident(strict=>false)`, `parse_ident(strict => boolean)`},
			{`parse_ident(qualified_identifier=>'a')`, `parse_ident(qualified_identifier => unknown)`},
			{`parse_ident('a',strict_mode=>false)`, `parse_ident(unknown, strict_mode => boolean)`},
			{`parse_ident("STR"=>'a')`, `parse_ident(STR => unknown)`},
			{`parse_ident('a',str=>'b')`, `parse_ident(unknown, str => unknown)`},
			{`"PARSE_IDENT"('a')`, `PARSE_IDENT(unknown)`},
			{`public.parse_ident('a')`, `public.parse_ident(unknown)`},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.call, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(8 + len([]rune(prefix)))
					stringReplaceFailure(t, p, b, c, prefix+"SELECT "+tc.call, d, true)
				})
			}
		}
		for _, tc := range []struct {
			call, message string
			position      int
		}{
			{`parse_ident(str=>'a',str=>'b')`, `argument name "str" used more than once`, 29},
			{`parse_ident(str=>'a',false)`, `positional argument cannot follow named argument`, 29},
		} {
			d := fixture.ControlsReferenceDiagnostic("42601", tc.message)
			d.Position = fixture.Position(tc.position)
			stringReplaceFailure(t, p, b, c, "SELECT "+tc.call, d, true)
		}
		for _, source := range []string{"'a'", "NULL"} {
			d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "bogus"`)
			d.Position = "24"
			if source == "NULL" {
				d.Position = "25"
			}
			stringReplaceFailure(t, p, b, c, "SELECT parse_ident("+source+",'bogus')", d, true)
		}
		q := "SELECT parse_ident($1,$2)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "bogus"`)
		d.Context = "unnamed portal parameter $2 = '...'"
		s := fixture.ErrorDiagnostic(q, d, "A.B", "bogus")
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: st, SQL: q}, b.Recorder, s)
		for _, tc := range []struct{ q, message string }{
			{`SELECT parse_ident(DISTINCT 'a')`, `DISTINCT specified, but parse_ident is not an aggregate function`},
			{`SELECT parse_ident('a' ORDER BY 'a')`, `ORDER BY specified, but parse_ident is not an aggregate function`},
			{`SELECT parse_ident('a') FILTER (WHERE true)`, `FILTER specified, but parse_ident is not an aggregate function`},
			{`SELECT parse_ident('a') OVER ()`, `OVER specified, but parse_ident is not a window function nor an aggregate function`},
		} {
			d := fixture.ControlsReferenceDiagnostic("42809", tc.message)
			d.Position = "8"
			stringReplaceFailure(t, p, b, c, tc.q, d, true)
		}
		d = fixture.ControlsReferenceDiagnostic("42883", "function parse_ident(text[]) does not exist")
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		d.Position = "8"
		stringReplaceFailure(t, p, b, c, `SELECT parse_ident(VARIADIC ARRAY['a'])`, d, true)
	})
}
