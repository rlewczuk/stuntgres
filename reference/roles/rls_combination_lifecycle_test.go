package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRLSCombinationHeldRole(t *testing.T) {
	combinationCases(t, func(s *fixture.RoleMembershipScenario) {
		combinationPolicies(s)
		h := combinationHeld(s, "SELECT id FROM t ORDER BY id")
		combinationHeldIDs(s, h, 1, 2, 3, 4)
		s.Exec("SET ROLE comb_reader")
		combinationHeldIDs(s, h, 1, 2, 3)
		s.Exec("SET ROLE comb_group")
		combinationHeldIDs(s, h, 1, 3)
		s.Exec("RESET ROLE")
		combinationHeldIDs(s, h, 1, 2, 3, 4)
		// A live executor retains its admitted quals and nested helper pins, while
		// expression identity callbacks use the FETCH operation, not ended DECLARE.
		s.Exec("CREATE TABLE cursor_helper(id INT)")
		s.Run(fixture.Exec("INSERT INTO cursor_helper VALUES(1),(2),(3)", 3))
		s.Exec("GRANT SELECT ON cursor_helper TO comb_reader,comb_group")
		s.Exec("CREATE POLICY cursor_helper_guard ON t AS RESTRICTIVE FOR SELECT USING(EXISTS(SELECT 1 FROM cursor_helper h WHERE h.id=t.id))")
		s.Exec("SET ROLE comb_reader")
		s.Exec("BEGIN")
		open := true
		defer func() {
			if open {
				_, _ = s.C.ExecContext(s.P.Ctx, "ROLLBACK")
			}
		}()
		s.Exec("DECLARE combination_cursor NO SCROLL CURSOR FOR SELECT id,current_user AS actor,session_user AS login FROM t ORDER BY id")
		s.Exec("SET ROLE comb_group")
		s.Run(fixture.Select("FETCH ALL FROM combination_cursor", []string{"id", "actor", "login"}, []string{"INT4", "NAME", "NAME"}, [][]any{
			{int64(1), "comb_group", s.Bootstrap}, {int64(2), "comb_group", s.Bootstrap}, {int64(3), "comb_group", s.Bootstrap},
		}))
		combinationHeldIDs(s, h, 1, 3)
		s.Exec("CLOSE combination_cursor")
		s.Exec("COMMIT")
		open = false
		s.Exec("RESET ROLE")
	})
}
func TestReferenceRLSCombinationHeldPolicy(t *testing.T) {
	combinationCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Exec("CREATE POLICY sel ON t FOR SELECT USING(TRUE)")
		s.Exec("CREATE POLICY ins ON t FOR INSERT WITH CHECK(TRUE)")
		s.Exec("CREATE POLICY upd ON t FOR UPDATE USING(TRUE) WITH CHECK(TRUE)")
		s.Exec("SET ROLE comb_reader")
		selectH := combinationHeld(s, "SELECT id FROM t ORDER BY id")
		insertH := combinationHeld(s, "INSERT INTO t VALUES(5,TRUE,50)")
		updateH := combinationHeld(s, "UPDATE t SET v=99 WHERE id=1")
		combinationHeldIDs(s, selectH, 1, 2, 3, 4)
		fixture.RunStep(s.T, s.P.Ctx, insertH, s.Record, fixture.Exec(insertH.SQL, 1))
		fixture.RunStep(s.T, s.P.Ctx, updateH, s.Record, fixture.Exec(updateH.SQL, 1))
		s.Exec("RESET ROLE")
		s.Run(fixture.Exec("DELETE FROM t WHERE id=5", 1))
		s.Exec("ALTER POLICY ins ON t WITH CHECK(FALSE)")
		s.Exec("ALTER POLICY upd ON t WITH CHECK(FALSE)")
		s.Exec("SET ROLE comb_reader")
		for _, h := range []fixture.HeldStatement{insertH, updateH} {
			step := fixture.ImplicitError(h.SQL, fixture.Diagnostic{Code: "42501", Message: `new row violates row-level security policy for table "t"`})
			fixture.RunStep(s.T, s.P.Ctx, h, s.Record, step)
		}
		combinationHeldIDs(s, selectH, 1, 2, 3, 4)
		s.Exec("RESET ROLE")
		s.Exec("ALTER POLICY sel ON t USING(FALSE)")
		s.Exec("SET ROLE comb_reader")
		combinationHeldIDs(s, selectH)
		s.Exec("RESET ROLE")
		s.Exec("DROP POLICY sel ON t")
		s.Exec("SET ROLE comb_reader")
		combinationHeldIDs(s, selectH)
		s.Exec("RESET ROLE")
		s.Exec("CREATE POLICY sel ON t FOR SELECT USING(id=2)")
		s.Exec("ALTER POLICY sel ON t RENAME TO replacement")
		s.Exec("SET ROLE comb_reader")
		combinationHeldIDs(s, selectH, 2)
		s.Exec("RESET ROLE")
		s.Run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(99)}, {int64(2), int64(20)}, {int64(3), int64(30)}, {int64(4), int64(40)}}))
	})
}
func TestReferenceRLSCombinationFeatureSmoke(t *testing.T) {
	combinationCases(t, func(s *fixture.RoleMembershipScenario) {
		combinationPolicies(s)
		s.Exec("CREATE POLICY r ON t AS RESTRICTIVE FOR SELECT USING(CASE WHEN id<3 THEN TRUE ELSE NULL END)")
		s.Exec("CREATE POLICY upd ON t FOR UPDATE USING(id=2) WITH CHECK(TRUE)")
		s.Exec("CREATE POLICY ins ON t FOR INSERT WITH CHECK(allowed)")
		h := combinationHeld(s, "SELECT id FROM t ORDER BY id")
		s.Exec("SET ROLE comb_reader")
		combinationHeldIDs(s, h, 1, 2)
		combinationIDs(s, "UPDATE t SET v=99 WHERE id>0 RETURNING id", 2)
		for _, value := range []string{"FALSE", "NULL"} {
			combinationDeny(s, "INSERT INTO t VALUES(5,"+value+",50)", "", false)
		}
		s.Exec("SET ROLE comb_group")
		combinationHeldIDs(s, h, 1)
		s.Exec("RESET ROLE")
		s.Exec("DROP POLICY ins ON t")
		s.Exec("CREATE POLICY ins ON t FOR INSERT WITH CHECK(10/(v-v)>0)")
		s.Exec("SET ROLE comb_reader")
		combinationError(s, "INSERT INTO t VALUES(5,TRUE,50)", "22012", "division by zero", false)
	})
}
