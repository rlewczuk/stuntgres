package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Shared output-format admission must retain declared type identity for both
// aggregate owners and their window route.
func TestReferenceSQLJSONAggregateOutputEncoding(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{
			"CREATE DOMAIN sqj_agg_encoding_text AS text",
			"CREATE DOMAIN sqj_agg_encoding_bytea AS bytea",
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
		for _, prefix := range []string{"SELECT ", "/*é😀*/ SELECT "} {
			for _, head := range []string{"JSON_ARRAYAGG(1 RETURNING ", "JSON_OBJECTAGG('a':1 RETURNING "} {
				for _, window := range []string{"", " OVER ()"} {
					for _, typ := range []string{"text", "json", "jsonb", "sqj_agg_encoding_text", "sqj_agg_encoding_bytea"} {
						for _, encoding := range []string{"UTF8", "UTF16", "UTF32"} {
							q := prefix + head + typ + " FORMAT JSON ENCODING " + encoding + ")" + window + " AS v"
							t.Run(q, func(t *testing.T) {
								d := fixture.ControlsReferenceDiagnostic("0A000", "cannot set JSON encoding for non-bytea output types")
								d.Position = jsonArrayAggregatePosition(q, "FORMAT")
								jsonArrayAggregateError(t, p, b, q, d, true)
							})
						}
					}
					for _, encoding := range []string{"UTF16", "UTF32"} {
						q := prefix + head + "bytea FORMAT JSON ENCODING " + encoding + ")" + window + " AS v"
						t.Run(q, func(t *testing.T) {
							d := fixture.ControlsReferenceDiagnostic("0A000", "unsupported JSON encoding")
							d.Hint = "Only UTF8 JSON encoding is supported."
							d.Position = jsonArrayAggregatePosition(q, "FORMAT")
							jsonArrayAggregateError(t, p, b, q, d, true)
						})
					}
					want := "[1]"
					if head == "JSON_OBJECTAGG('a':1 RETURNING " {
						want = `{ "a" : 1 }`
					}
					for _, output := range []struct{ clause, typ string }{
						{"bytea FORMAT JSON ENCODING UTF8", "BYTEA"},
						{"text FORMAT JSON", "TEXT"},
					} {
						q := prefix + head + output.clause + ")" + window + " AS v"
						t.Run(q, func(t *testing.T) {
							if window != "" && !b.PG {
								d := fixture.ControlsReferenceDiagnostic("0A000", "window function provider is unsupported")
								d.Position = jsonArrayAggregatePosition(q, head[:strings.IndexByte(head, '(')])
								jsonArrayAggregateError(t, p, b, q, d, true)
							} else {
								jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, output.typ, want))
							}
						})
					}
				}
			}
		}
	})
}
