package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// The SQL wrapper executes its inner matcher with database-default C collation,
// even when an input carries an explicit builtin Unicode identity.
func TestReferenceSimilarExtractionCollation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, collation := range []string{"C", "pg_c_utf8", "pg_unicode_fast"} {
			for _, operand := range []string{"source", "pattern", "escape"} {
				for _, c := range []struct {
					code  int
					class string
				}{{233, "alpha"}, {1637, "digit"}} {
					source := fmt.Sprintf("chr(%d)", c.code)
					pattern := fmt.Sprintf(`'#"[[:%s:]]#"'`, c.class)
					escape := "'#'"
					annotation := fmt.Sprintf(` COLLATE "%s"`, collation)
					switch operand {
					case "source":
						source += annotation
					case "pattern":
						pattern += annotation
					case "escape":
						escape += annotation
					}
					query := fmt.Sprintf("SELECT substring(%s SIMILAR %s ESCAPE %s) AS piece", source, pattern, escape)
					step := fixture.Select(query, []string{"piece"}, []string{"TEXT"}, [][]any{{nil}})
					step.CheckMetadata = similarReferenceMetadata
					similarReferenceRoutes(t, p, b, step)
				}
			}
		}
	}
}
