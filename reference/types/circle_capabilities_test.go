package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCircleCapabilities(t *testing.T) {
	circleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE circle_caps(v circle)", 0))
		for _, tc := range []struct{ q, message, hint, at string }{
			{"SELECT v FROM circle_caps ORDER BY v", "could not identify an ordering operator for type circle", "Use an explicit ordering operator or modify the query.", "v"},
			{"SELECT DISTINCT v FROM circle_caps", "could not identify an equality operator for type circle", "", "v"},
			{"SELECT v FROM circle_caps GROUP BY v", "could not identify an equality operator for type circle", "", "v"},
			{"CREATE INDEX circle_btree ON circle_caps(v)", "data type circle has no default operator class for access method \"btree\"", "You must specify an operator class for the index or define a default operator class for the data type.", ""},
			{"CREATE INDEX circle_hash ON circle_caps USING hash(v)", "data type circle has no default operator class for access method \"hash\"", "You must specify an operator class for the index or define a default operator class for the data type.", ""},
		} {
			t.Run(tc.q, func(t *testing.T) {
				want := circleDiagnostic("42883", tc.message)
				want.Hint = tc.hint
				if tc.at != "" {
					want.Position = strconv.Itoa(strings.LastIndex(tc.q, tc.at) + 1)
				}
				for _, prepared := range []bool{false, true} {
					if strings.HasPrefix(tc.q, "CREATE INDEX") {
						want.Code = "42704"
					}
					s := fixture.ErrorDiagnostic(tc.q, want)
					s.SelectRows = strings.HasPrefix(tc.q, "SELECT")
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						if s.SelectRows {
							s.ErrorStage = "prepare"
						}
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		q := "SELECT NULL::circle + NULL::circle"
		want := circleDiagnostic("42883", "operator does not exist: circle + circle")
		want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		want.Position = "21"
		s := fixture.ErrorDiagnostic(q, want)
		s.SelectRows = true
		run(s)
		s = fixture.Prepared(s)
		s.ErrorStage = "prepare"
		run(s)
		run(fixture.Select("SELECT ('circle'::regtype)::oid::integer AS scalar,('circle[]'::regtype)::oid::integer AS array", []string{"scalar", "array"}, []string{"INT4", "INT4"}, [][]any{{int64(718), int64(719)}}))
	})
}
