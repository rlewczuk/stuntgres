package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFormatReviewParameterInference(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("CREATE TABLE format_review_parameter_inference(id integer)", 0))
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				for _, tc := range []struct {
					name, query string
					diagnostic  fixture.Diagnostic
				}{
					{
						name:  "inconsistent",
						query: "SELECT format($1,$1::integer)",
						diagnostic: fixture.Diagnostic{
							Severity:            "ERROR",
							SeverityUnlocalized: "ERROR",
							Code:                "42P08",
							Message:             "inconsistent types deduced for parameter $1",
							Detail:              "integer versus text",
						},
					},
					{
						name:  "child-before-formal-inference",
						query: "SELECT format($1,$1::integer,absent) FROM format_review_parameter_inference",
						diagnostic: fixture.Diagnostic{
							Severity:            "ERROR",
							SeverityUnlocalized: "ERROR",
							Code:                "42703",
							Message:             `column "absent" does not exist`,
						},
					},
				} {
					t.Run(prefix+tc.name, func(t *testing.T) {
						q := prefix + tc.query
						d := tc.diagnostic
						if tc.name == "inconsistent" {
							d.Position = fixture.Position(len([]rune(q[:strings.Index(q, "$1")])) + 1)
						} else {
							d.Position = fixture.Position(len([]rune(q[:strings.Index(q, "absent")])) + 1)
						}
						for _, prepared := range []bool{false, true} {
							t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
								s := fixture.ErrorDiagnostic(q, d, "7")
								s.SelectRows, s.ErrorStage = true, "execute"
								if prepared {
									s = fixture.Prepared(s)
									s.ErrorStage = "prepare"
								}
								fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
							})
						}
					})
				}
				t.Run(prefix+"compatible", func(t *testing.T) {
					s := fixture.Select(prefix+"SELECT format($1,$1::text)", []string{"format"}, []string{"TEXT"}, [][]any{{"compatible"}}, "compatible")
					formatReferenceBoth(t, p, backend, conn, s)
				})
			}
		})
	}
}
