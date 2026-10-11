package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Exact tagged iteration and feasible-capture outcomes observed on pinned
// PostgreSQL18.0 in both direct and prepared execution.
func TestReferenceSimilarReviewTaggedIterator(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
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
			step := fixture.Select(`SELECT $1 SIMILAR TO $2 ESCAPE '#' AS matched,substring($1 SIMILAR $2 ESCAPE '#') AS piece`, []string{"matched", "piece"}, []string{"BOOL", "TEXT"}, [][]any{{true, c.piece}}, c.source, c.pattern)
			step.CheckMetadata = similarReferenceMetadata
			similarReferenceRoutes(t, p, b, step)
		}
	}
}
