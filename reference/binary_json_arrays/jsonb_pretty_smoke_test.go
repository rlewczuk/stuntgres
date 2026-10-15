package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBPrettyFeatureSmoke(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ input, want any }{
			{jsonbPrettyNestedInput, jsonbPrettyNested}, {jsonTypeOrderedObject, jsonbPrettyOrdered},
			{jsonbPrettyEscapeInput, jsonbPrettyEscaped}, {"[]", "[\n]"},
			{"1.230e-5", "0.00001230"}, {"null", "null"}, {nil, nil},
		} {
			jsonTypeRun(t, p, b, c, jsonTypeWithMetadata(jsonbPrettyOne("SELECT jsonb_pretty($1) AS pretty", tc.want, tc.input), b.PG))
		}
		jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature("SELECT jsonb_pretty(1)", "jsonb_pretty(integer)", 8))
		// Reuse behavioral consumers, not a second implementation or fixture matrix:
		// one held handle with recovery, then stored recipes/atomicity/native replay.
		jsonbPrettyHeld(t, p, b, c)
		jsonbPrettyStored(t, p, b, c)
	})
}
