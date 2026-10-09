package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceFormatReviewParameterInference(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("CREATE TABLE format_review_parameter_inference(id integer)", 0))
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				for _, tc := range []struct {
					name, query string
					diagnostic  referenceDiagnostic
				}{
					{
						name:  "inconsistent",
						query: "SELECT format($1,$1::integer)",
						diagnostic: referenceDiagnostic{
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
						diagnostic: referenceDiagnostic{
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
							d.Position = referencePosition(len([]rune(q[:strings.Index(q, "$1")])) + 1)
						} else {
							d.Position = referencePosition(len([]rune(q[:strings.Index(q, "absent")])) + 1)
						}
						for _, prepared := range []bool{false, true} {
							t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
								s := referenceErrorDiagnostic(q, d, "7")
								s.selectRows, s.errorStage = true, "execute"
								if prepared {
									s = referencePrepared(s)
									s.errorStage = "prepare"
								}
								referenceRunStep(t, p.ctx, conn, backend.recorder, s)
							})
						}
					})
				}
				t.Run(prefix+"compatible", func(t *testing.T) {
					s := referenceSelect(prefix+"SELECT format($1,$1::text)", []string{"format"}, []string{"TEXT"}, [][]any{{"compatible"}}, "compatible")
					formatReferenceBoth(t, p, backend, conn, s)
				})
			}
		})
	}
}
