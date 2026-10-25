package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceLineDiagnostics(t *testing.T) {
	lineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE line_errors(id integer,l line)", 0))
		cases := []struct{ input, code, message string }{}
		for _, v := range []string{"", " ", "{}", "{0}", "{0,0}", "{0,0,1", "{1,2,3,4}", "{0,0,1} x", "{a,2,3}", "1,2,3", "1,2,3,4,5", "(1;2),(3,4)", "[(1,2),(3,4)", "(1,2),(3,4)junk", "((1,2)),((3,4))", "[(1,2,3,4]", "[ ((1,2),(3,4)]"} {
			cases = append(cases, struct{ input, code, message string }{v, "22P02", "invalid input syntax for type line: \"" + v + "\""})
		}
		for _, v := range []string{"{0,0,1}", "{0.000001,-0.000001,3}"} {
			cases = append(cases, struct{ input, code, message string }{v, "22P02", "invalid line specification: A and B cannot both be zero"})
		}
		for _, v := range []string{"(1,2),(1,2)", "(1,2),(1.0000005,2.0000005)", "(0,0),(0.000001,0.000001)", "(NaN,2),(NaN,2)"} {
			cases = append(cases, struct{ input, code, message string }{v, "22P02", "invalid line specification: must be two distinct points"})
		}
		for _, v := range []struct{ input, token string }{{"{1e400,2,3}", "1e400"}, {"{1,2,1e-4000}", "1e-4000"}, {"(1,2),(1e400,4)", "1e400"}} {
			cases = append(cases, struct{ input, code, message string }{v.input, "22003", "\"" + v.token + "\" is out of range for type double precision"})
		}
		for _, v := range []struct{ input, kind string }{{"(1e308,1e308),(-1e308,-1e308)", "overflow"}, {"(1e-308,0),(1e308,1)", "underflow"}} {
			cases = append(cases, struct{ input, code, message string }{v.input, "22003", "value out of range: " + v.kind})
		}
		for _, tc := range cases {
			t.Run(tc.input, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				base := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				for _, q := range []string{"SELECT line " + boxQuote(tc.input), "SELECT pg_catalog.line " + boxQuote(tc.input), "SELECT /*é*/ line " + boxQuote(tc.input), "SELECT CAST(" + boxQuote(tc.input) + " AS line)", "SELECT " + boxQuote(tc.input) + "::text::line", "SELECT " + boxQuote(tc.input) + "::line FROM line_errors WHERE FALSE", "INSERT INTO line_errors VALUES(1," + boxQuote(tc.input) + ")"} {
					runtime := strings.Contains(q, "::text::")
					for _, prepared := range []bool{false, true} {
						for _, queryMode := range []bool{false, true} {
							d := base
							if !runtime {
								d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
							}
							s := fixture.ErrorDiagnostic(q, d)
							s.SelectRows = queryMode
							s.Prepared = prepared
							s.ErrorStage = "execute"
							if prepared && !runtime {
								s.ErrorStage = "prepare"
							}
							run(s)
						}
					}
				}
				q := "SELECT $1::line AS l"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, arg := range []any{tc.input, []byte(tc.input)} {
					d := base
					d.Context = "unnamed portal parameter $1 = '...'"
					if tc.input == "" {
						d.Context = "unnamed portal parameter $1 = ''"
					}
					s := fixture.ErrorDiagnostic(q, d, arg)
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, lineSelect(b, q, []string{"l"}, []string{"LINE"}, [][]any{{"{2,4,6}"}}, "{2,4,6}"))
					for _, prepared := range []bool{false, true} {
						s.Prepared = prepared
						run(s)
					}
				}
			})
		}
		run(fixture.Exec("INSERT INTO line_errors VALUES(1,'{2,4,6}')", 1))
		q := "INSERT INTO line_errors VALUES(2,$1),(3,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, prepared := range []bool{false, true} {
			d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type line: \"bad\"")
			d.Context = "unnamed portal parameter $2 = '...'"
			s := fixture.ErrorDiagnostic(q, d, "{1,2,3}", "bad")
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
		}
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type line: \"bad\"")
		d.Context = "unnamed portal parameter $2 = '...'"
		s := fixture.ErrorDiagnostic(q, d, "{1,2,3}", "bad")
		s.ErrorStage = "execute"
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		q2 := "INSERT INTO line_errors VALUES(2,'{1,2,3}'),(3,'bad')"
		for _, prepared := range []bool{false, true} {
			run(boxError(q2, "22P02", "invalid input syntax for type line: \"bad\"", fixture.Position(strings.Index(q2, "'bad'")+1), "", prepared, false))
		}
		run(lineSelect(b, "SELECT id,l FROM line_errors ORDER BY id", []string{"id", "l"}, []string{"INT4", "LINE"}, [][]any{{int64(1), "{2,4,6}"}}))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, "{1,2,3}", "{2,4,6}"))
	})
}
