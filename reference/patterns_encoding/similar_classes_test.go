package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Unicode and ASCII expectations independently specified from release source,
// confirmed against pinned PostgreSQL18.0 UTF8/C in the root's oracle run.
func TestReferenceSimilarClasses(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, collation := range []string{"C", "POSIX", "pg_c_utf8", "pg_unicode_fast"} {
			for _, c := range []struct {
				code     int
				class    string
				expected bool
			}{
				{133, "cntrl", true}, {233, "alpha", collation == "pg_c_utf8" || collation == "pg_unicode_fast"},
				{1637, "digit", collation == "pg_unicode_fast"}, {43, "punct", collation != "pg_unicode_fast"},
				{160, "blank", false},
			} {
				query := fmt.Sprintf(`SELECT chr(%d) SIMILAR TO '[[:%s:]]' COLLATE "%s" AS matched`, c.code, c.class, collation)
				similarReferenceRoutes(t, p, b, fixture.Select(query, []string{"matched"}, []string{"BOOL"}, [][]any{{c.expected}}))
			}
		}
		for _, c := range []struct{ query, piece string }{
			{`SELECT '"' SIMILAR TO '[#"]' ESCAPE '#', substring('"' SIMILAR '[#"]' ESCAPE '#')`, `"`},
			{`SELECT '"' SIMILAR TO '[\"]', substring('"' SIMILAR '[\"]' ESCAPE '\')`, `"`},
			{`SELECT ']' SIMILAR TO '[]#"]' ESCAPE '#', substring(']' SIMILAR '[]#"]' ESCAPE '#')`, "]"},
		} {
			similarReferenceRoutes(t, p, b, fixture.Select(c.query, []string{"?column?", "substring"}, []string{"BOOL", "TEXT"}, [][]any{{true, c.piece}}))
		}
	}
}
