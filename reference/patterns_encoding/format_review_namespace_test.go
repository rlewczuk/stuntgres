package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFormatReviewNamespace(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE format_review_analysis(id integer,v text)", 0))
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			for _, call := range []string{
				"format_review_missing.format(NULL,absent)",
				"format(NULL,format_review_missing.absent_format_child(absent))",
			} {
				q := prefix + "SELECT " + call + " FROM format_review_analysis"
				d := fixture.ControlsReferenceDiagnostic("42703", `column "absent" does not exist`)
				d.Position = fixture.Position(len([]rune(q[:strings.LastIndex(q, "absent)")])) + 1)
				t.Run(q, func(t *testing.T) { formatReferenceFailure(t, p, b, c, q, d, true) })
			}
			for _, call := range []string{
				"format_review_missing.format(NULL,v)",
				"format(NULL,format_review_missing.absent_format_child(v))",
			} {
				q := prefix + "SELECT " + call + " FROM format_review_analysis"
				d := fixture.ControlsReferenceDiagnostic("3F000", `schema "format_review_missing" does not exist`)
				d.Position = fixture.Position(len([]rune(q[:strings.Index(q, "format_review_missing")])) + 1)
				t.Run(q, func(t *testing.T) { formatReferenceFailure(t, p, b, c, q, d, true) })
			}
		}
	})
}
