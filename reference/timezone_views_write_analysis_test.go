package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func TestReferenceTimezoneViewsWriteAnalysis(t *testing.T) {
	timezoneViewsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor) {
		for _, view := range []string{"pg_timezone_names", "pg_timezone_abbrevs"} {
			for _, tc := range []struct {
				query, token string
				want         referenceDiagnostic
			}{
				{"UPDATE pg_catalog." + view + " SET bogus=true", "bogus", referenceDiagnostic{Code: "42703", Message: `column "bogus" of relation "` + view + `" does not exist`}},
				{"UPDATE pg_catalog." + view + " SET is_dst=true WHERE bogus=true", "bogus", referenceDiagnostic{Code: "42703", Message: `column "bogus" does not exist`}},
				{"DELETE FROM pg_catalog." + view + " WHERE bogus=true", "bogus", referenceDiagnostic{Code: "42703", Message: `column "bogus" does not exist`}},
				{"UPDATE pg_catalog." + view + " SET is_dst=1", "1", referenceDiagnostic{Code: "42804", Message: `column "is_dst" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression."}},
				{"DELETE FROM pg_catalog." + view + " WHERE 1", "1", referenceDiagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer"}},
				{"UPDATE pg_catalog." + view + " SET is_dst=true RETURNING bogus", "bogus", referenceDiagnostic{Code: "42703", Message: `column "bogus" does not exist`}},
				{"DELETE FROM pg_catalog." + view + " RETURNING bogus", "bogus", referenceDiagnostic{Code: "42703", Message: `column "bogus" does not exist`}},
			} {
				tc.want.Position = strconv.Itoa(strings.Index(tc.query, tc.token) + 1)
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
