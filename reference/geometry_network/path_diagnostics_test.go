package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePathDiagnostics(t *testing.T) {
	pathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE path_errors(id integer,p path)", 0))
		cases := []struct{ input, code, message string }{}
		for _, v := range []string{"", " ", "[]", "()", "[()]", "[(,2),(3,4)]", "[(1,2),(3)]", "[(1,2,6),(3,4,6)]", "[(1,2),(3,4)", "(1,2,3,4", "(1,2),(3,4)]", "(((1,2)))", "[(1,2),(3,4)]junk", "1e400,2,3"} {
			cases = append(cases, struct{ input, code, message string }{v, "22P02", "invalid input syntax for type path: \"" + v + "\""})
		}
		for _, token := range []string{"1e400", "1e-4000"} {
			cases = append(cases, struct{ input, code, message string }{"(" + token + ",2)", "22003", "\"" + token + "\" is out of range for type double precision"})
		}
		for _, tc := range cases {
			t.Run(tc.input, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				base := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				for _, q := range []string{"SELECT path " + boxQuote(tc.input), "SELECT pg_catalog.path " + boxQuote(tc.input), "SELECT /*é*/ path " + boxQuote(tc.input), "SELECT CAST(" + boxQuote(tc.input) + " AS path)", "SELECT " + boxQuote(tc.input) + "::text::path", "SELECT " + boxQuote(tc.input) + "::path FROM path_errors WHERE FALSE", "INSERT INTO path_errors VALUES(1," + boxQuote(tc.input) + ")"} {
					runtime := strings.Contains(q, "::text::")
					for _, prepared := range []bool{false, true} {
						for _, queryMode := range []bool{false, true} {
							d := base
							if !runtime {
								d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
							}
							s := fixture.ErrorDiagnostic(q, d)
							s.SelectRows, s.Prepared, s.ErrorStage = queryMode, prepared, "execute"
							if prepared && !runtime {
								s.ErrorStage = "prepare"
							}
							run(s)
						}
					}
				}
				q := "SELECT $1::path AS p"
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
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, pathSelect(b, q, []string{"p"}, []string{"PATH"}, [][]any{{"[(1,2)]"}}, "[1,2]"))
					for _, prepared := range []bool{false, true} {
						s.Prepared = prepared
						run(s)
					}
				}
			})
		}
		run(fixture.Exec("INSERT INTO path_errors VALUES(1,'[1,2]')", 1))
		q := "INSERT INTO path_errors VALUES(2,$1),(3,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type path: \"[]\"")
		d.Context = "unnamed portal parameter $2 = '...'"
		s := fixture.ErrorDiagnostic(q, d, "[3,4]", "[]")
		s.ErrorStage = "execute"
		for _, prepared := range []bool{false, true} {
			s.Prepared = prepared
			run(s)
		}
		s.Prepared = false
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		q2 := "INSERT INTO path_errors VALUES(2,'[3,4]'),(3,'[]')"
		for _, prepared := range []bool{false, true} {
			run(boxError(q2, "22P02", d.Message, fixture.Position(strings.Index(q2, "'[]'")+1), "", prepared, false))
		}
		run(pathSelect(b, "SELECT id,p FROM path_errors ORDER BY id", []string{"id", "p"}, []string{"INT4", "PATH"}, [][]any{{int64(1), "[(1,2)]"}}))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, "[3,4]", "(5,6)"))
	})
}
