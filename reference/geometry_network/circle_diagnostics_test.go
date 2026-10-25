package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceCircleDiagnostics(t *testing.T) {
	bad := []string{"bad", "", "1", "1,2", "1,,3", ",2,3", "1 2,3", "(1,2,3", "<(1,2),3", "<(1,2),3>>", "1,2,3 junk", "0,0,-1", "0,0,-Infinity", "(x,2),3", "1,2,z"}
	circleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for i, input := range bad {
			t.Run(fmt.Sprintf("input_%d", i), func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					want := circleInputDiagnostic(input)
					want.Position = "8"
					s := fixture.ErrorDiagnostic("SELECT "+circleLiteral(input)+"::circle", want)
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
			t.Run(fmt.Sprintf("parameter_%d", i), func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					want := circleInputDiagnostic(input)
					text := "..."
					if input == "" {
						text = ""
					}
					want.Context = "unnamed portal parameter $1 = '" + text + "'"
					s := fixture.ErrorDiagnostic("SELECT $1::circle", want, input)
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		for _, tc := range []struct{ input, token string }{{"1e999,0,1", "1e999"}, {"0,0,1e-999", "1e-999"}, {"1e999;broken", "1e999"}, {"0,1e999;broken", "1e999"}} {
			t.Run("range_"+tc.input, func(t *testing.T) {
				want := circleDiagnostic("22003", `"`+tc.token+`" is out of range for type double precision`)
				want.Position = "8"
				s := fixture.ErrorDiagnostic("SELECT "+circleLiteral(tc.input)+"::circle", want)
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
		for _, q := range []string{"SELECT CAST('bad' AS circle)", "/*é*/ SELECT 'bad'::circle"} {
			want := circleInputDiagnostic("bad")
			want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'bad'")]) + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, want)
				s.SelectRows = true
				s.ErrorStage = "execute"
				if prepared {
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE circle_atomic(id integer,v circle)", 0))
		run(fixture.Exec("INSERT INTO circle_atomic VALUES(1,'0,0,1')", 1))
		q := "INSERT INTO circle_atomic VALUES(2,'0,0,2'),(3,'bad')"
		want := circleInputDiagnostic("bad")
		want.Position = strconv.Itoa(strings.Index(q, "'bad'") + 1)
		run(fixture.ErrorDiagnostic(q, want))
		want = circleInputDiagnostic("bad")
		want.Context = "unnamed portal parameter $2 = '...'"
		s := fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO circle_atomic VALUES(2,$1),(3,$2)", want, "0,0,2", "bad"))
		s.ErrorStage = "execute"
		run(s)
		run(circleSelect(b, "SELECT id,v FROM circle_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "CIRCLE"}, [][]any{{int64(1), "<(0,0),1>"}}))
	})
}
