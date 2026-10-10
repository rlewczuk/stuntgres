package reference_test

import "testing"

// Exact tagged iteration and feasible-capture outcomes observed on pinned
// PostgreSQL18.0 in both direct and prepared execution.
func TestReferenceSimilarReviewTaggedIterator(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		for _, c := range []struct {
			source, pattern string
			piece           any
		}{
			{"", `#"a?)*(#"`, ""},
			{"", `#"a??)*(#"`, nil},
			{"", `#"a?)+(#"`, ""},
			{"aaa", `#"a+)*(#"`, "aaa"},
			{"aaa", `#"a+?)*(#"`, "a"},
			{"aaaa", `((#"a+#"#1))+`, "a"},
			{"aaaa", `#"a+#"#1`, "aa"},
			{"aaaaaa", `#"a+#"#1`, "aaa"},
		} {
			step := referenceSelect(`SELECT $1 SIMILAR TO $2 ESCAPE '#' AS matched,substring($1 SIMILAR $2 ESCAPE '#') AS piece`, []string{"matched", "piece"}, []string{"BOOL", "TEXT"}, [][]any{{true, c.piece}}, c.source, c.pattern)
			step.checkMetadata = similarReferenceMetadata
			similarReferenceRoutes(t, p, b, step)
		}
	}
}
