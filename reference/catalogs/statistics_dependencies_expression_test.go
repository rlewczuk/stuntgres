package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsDependenciesExpression(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE dep_t(a INT,b INT,c INT, id INT PRIMARY KEY)", 0))
		s.run(fixture.Exec("INSERT INTO dep_t VALUES(0,0,0,1),(0,1,1,2),(1,0,1,3),(1,1,0,4)", 4))
		s.run(fixture.Exec("CREATE STATISTICS dep_s(dependencies) ON a,(b+0),(c+0) FROM dep_t", 0))
		s.run(fixture.Exec("ANALYZE dep_t", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, `{"1, -1 => -2": 1.000000, "1, -2 => -1": 1.000000, "-1, -2 => 1": 1.000000}`}))
		s.run(fixture.Exec("DELETE FROM dep_t", 4))
		s.run(fixture.Exec("INSERT INTO dep_t VALUES(1,10,0,1),(1,NULL,0,2),(2,20,0,3)", 3))
		s.run(fixture.Exec("DROP STATISTICS dep_s", 0))
		s.run(fixture.Exec("CREATE STATISTICS dep_s(dependencies) ON a,(b+0) FROM dep_t", 0))
		s.run(fixture.Exec("ANALYZE dep_t", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, `{"1 => -1": 0.333333, "-1 => 1": 1.000000}`}))
		s.run(fixture.Exec("UPDATE dep_t SET b=10 WHERE a=1", 2))
		// Simple key a gates eligibility; expression input b is read from full rows
		// even when it is absent from the selected ANALYZE column list.
		s.run(fixture.Exec("ANALYZE dep_t(a,c)", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, `{"1 => -1": 1.000000, "-1 => 1": 1.000000}`}))
		s.run(fixture.Exec("ANALYZE dep_t(a,b)", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, `{"1 => -1": 1.000000, "-1 => 1": 1.000000}`}))
	})
}
