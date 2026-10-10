package reference_test

import "testing"

// A complete match can bypass the separator-created capture through accepted
// cross-section groups/alternatives. Absent capture is NULL, not the complete
// source; no-separator extraction and a participating empty capture differ.
func TestReferenceSimilarAbsentCapture(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
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
			s := referenceSelect(`SELECT $1 SIMILAR TO $2 ESCAPE '#' AS matched,substring($1 SIMILAR $2 ESCAPE '#') AS piece`, []string{"matched", "piece"}, []string{"BOOL", "TEXT"}, [][]any{{true, c.piece}}, c.source, c.pattern)
			s.checkMetadata = similarReferenceMetadata
			similarReferenceRoutes(t, p, b, s)
		}
	}
}
