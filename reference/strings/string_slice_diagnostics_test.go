package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringSliceDiagnostics(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, expr := range []string{"substring('abc',1,-1)", "substring('',1,-1)", "substring('abc',99,-1)", "substr('abc',-9,-1)"} {
			t.Run(expr, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22011", "negative substring length not allowed")
				stringPadFailure(t, p, b, c, "SELECT "+expr, d, false)
			})
		}
		for _, tc := range []struct{ call, signature string }{
			{"left('abc',2::bigint)", "left(unknown, bigint)"}, {"right('abc','2'::text)", "right(unknown, text)"},
			{"left(1,2)", "left(integer, integer)"}, {"substr('abc',2::numeric)", "substr(unknown, numeric)"},
			{"substring('abc',2,2::double precision)", "substring(unknown, integer, double precision)"},
			{"substr('a'::text,'a'::text)", "substr(text, text)"},
			{`"LEFT"('a',1)`, "LEFT(unknown, integer)"}, {"left('a')", "left(unknown)"},
			{"right('a',1,2)", "right(unknown, integer, integer)"}, {"substr()", "substr()"},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.call, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = fixture.Position(8 + len([]rune(prefix)))
					stringPadFailure(t, p, b, c, prefix+"SELECT "+tc.call, d, true)
				})
			}
		}
		for _, tc := range []struct {
			q, code, message string
			pos              int
		}{
			{"SELECT left('a','bad')", "22P02", `invalid input syntax for type integer: "bad"`, 17},
			{"SELECT right('a','2147483648')", "22003", `value "2147483648" is out of range for type integer`, 18},
			{"SELECT substr('a','a')", "22P02", `invalid input syntax for type integer: "a"`, 19},
			{"SELECT no_slice_schema.left('a',1)", "3F000", `schema "no_slice_schema" does not exist`, 8},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.q, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Position = fixture.Position(tc.pos + len([]rune(prefix)))
					stringPadFailure(t, p, b, c, prefix+tc.q, d, true)
				})
			}
		}
		for _, tc := range []struct{ q, code, message string }{
			{"SELECT substring('abc','[')", "2201B", "invalid regular expression: brackets [] not balanced"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				stringPadFailure(t, p, b, c, tc.q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), false)
			})
		}
	})
}

func TestReferenceStringSliceHeldParameters(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"SELECT substring($1,$2::int,$3::int) AS v", "SELECT substr($1,$2,$3) AS v"} {
			t.Run(q, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, tc := range []struct {
					s            any
					start, count any
					want         any
				}{
					{"abcde", 2, 3, "bcd"}, {"Aé🙂Z", 2, 2, "é🙂"}, {nil, 1, -1, nil}, {"abc", 1, nil, nil},
				} {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.s, tc.start, tc.count))
				}
				for _, tc := range []struct{ value, code, message string }{
					{"bad", "22P02", `invalid input syntax for type integer: "bad"`},
					{"2147483648", "22003", `value "2147483648" is out of range for type integer`},
				} {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Context = "unnamed portal parameter $2 = '...'"
					s := fixture.ErrorDiagnostic(q, d, "abc", tc.value, 1)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"c"}}, "abc", 3, 9))
			})
		}
		for _, tc := range []struct {
			q    string
			args []any
			want any
		}{
			{"SELECT left($1,$2) AS v", []any{"abc", 2}, "ab"},
			{"SELECT right($1,$2) AS v", []any{"abc", -1}, "bc"},
			{"SELECT substring($1,$2::text) AS v", []any{"foobar", "o(.)b"}, "o"},
			{`SELECT substring($1,$2::text,$3::text) AS v`, []any{"foobar", `%#"o_b#"%`, "#"}, "oob"},
		} {
			stringPadBoth(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.args...))
		}
		for _, tc := range []struct{ arg, message string }{
			{string([]byte{'a', 0}), `invalid byte sequence for encoding "UTF8": 0x00`},
			{string([]byte{0xff}), `invalid byte sequence for encoding "UTF8": 0xff`},
		} {
			t.Run(tc.message, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22021", tc.message)
				d.Context = "unnamed portal parameter $1"
				s := fixture.ErrorDiagnostic("SELECT left($1,1)", d, tc.arg)
				s.SelectRows, s.ErrorStage = true, "execute"
				stringPadBoth(t, p, b, c, s)
			})
		}
	})
}

func TestReferenceStringSliceInference(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// PostgreSQL18.0 retains the source for right(INT_MIN), unlike ordinary
		// negative magnitudes at least as large as the character count.
		for _, s := range []fixture.Step{
			fixture.Select("SELECT right('abc',-2147483648),right('Aé🙂Z',-2147483648),right('',-2147483648),left('abc',-2147483648)", []string{"right", "right", "right", "left"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"abc", "Aé🙂Z", "", ""}}),
			fixture.Select("SELECT right($1,$2::int),left($1,$2::int)", []string{"right", "left"}, []string{"TEXT", "TEXT"}, [][]any{{"Aé🙂Z", ""}}, "Aé🙂Z", int64(-2147483648)),
			fixture.Select("SELECT right($1,$4::int),right($2,$4::int),right($3,$4::int),left($1,$4::int)", []string{"right", "right", "right", "left"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"abc", "Aé🙂Z", "", ""}}, "abc", "Aé🙂Z", "", int64(-2147483648)),
			fixture.Select("SELECT substring($1,$2)", []string{"substring"}, []string{"TEXT"}, [][]any{{"o"}}, "foobar", "o(.)b"),
			fixture.Select("SELECT substring($1,$2,$3)", []string{"substring"}, []string{"TEXT"}, [][]any{{"oob"}}, "foobar", `%#"o_b#"%`, "#"),
		} {
			t.Run(s.Query, func(t *testing.T) { stringPadBoth(t, p, b, c, s) })
		}
	})
}

func TestReferenceStringSliceResolutionDiagnostics(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ q, signature string }{
			{"SELECT left(string => 'abc', n => 1)", "left(string => unknown, n => integer)"},
			{"SELECT substr(string => 'abc', start => 1)", "substr(string => unknown, start => integer)"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
				d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				d.Position = "8"
				stringPadFailure(t, p, b, c, tc.q, d, true)
			})
		}
		for _, tc := range []struct{ q, message, position string }{
			{"SELECT substring('abc' FROM)", `syntax error at or near ")"`, "28"},
			{"SELECT left('abc',1) trailing junk", `syntax error at or near "junk"`, "31"},
			{"SELECT right('abc',)", `syntax error at or near ")"`, "20"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42601", tc.message)
				d.Position = tc.position
				stringPadFailure(t, p, b, c, tc.q, d, true)
			})
		}
		for _, tc := range []struct{ q, code, message, hint string }{
			{`SELECT substring('abc','%','xx')`, "22025", "invalid escape string", "Escape string must be empty or one character."},
			{`SELECT substring('abc','#"a#"b#"c','#')`, "2200C", "SQL regular expression may not contain more than two escape-double-quote separators", ""},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Hint = tc.hint
				d.Context = `SQL function "substring" statement 1`
				stringPadFailure(t, p, b, c, tc.q, d, false)
			})
		}
		for _, q := range []string{
			"SELECT CASE WHEN FALSE THEN 'safe' ELSE substring('abc',1,-1) END",
			"SELECT substring('abc',1,-1) WHERE FALSE",
		} {
			t.Run(q, func(t *testing.T) {
				stringPadFailure(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("22011", "negative substring length not allowed"), false)
			})
		}
	})
}
