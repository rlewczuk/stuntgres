package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAggregateModifiersDistinctCollation(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec(`CREATE TABLE am_collation(a TEXT COLLATE "C",b TEXT COLLATE "POSIX")`, 0))
		for _, populated := range []bool{false, true} {
			if populated {
				run(referenceExec("INSERT INTO am_collation VALUES('a','x'),('a','y'),(NULL,'b'),(NULL,NULL)", 4))
			}
			for _, filter := range []string{"", " FILTER(WHERE FALSE)"} {
				q := "/* é */ SELECT count(DISTINCT COALESCE(a,b))" + filter + " FROM am_collation"
				s := aggregateModifiersError(q, "42P21", `collation mismatch between implicit collations "C" and "POSIX"`, "b))")
				s.diagnostic.Hint = "You can choose the collation by applying the COLLATE clause to one or both expressions."
				aggregateModifiersBoth(run, s)
				q = `/* é */ SELECT count(DISTINCT COALESCE(a,b) COLLATE "C")` + filter + " FROM am_collation"
				count := int64(0)
				if populated && filter == "" {
					count = 2
				}
				aggregateModifiersBoth(run, referenceSelect(q, []string{"count"}, []string{"INT8"}, [][]any{{count}}))
			}
		}
	})
}
