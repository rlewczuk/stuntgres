package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceBoxDiagnostics(t *testing.T) {
	boxScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range []struct{ source, target, expression string }{
			{"integer", "box", "1"},
			{"integer", "point", "1"},
			{"box", "integer", "(box '1,1,0,0')"},
			{"point", "integer", "(point '(1,2)')"},
		} {
			for _, cast := range []struct{ query, token string }{
				{"SELECT " + tc.expression + "::" + tc.target, "::"},
				{"SELECT CAST(" + tc.expression + " AS " + tc.target + ")", "CAST"},
			} {
				position := fixture.Position(1 + utf8.RuneCountInString(cast.query[:strings.Index(cast.query, cast.token)]))
				for _, prepared := range []bool{false, true} {
					run(boxError(cast.query, "42846", "cannot cast type "+tc.source+" to "+tc.target, position, "", prepared, false))
				}
			}
		}
		run(fixture.Exec("CREATE TABLE box_errors(id integer,b box)", 0))
		cases := []struct{ input, code, message string }{}
		for _, input := range []string{"", " ", "[1,2,3,4]", "1,2,3", "1,2,3,4,5", "(1;2),(3,4)", "(1,2),(3,4)junk", "((1,2),(3,4)", "(1,2),(3,", "a,2,3,4", "((1,2)),((3,4))", "(1,2),3,4"} {
			cases = append(cases, struct{ input, code, message string }{input, "22P02", "invalid input syntax for type box: \"" + input + "\""})
		}
		for _, tc := range []struct{ input, token string }{{"1e400,2,3,4", "1e400"}, {"1e-4000,2,3,4", "1e-4000"}, {"(1,2),(1e400,4)", "1e400"}, {"(1e400;2),(3,4)", "1e400"}} {
			cases = append(cases, struct{ input, code, message string }{tc.input, "22003", "\"" + tc.token + "\" is out of range for type double precision"})
		}
		for _, tc := range cases {
			t.Run(tc.input, func(t *testing.T) {
				base := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				for _, query := range []string{
					"SELECT box " + boxQuote(tc.input), "SELECT pg_catalog.box " + boxQuote(tc.input),
					"SELECT /*é*/ box " + boxQuote(tc.input), "SELECT CAST(" + boxQuote(tc.input) + " AS box)",
					"SELECT " + boxQuote(tc.input) + "::text::box", "SELECT " + boxQuote(tc.input) + "::box FROM box_errors WHERE FALSE",
					"INSERT INTO box_errors VALUES(1," + boxQuote(tc.input) + ")",
				} {
					runtime := strings.Contains(query, "::text::")
					for _, prepared := range []bool{false, true} {
						for _, queryMode := range []bool{false, true} {
							d := base
							if !runtime {
								d.Position = fixture.Position(utf8.RuneCountInString(query[:strings.Index(query, "'")]) + 1)
							}
							s := fixture.ErrorDiagnostic(query, d)
							s.SelectRows, s.Prepared, s.ErrorStage = queryMode, prepared, "execute"
							if prepared && !runtime {
								s.ErrorStage = "prepare"
							}
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						}
					}
				}
				for _, arg := range []any{tc.input, []byte(tc.input)} {
					for _, prepared := range []bool{false, true} {
						for _, queryMode := range []bool{false, true} {
							d := base
							d.Context = "unnamed portal parameter $1 = '...'"
							if tc.input == "" {
								d.Context = "unnamed portal parameter $1 = ''"
							}
							s := fixture.ErrorDiagnostic("SELECT $1::box", d, arg)
							s.SelectRows, s.Prepared, s.ErrorStage = queryMode, prepared, "execute"
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						}
					}
				}
				q := "SELECT $1::box AS b"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				d := base
				d.Context = "unnamed portal parameter $1 = '...'"
				if tc.input == "" {
					d.Context = "unnamed portal parameter $1 = ''"
				}
				s := fixture.ErrorDiagnostic(q, d, tc.input)
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, boxSelect(b, q, []string{"b"}, []string{"BOX"}, [][]any{{"(3,4),(1,2)"}}, "1,4,3,2"))
			})
		}
		run(fixture.Exec("INSERT INTO box_errors VALUES(1,'1,4,3,2')", 1))
		for _, prepared := range []bool{false, true} {
			query := "INSERT INTO box_errors VALUES(2,$1),(3,$2)"
			d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type box: \"bad\"")
			d.Context = "unnamed portal parameter $2 = '...'"
			s := fixture.ErrorDiagnostic(query, d, "1,1,0,0", "bad")
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			run(boxSelect(b, "SELECT id,b FROM box_errors ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOX"}, [][]any{{int64(1), "(3,4),(1,2)"}}))
		}
		query := "INSERT INTO box_errors VALUES(2,'1,1,0,0'),(3,'bad')"
		run(boxError(query, "22P02", "invalid input syntax for type box: \"bad\"", fixture.Position(strings.Index(query, "'bad'")+1), "", false, false))
		run(boxSelect(b, "SELECT id,b FROM box_errors ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOX"}, [][]any{{int64(1), "(3,4),(1,2)"}}))
	})
}
