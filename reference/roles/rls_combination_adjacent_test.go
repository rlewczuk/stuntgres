package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRLSCombinationAdjacentBoundaries(t *testing.T) {
	for _, q := range []string{
		"INSERT INTO t VALUES(5,TRUE,50) ON CONFLICT(id) DO NOTHING",
		"MERGE INTO t USING (SELECT 5 AS id) x ON t.id=x.id WHEN NOT MATCHED THEN INSERT(id,allowed,v) VALUES(x.id,TRUE,50)",
	} {
		t.Run(q, func(t *testing.T) {
			combinationCases(t, func(s *fixture.RoleMembershipScenario) {
				// lib/pq does not decode MERGE command tags (fixture.MergeRun contract).
				count := int64(1)
				if s.Backend.PG && strings.HasPrefix(q, "MERGE") {
					count = 0
				}
				// Bypassing owner remains supported, even with expression policies present.
				s.Exec("CREATE POLICY p ON t USING(TRUE) WITH CHECK(TRUE)")
				s.Run(fixture.Exec(q, count))
				combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2, 3, 4, 5)
				s.Run(fixture.Exec("DELETE FROM t WHERE id=5", 1))
				s.Exec("SET ROLE comb_reader")
				if s.Backend.PG {
					s.Run(fixture.Exec(q, count))
				} else {
					predefinedFailure(s, q, fixture.Diagnostic{Code: "0A000", Message: "row-security policy expression enforcement is unsupported"}, false, "execute")
				}
				s.Exec("RESET ROLE")
				if s.Backend.PG {
					combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2, 3, 4, 5)
					s.Run(fixture.Exec("DELETE FROM t WHERE id=5", 1))
				} else {
					combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2, 3, 4)
				}
				s.Exec("DROP POLICY p ON t")
				s.Exec("SET ROLE comb_reader")
				combinationDeny(s, q, "", false)
				s.Exec("RESET ROLE")
				combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2, 3, 4)
			})
		})
	}
	for _, q := range []string{
		"INSERT INTO destination SELECT id,allowed,v FROM t WHERE TRUE ON CONFLICT(id) DO NOTHING",
		"MERGE INTO destination d USING t ON d.id=t.id WHEN NOT MATCHED THEN INSERT(id,allowed,v) VALUES(t.id,t.allowed,t.v)",
	} {
		t.Run("ordinary-source/"+q, func(t *testing.T) {
			combinationCases(t, func(s *fixture.RoleMembershipScenario) {
				s.Exec("CREATE TABLE destination(id INT PRIMARY KEY,allowed BOOL,v INT)")
				s.Exec("GRANT SELECT,INSERT ON destination TO comb_reader")
				s.Exec("CREATE POLICY source ON t FOR SELECT USING(id=1)")
				s.Exec("SET ROLE comb_reader")
				count := int64(1)
				if s.Backend.PG && strings.HasPrefix(q, "MERGE") {
					count = 0
				}
				s.Run(fixture.Exec(q, count))
				s.Exec("RESET ROLE")
				combinationIDs(s, "SELECT id FROM destination ORDER BY id", 1)
				combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2, 3, 4)
			})
		})
	}
}
