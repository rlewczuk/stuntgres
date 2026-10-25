package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLsegDelimiterBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			q := "SELECT $1::lseg AS s"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				input string
				valid bool
			}{
				{"1,2,3,4, ", false},
				{"[(1,2),(3,4), ]", false},
				{"((1,2),(3,4), )", false},
				{"1,2,3,4,\t", false},
				{"[(1,2),(3,4),\t]", false},
				{"1,2,3,4,", true},
				{"[(1,2),(3,4),]", true},
				{"((1,2),(3,4),)", true},
				{"1,2,3,4 ", true},
				{"[(1,2),(3,4) ]", true},
				{"[(1,2),(3,4)] ", true},
				{"1,2,3,4 ,", true},
				{"[(1,2),(3,4) ,]", true},
			} {
				t.Run(tc.input, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						for _, literal := range []string{"SELECT lseg " + boxQuote(tc.input) + " AS s", "SELECT " + boxQuote(tc.input) + "::lseg AS s"} {
							if tc.valid {
								s := boxSelect(b, literal, []string{"s"}, []string{"LSEG"}, [][]any{{"[(1,2),(3,4)]"}})
								s.Prepared = prepared
								fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
							} else {
								fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(literal, "22P02", "invalid input syntax for type lseg: \""+tc.input+"\"", fixture.Position(strings.Index(literal, "'")+1), "", prepared, false))
							}
						}
						for _, arg := range []any{tc.input, []byte(tc.input)} {
							s := lsegDelimiterBind(b, q, tc.input, tc.valid, arg)
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						}
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, lsegDelimiterBind(b, q, tc.input, tc.valid, tc.input))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, q, []string{"s"}, []string{"LSEG"}, [][]any{{"[(3,4),(1,2)]"}}, "3,4,1,2"))
				})
			}
		})
	}
}

func lsegDelimiterBind(b fixture.Backend, q, input string, valid bool, arg any) fixture.Step {
	if valid {
		return boxSelect(b, q, []string{"s"}, []string{"LSEG"}, [][]any{{"[(1,2),(3,4)]"}}, arg)
	}
	d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type lseg: \""+input+"\"")
	d.Context = "unnamed portal parameter $1 = '...'"
	s := fixture.ErrorDiagnostic(q, d, arg)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}
