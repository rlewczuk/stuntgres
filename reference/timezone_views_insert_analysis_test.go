package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func TestReferenceTimezoneViewsInsertAnalysis(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor) {
		for _, view := range []string{"pg_timezone_names", "pg_timezone_abbrevs"} {
			for _, tc := range []struct {
				query, token string
				want         referenceDiagnostic
			}{
				{"INSERT INTO pg_catalog." + view + "(bogus) VALUES(true)", "bogus", referenceDiagnostic{Code: "42703", Message: `column "bogus" of relation "` + view + `" does not exist`}},
				{"INSERT INTO pg_catalog." + view + "(is_dst) VALUES(1)", "1", referenceDiagnostic{Code: "42804", Message: `column "is_dst" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression."}},
				{"INSERT INTO pg_catalog." + view + "(is_dst) VALUES(true+1)", "+", referenceDiagnostic{Code: "42883", Message: "operator does not exist: boolean + integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
				{"INSERT INTO pg_catalog." + view + "(is_dst) VALUES(bogus)", "bogus", referenceDiagnostic{Code: "42703", Message: `column "bogus" does not exist`}},
				{"INSERT INTO pg_catalog." + view + "(is_dst) SELECT 1", "1", referenceDiagnostic{Code: "42804", Message: `column "is_dst" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression."}},
				{"INSERT INTO pg_catalog." + view + "(is_dst) SELECT bogus FROM pg_timezone_abbrevs", "bogus", referenceDiagnostic{Code: "42703", Message: `column "bogus" does not exist`}},
			} {
				tc.want.Position = strconv.Itoa(strings.Index(tc.query, tc.token) + 1)
				if !b.pg && strings.Contains(tc.query, "VALUES(bogus)") {
					// Existing scalar-input capability boundary, not an invented
					// timezone binder or new VALUES column-reference support.
					tc.want.Code = "0A000"
					tc.want.Message = "column references are unsupported in scalar inputs"
				}
				for _, prepared := range []bool{false, true} {
					t.Run(tc.query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						s := referenceImplicitError(tc.query, tc.want)
						s.prepared, s.errorStage = prepared, "execute"
						if prepared {
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, c, b.recorder, s)
						referenceRunStep(t, p.ctx, c, b.recorder, timezoneViewsNames("SELECT * FROM pg_timezone_names WHERE name='UTC'", [][]any{{"UTC", "UTC", "00:00:00", false}}))
						referenceRunStep(t, p.ctx, c, b.recorder, timezoneViewsAbbrevs("SELECT * FROM pg_timezone_abbrevs WHERE abbrev='UTC'", [][]any{{"UTC", "00:00:00", false}}))
					})
				}
			}
		}
	})
}
