package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRLSCombinationSetViewDemand(t *testing.T) {
	for _, negative := range []bool{false, true} {
		name, predicate := "exists", "EXISTS"
		ids := []int64{1, 3}
		if negative {
			name, predicate = "not_exists", "NOT EXISTS"
			ids = []int64{2, 4}
		}
		t.Run(name, func(t *testing.T) {
			combinationCases(t, func(s *fixture.RoleMembershipScenario) {
				combinationViewDemandSource(s)
				s.Exec("CREATE POLICY p ON t FOR SELECT USING(" + predicate + "(SELECT 1 FROM helper_view h WHERE h.lookup=t.id UNION ALL SELECT 1 WHERE t.id<0))")
				// The held query survives renaming the policy's correlated target column;
				// neither its SQL nor its independently expected INT4 result shape changes.
				q := "SELECT v/10 AS id FROM t ORDER BY v"
				held := combinationHeld(s, q)
				s.Exec("SET ROLE comb_reader")
				combinationIDs(s, q, ids...)
				combinationHeldIDs(s, held, ids...)
				combinationHeldIDs(s, held, ids...)
				s.Exec("RESET ROLE")
				s.Exec("ALTER TABLE helper RENAME COLUMN id TO retained_id")
				s.Exec("ALTER TABLE helper RENAME TO retained_helper")
				s.Exec("ALTER VIEW helper_view RENAME COLUMN lookup TO retained_key")
				s.Exec("ALTER VIEW helper_view RENAME TO retained_view")
				s.Exec("CREATE TABLE helper(id INT)")
				s.Run(fixture.Exec("INSERT INTO helper VALUES(2),(4)", 2))
				s.Exec("CREATE VIEW helper_view AS SELECT 0 AS unused,id+0 AS lookup FROM helper")
				s.Exec("GRANT SELECT ON helper_view TO comb_reader")
				s.Exec("ALTER TABLE t RENAME COLUMN id TO retained_id")
				s.Exec("SET ROLE comb_reader")
				combinationIDs(s, q, ids...)
				combinationHeldIDs(s, held, ids...)
				s.Exec("RESET ROLE")
			})
		})
	}
}
