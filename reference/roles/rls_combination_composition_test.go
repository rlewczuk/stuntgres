package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func combinationPolicies(s *fixture.RoleMembershipScenario) {
	s.Exec("CREATE POLICY p_public ON t FOR SELECT TO PUBLIC USING(id=1)")
	s.Exec("CREATE POLICY p_direct ON t FOR SELECT TO comb_reader USING(id=2)")
	s.Exec("CREATE POLICY p_group ON t FOR SELECT TO comb_group USING(id=3)")
	s.Exec("CREATE POLICY p_other ON t FOR SELECT TO comb_other USING(id=4)")
}
func TestReferenceRLSCombinationApplicability(t *testing.T) {
	combinationCases(t, func(s *fixture.RoleMembershipScenario) {
		combinationPolicies(s)
		s.Exec("CREATE POLICY danger ON t FOR UPDATE TO comb_reader USING(10/(id-id)>0)")
		s.Exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2, 3)
		s.Exec("SET ROLE comb_group")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 3)
		s.Run(fixture.Select("SELECT current_user AS actor,session_user AS login", []string{"actor", "login"}, []string{"NAME", "NAME"}, [][]any{{"comb_group", s.Bootstrap}}))
		s.Exec("RESET ROLE")
		s.Exec("GRANT comb_group TO comb_reader WITH INHERIT FALSE")
		s.Exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2)
		s.Exec("RESET ROLE")
		s.Exec("GRANT comb_group TO comb_middle WITH INHERIT FALSE")
		s.Exec("GRANT comb_middle TO comb_reader WITH INHERIT TRUE")
		s.Exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2)
		s.Exec("RESET ROLE")
		s.Exec("CREATE POLICY alternatives ON t FOR SELECT TO comb_reader,comb_other USING(id=4)")
		s.Exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2, 4)
	})
}
func TestReferenceRLSCombinationPermissiveRestrictive(t *testing.T) {
	combinationCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Exec("CREATE POLICY p ON t FOR SELECT USING(id IN(1,2))")
		s.Exec("CREATE POLICY q ON t FOR SELECT USING(id=3)")
		s.Exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2, 3)
		s.Exec("RESET ROLE")
		s.Exec("CREATE POLICY r ON t AS RESTRICTIVE FOR SELECT USING(allowed)")
		s.Exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1)
		s.Exec("RESET ROLE")
		s.Exec("CREATE POLICY a ON t USING(id=4)")
		s.Exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 4)
		s.Exec("RESET ROLE")
		for _, p := range []string{"p", "q", "a"} {
			s.Exec("DROP POLICY " + p + " ON t")
		}
		s.Exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id")
		s.Exec("RESET ROLE")
		s.Exec("ALTER POLICY r ON t USING(TRUE)")
		s.Exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id")
	})
}
func TestReferenceRLSCombinationThreeValued(t *testing.T) {
	for _, tc := range []struct {
		p, q, r string
		pass    bool
	}{
		{"TRUE", "NULL", "TRUE", true}, {"FALSE", "NULL", "TRUE", false},
		{"TRUE", "FALSE", "NULL", false}, {"TRUE", "FALSE", "FALSE", false},
	} {
		t.Run(tc.p+tc.q+tc.r, func(t *testing.T) {
			combinationCases(t, func(s *fixture.RoleMembershipScenario) {
				s.Exec("CREATE POLICY p ON t USING(" + tc.p + ")")
				s.Exec("CREATE POLICY q ON t USING(" + tc.q + ")")
				s.Exec("CREATE POLICY r ON t AS RESTRICTIVE USING(" + tc.r + ")")
				s.Exec("SET ROLE comb_reader")
				if tc.pass {
					combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2, 3, 4)
				} else {
					combinationIDs(s, "SELECT id FROM t ORDER BY id")
				}
			})
		})
	}
}
func TestReferenceRLSCombinationCommandConjunction(t *testing.T) {
	combinationCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Exec("CREATE POLICY sel ON t FOR SELECT USING(id<=2)")
		s.Exec("CREATE POLICY upd ON t FOR UPDATE USING(id IN(2,3)) WITH CHECK(TRUE)")
		s.Exec("CREATE POLICY del ON t FOR DELETE USING(id IN(2,3))")
		s.Exec("SET ROLE comb_reader")
		combinationIDs(s, "UPDATE t SET v=99 WHERE id>0 RETURNING id", 2)
		s.Run(fixture.Exec("UPDATE t SET v=99", 2))
		combinationIDs(s, "SELECT a.id FROM t a JOIN t b ON a.id=b.id ORDER BY a.id", 1, 2)
		s.Run(fixture.Exec("DELETE FROM t WHERE id>0", 1))
		s.Exec("RESET ROLE")
		s.Run(fixture.Exec("INSERT INTO t VALUES(2,FALSE,20)", 1))
		s.Exec("SET ROLE comb_reader")
		s.Run(fixture.Exec("DELETE FROM t USING (SELECT 2 AS id) x WHERE t.id=x.id", 1))
		s.Exec("RESET ROLE")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 3, 4)
	})
}
