package reference_test

import "testing"

// PostgreSQL18.0 direct/prepared observations pin final-iteration capture
// reset and partition ranking independently of the native matcher.
func TestReferenceSimilarRepeatedCapture(t *testing.T) {
	p := newReferenceScenario(t)
	cases := []struct {
		source, pattern string
		piece           any
	}{
		{"a", `((#"a#")|b)*`, "a"},
		{"ab", `((#"a#")|b)*`, nil},
		{"ba", `((#"a#")|b)*`, "a"},
		{"b", `((#"a#")|b)*`, nil},
		{"", `((#"a#")|b)*`, nil},
		{"aaa", `((#"a%#"))+`, "a"},
		{"aaa", `((#"a%#"))+?`, "aaa"},
		{"aaa", `((#"a%#")){2}`, "aa"},
		{"aaa", `((#"a%#")){2}?`, "aa"},
	}
	const query = `SELECT $1 SIMILAR TO $2 ESCAPE '#' AS matched,substring($1 SIMILAR $2 ESCAPE '#') AS piece`
	for _, b := range p.backends() {
		for _, c := range cases {
			step := referenceSelect(query, []string{"matched", "piece"}, []string{"BOOL", "TEXT"}, [][]any{{true, c.piece}}, c.source, c.pattern)
			step.checkMetadata = similarReferenceMetadata
			similarReferenceRoutes(t, p, b, step)
		}
	}
}
