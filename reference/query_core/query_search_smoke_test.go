package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQuerySearchFeatureSmoke(t *testing.T) {
	searchReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		searchSeed(subqueryRun(t, p, b, c))
		for _, mode := range searchModes {
			t.Run(mode+" real ordered values", func(t *testing.T) {
				recursiveCTERoutes(t, p, b, c, searchTreeStep(mode, searchTree(mode)+"SELECT id,seq FROM r ORDER BY seq"))
			})
			t.Run(mode+" same held fresh frames", func(t *testing.T) { searchHeld(t, p, b, c, mode) })
			for _, tc := range []struct{ keys, set, message string }{
				{"missing", "seq", `search column "missing" not in WITH query column list`},
				{"n", "n", `search sequence column name "n" already used in WITH query column list`},
			} {
				t.Run(mode+" positioned "+tc.message, func(t *testing.T) {
					q := "/*é*/ WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT 1 FROM r) SEARCH " + mode + " FIRST BY " + tc.keys + " SET " + tc.set + " SELECT * FROM r"
					cycleAssertDiagnostic(t, p, b, c, q, cycleDiagnostic("42601", tc.message, q, "SEARCH"))
				})
			}
		}
		t.Run("typed NULL keys", func(t *testing.T) { searchNulls(t, p, b, c) })
		t.Run("native symbolic restore and foreign owner", func(t *testing.T) { searchSmokeStored(t, p, b, c) })
		for _, mode := range searchModes {
			t.Run(mode+" late error atomicity", func(t *testing.T) { searchFailure(t, p, b, c, mode) })
			t.Run(mode+" active cancellation and recovery", func(t *testing.T) { searchCancellation(t, p, b, c, mode) })
		}
	})
}
