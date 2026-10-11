package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAggregateModifiersDistinctCollation(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE am_collation(a TEXT COLLATE "C",b TEXT COLLATE "POSIX")`, 0))
		for _, populated := range []bool{false, true} {
			if populated {
				run(fixture.Exec("INSERT INTO am_collation VALUES('a','x'),('a','y'),(NULL,'b'),(NULL,NULL)", 4))
			}
			for _, filter := range []string{"", " FILTER(WHERE FALSE)"} {
				q := "/* é */ SELECT count(DISTINCT COALESCE(a,b))" + filter + " FROM am_collation"
				s := aggregateModifiersError(q, "42P21", `collation mismatch between implicit collations "C" and "POSIX"`, "b))")
				s.Diagnostic.Hint = "You can choose the collation by applying the COLLATE clause to one or both expressions."
				aggregateModifiersBoth(run, s)
				q = `/* é */ SELECT count(DISTINCT COALESCE(a,b) COLLATE "C")` + filter + " FROM am_collation"
				count := int64(0)
				if populated && filter == "" {
					count = 2
				}
				aggregateModifiersBoth(run, fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{count}}))
			}
		}
	})
}
