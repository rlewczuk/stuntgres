package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// A complete match can bypass the separator-created capture through accepted
// cross-section groups/alternatives. Absent capture is NULL, not the complete
// source; no-separator extraction and a participating empty capture differ.
func TestReferenceSimilarAbsentCapture(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, c := range []struct {
			source, pattern string
			piece           any
		}{
			{"d", `((a#"b#")|d)`, nil},
			{"b", `#"a)|b(#"`, nil},
			{"ab", `((a#"b#")|d)`, "b"},
			{"b", `b`, "b"},
			{"ab", `a#"#"b`, ""},
		} {
			s := fixture.Select(`SELECT $1 SIMILAR TO $2 ESCAPE '#' AS matched,substring($1 SIMILAR $2 ESCAPE '#') AS piece`, []string{"matched", "piece"}, []string{"BOOL", "TEXT"}, [][]any{{true, c.piece}}, c.source, c.pattern)
			s.CheckMetadata = similarReferenceMetadata
			similarReferenceRoutes(t, p, b, s)
		}
	}
}
