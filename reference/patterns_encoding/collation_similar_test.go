package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Copies retain provider character-class semantics, not the source's object OID.
// Expectations are independently specified by the existing PostgreSQL18 Unicode
// class contract: C.UTF-8 alphabetic/punctuation and PG_UNICODE_FAST decimal.
func TestReferenceCollationSimilarCopiedUnicode(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.CollationExec(t, p, b,
			`CREATE COLLATION similar_copy_c FROM pg_catalog."C"`,
			`CREATE COLLATION similar_copy_utf8 FROM pg_catalog.pg_c_utf8`,
			`CREATE COLLATION similar_copy_full FROM pg_catalog.pg_unicode_fast`,
		)
		for _, c := range []struct {
			name                string
			alpha, digit, punct bool
		}{
			{"similar_copy_c", false, false, true},
			{"similar_copy_utf8", true, false, true},
			{"similar_copy_full", true, true, false},
		} {
			for _, witness := range []struct {
				source, class string
				want          bool
			}{
				{"é", "alpha", c.alpha},
				{"٥", "digit", c.digit},
				{"+", "punct", c.punct},
			} {
				pattern := "[[:" + witness.class + ":]]"
				query := fmt.Sprintf(`SELECT $1::text COLLATE %s SIMILAR TO $2 AS matched`, c.name)
				fixture.CollationRoutes(t, p, b, fixture.Select(query, []string{"matched"}, []string{"BOOL"}, [][]any{{witness.want}}, witness.source, pattern))
				query = fmt.Sprintf(`SELECT $1::text SIMILAR TO $2::text COLLATE %s AS matched`, c.name)
				fixture.CollationRoutes(t, p, b, fixture.Select(query, []string{"matched"}, []string{"BOOL"}, [][]any{{witness.want}}, witness.source, pattern))
			}
		}
		fixture.CollationExec(t, p, b,
			`CREATE TABLE similar_copy_input(compat TEXT COLLATE similar_copy_utf8,unicode_full TEXT COLLATE similar_copy_full)`,
		)
		fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO similar_copy_input VALUES('é','٥')`, 1))
		fixture.CollationExec(t, p, b,
			`CREATE VIEW similar_copy_view AS SELECT compat SIMILAR TO '[[:alpha:]]' AS alphabetic,unicode_full SIMILAR TO '[[:digit:]]' AS decimal_digit FROM similar_copy_input`,
		)
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT alphabetic,decimal_digit FROM similar_copy_view`, []string{"alphabetic", "decimal_digit"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	})
}
