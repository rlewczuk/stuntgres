package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePathWrapperStages(t *testing.T) {
	pathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "SELECT $1::path AS p"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		// Released path_in removes one single leading parenthesis, then
		// path_decode independently recognizes the open square-bracket list.
		for _, input := range []string{"([1,2])", " ( \t[ 1 , 2 ] \n) ", "([1,2,3,4])", "([1,2))"} {
			want := "[(1,2)]"
			if strings.Contains(input, "3,4") {
				want = "[(1,2),(3,4)]"
			}
			for _, prepared := range []bool{false, true} {
				s := pathSelect(b, "SELECT path "+boxQuote(input)+" AS p", []string{"p"}, []string{"PATH"}, [][]any{{want}})
				s.Prepared = prepared
				run(s)
				s = pathSelect(b, q, []string{"p"}, []string{"PATH"}, [][]any{{want}}, input)
				s.Prepared = prepared
				run(s)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pathSelect(b, q, []string{"p"}, []string{"PATH"}, [][]any{{want}}, []byte(input)))
		}
		for _, input := range []string{"([1,2]", "([1,2)", "([1,2])junk", "(([1,2]))"} {
			for _, prepared := range []bool{false, true} {
				literal := "SELECT path " + boxQuote(input)
				run(boxError(literal, "22P02", "invalid input syntax for type path: \""+input+"\"", "13", "", prepared, false))
			}
			d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type path: \""+input+"\"")
			d.Context = "unnamed portal parameter $1 = '...'"
			s := fixture.ErrorDiagnostic(q, d, input)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pathSelect(b, q, []string{"p"}, []string{"PATH"}, [][]any{{"[(1,2)]"}}, "([1,2])"))
		}
	})
}
