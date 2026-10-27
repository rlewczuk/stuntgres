package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSFilterOwnerRows(t *testing.T) {
	combinationCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Exec("ALTER TABLE t ADD COLUMN owner_name TEXT")
		s.Run(fixture.Exec("UPDATE t SET owner_name=CASE WHEN id IN(1,4) THEN 'comb_reader' WHEN id=2 THEN 'comb_group' ELSE NULL END", 4))
		// The explicit CHECK isolates OLD filtering, without changing ownership.
		s.Exec("CREATE POLICY owned ON t USING(owner_name=current_user) WITH CHECK(TRUE)")
		h := combinationHeld(s, "SELECT id FROM t ORDER BY id")
		s.Exec("SET ROLE comb_reader")
		combinationHeldIDs(s, h, 1, 4)
		s.Exec("SET ROLE comb_group")
		combinationHeldIDs(s, h, 2)
		s.Exec("RESET ROLE")
		combinationHeldIDs(s, h, 1, 2, 3, 4)

		s.Exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 4)
		s.Run(fixture.Exec("UPDATE t SET v=v+1 WHERE id=2", 0))
		s.Run(fixture.Exec("DELETE FROM t WHERE id=2", 0))
		s.Run(fixture.Exec("UPDATE t SET v=v+1", 2))
		s.Exec("SET ROLE comb_group")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 2)
		s.Run(fixture.Exec("UPDATE t SET v=v+10 WHERE id=1", 0))
		s.Run(fixture.Exec("DELETE FROM t WHERE id=1", 0))
		s.Run(fixture.Exec("UPDATE t SET v=v+10", 1))
		s.Exec("RESET ROLE")
		s.Run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{
			{int64(1), int64(11)}, {int64(2), int64(30)}, {int64(3), int64(30)}, {int64(4), int64(41)},
		}))
		s.Exec("SET ROLE comb_reader")
		s.Run(fixture.Exec("DELETE FROM t", 2))
		s.Exec("SET ROLE comb_group")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 2)
		s.Run(fixture.Exec("DELETE FROM t", 1))
		s.Exec("RESET ROLE")
		s.Run(fixture.Select("SELECT id,owner_name,v FROM t ORDER BY id", []string{"id", "owner_name", "v"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(3), nil, int64(30)}}))
		if len(*s.Notices) != 0 {
			s.T.Fatalf("unexpected notices: %+v", *s.Notices)
		}
	})
}

func TestReferenceRLSFilterTruthAndDefaultDeny(t *testing.T) {
	combinationCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id")
		s.Run(fixture.Exec("UPDATE t SET v=99", 0))
		s.Run(fixture.Exec("DELETE FROM t", 0))
		s.Exec("RESET ROLE")
		filterSeedRows(s)
		s.Exec("CREATE POLICY truth ON t USING(allowed) WITH CHECK(TRUE)")
		s.Exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 4)
		s.Run(fixture.Exec("UPDATE t SET v=v+1", 2))
		s.Exec("RESET ROLE")
		s.Run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{
			{int64(1), int64(11)}, {int64(2), int64(20)}, {int64(3), int64(30)}, {int64(4), int64(41)},
		}))
		s.Exec("SET ROLE comb_reader")
		s.Run(fixture.Exec("DELETE FROM t", 2))
		s.Exec("RESET ROLE")
		s.Run(fixture.Select("SELECT id,allowed,v FROM t ORDER BY id", []string{"id", "allowed", "v"}, []string{"INT4", "BOOL", "INT4"}, [][]any{
			{int64(2), false, int64(20)}, {int64(3), nil, int64(30)},
		}))
		if len(*s.Notices) != 0 {
			s.T.Fatalf("unexpected notices: %+v", *s.Notices)
		}
	})
}

// Inspect every original value through bootstrap authority, not the RLS view.
func filterSeedRows(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	s.Run(fixture.Select("SELECT id,allowed,v FROM t ORDER BY id", []string{"id", "allowed", "v"}, []string{"INT4", "BOOL", "INT4"}, [][]any{
		{int64(1), true, int64(10)}, {int64(2), false, int64(20)}, {int64(3), nil, int64(30)}, {int64(4), true, int64(40)},
	}))
}
