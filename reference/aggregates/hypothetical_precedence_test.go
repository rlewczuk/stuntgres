package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceHypotheticalAggregatePrecedence(t *testing.T) {
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		for _, name := range []string{"rank", "dense_rank", "percent_rank", "cume_dist"} {
			for _, tc := range []struct{ direct, order, code, message, token, hint string }{
				{"NULL::json, 1", "NULL::json, 'x'::text", "42804", "WITHIN GROUP types text and integer cannot be matched", "1)", ""},
				{"'a' COLLATE \"C\", 1", "'a' COLLATE \"POSIX\", 'x'::text", "42804", "WITHIN GROUP types text and integer cannot be matched", "1)", ""},
				{"'a' COLLATE \"C\", 'bad'", "'a' COLLATE \"POSIX\", 1", "22P02", "invalid input syntax for type integer: \"bad\"", "'bad'", ""},
				{"'a' COLLATE \"C\", NULL::json", "'a' COLLATE \"POSIX\", NULL::json", "42883", "could not identify an ordering operator for type json", "", "Use an explicit ordering operator or modify the query."},
			} {
				q := "/*é😀*/ SELECT " + name + "(" + tc.direct + ") WITHIN GROUP(ORDER BY " + tc.order + ")"
				t.Run(q, func(t *testing.T) {
					s := orderedSetDiagnostic(q, tc.code, tc.message, tc.token, tc.hint)
					if tc.code == "42883" {
						s.Diagnostic.Position = fixture.Position(utf8.RuneCountInString(q[:strings.LastIndex(q, "NULL::json")]) + 1)
					}
					// All fail during analysis: direct Query/Exec executes analysis;
					// prepared Query/Exec must fail while preparing, never executing.
					orderedSetBoth(t, p, c, b, s)
				})
			}
		}
	})
}
