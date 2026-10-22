package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRLSCombinationHeldRole(t *testing.T) {
	combinationCases(t, func(s *membershipScenario) {
		combinationPolicies(s)
		h := combinationHeld(s, "SELECT id FROM t ORDER BY id")
		combinationHeldIDs(s, h, 1, 2, 3, 4)
		s.exec("SET ROLE comb_reader")
		combinationHeldIDs(s, h, 1, 2, 3)
		s.exec("SET ROLE comb_group")
		combinationHeldIDs(s, h, 1, 3)
		s.exec("RESET ROLE")
		combinationHeldIDs(s, h, 1, 2, 3, 4)
		// A live executor retains its admitted quals and nested helper pins, while
		// expression identity callbacks use the FETCH operation, not ended DECLARE.
		s.exec("CREATE TABLE cursor_helper(id INT)")
		s.run(fixture.Exec("INSERT INTO cursor_helper VALUES(1),(2),(3)", 3))
		s.exec("GRANT SELECT ON cursor_helper TO comb_reader,comb_group")
		s.exec("CREATE POLICY cursor_helper_guard ON t AS RESTRICTIVE FOR SELECT USING(EXISTS(SELECT 1 FROM cursor_helper h WHERE h.id=t.id))")
		s.exec("SET ROLE comb_reader")
		s.exec("BEGIN")
		open := true
		defer func() {
			if open {
				_, _ = s.c.ExecContext(s.p.Ctx, "ROLLBACK")
			}
		}()
		s.exec("DECLARE combination_cursor NO SCROLL CURSOR FOR SELECT id,current_user AS actor,session_user AS login FROM t ORDER BY id")
		s.exec("SET ROLE comb_group")
		s.run(fixture.Select("FETCH ALL FROM combination_cursor", []string{"id", "actor", "login"}, []string{"INT4", "NAME", "NAME"}, [][]any{
			{int64(1), "comb_group", s.bootstrap}, {int64(2), "comb_group", s.bootstrap}, {int64(3), "comb_group", s.bootstrap},
		}))
		combinationHeldIDs(s, h, 1, 3)
		s.exec("CLOSE combination_cursor")
		s.exec("COMMIT")
		open = false
		s.exec("RESET ROLE")
	})
}
func TestReferenceRLSCombinationHeldPolicy(t *testing.T) {
	combinationCases(t, func(s *membershipScenario) {
		s.exec("CREATE POLICY sel ON t FOR SELECT USING(TRUE)")
		s.exec("CREATE POLICY ins ON t FOR INSERT WITH CHECK(TRUE)")
		s.exec("CREATE POLICY upd ON t FOR UPDATE USING(TRUE) WITH CHECK(TRUE)")
		s.exec("SET ROLE comb_reader")
		selectH := combinationHeld(s, "SELECT id FROM t ORDER BY id")
		insertH := combinationHeld(s, "INSERT INTO t VALUES(5,TRUE,50)")
		updateH := combinationHeld(s, "UPDATE t SET v=99 WHERE id=1")
		combinationHeldIDs(s, selectH, 1, 2, 3, 4)
		fixture.RunStep(s.t, s.p.Ctx, insertH, s.record, fixture.Exec(insertH.SQL, 1))
		fixture.RunStep(s.t, s.p.Ctx, updateH, s.record, fixture.Exec(updateH.SQL, 1))
		s.exec("RESET ROLE")
		s.run(fixture.Exec("DELETE FROM t WHERE id=5", 1))
		s.exec("ALTER POLICY ins ON t WITH CHECK(FALSE)")
		s.exec("ALTER POLICY upd ON t WITH CHECK(FALSE)")
		s.exec("SET ROLE comb_reader")
		for _, h := range []fixture.HeldStatement{insertH, updateH} {
			step := fixture.ImplicitError(h.SQL, fixture.Diagnostic{Code: "42501", Message: `new row violates row-level security policy for table "t"`})
			fixture.RunStep(s.t, s.p.Ctx, h, s.record, step)
		}
		combinationHeldIDs(s, selectH, 1, 2, 3, 4)
		s.exec("RESET ROLE")
		s.exec("ALTER POLICY sel ON t USING(FALSE)")
		s.exec("SET ROLE comb_reader")
		combinationHeldIDs(s, selectH)
		s.exec("RESET ROLE")
		s.exec("DROP POLICY sel ON t")
		s.exec("SET ROLE comb_reader")
		combinationHeldIDs(s, selectH)
		s.exec("RESET ROLE")
		s.exec("CREATE POLICY sel ON t FOR SELECT USING(id=2)")
		s.exec("ALTER POLICY sel ON t RENAME TO replacement")
		s.exec("SET ROLE comb_reader")
		combinationHeldIDs(s, selectH, 2)
		s.exec("RESET ROLE")
		s.run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(99)}, {int64(2), int64(20)}, {int64(3), int64(30)}, {int64(4), int64(40)}}))
	})
}
func TestReferenceRLSCombinationFeatureSmoke(t *testing.T) {
	combinationCases(t, func(s *membershipScenario) {
		combinationPolicies(s)
		s.exec("CREATE POLICY r ON t AS RESTRICTIVE FOR SELECT USING(CASE WHEN id<3 THEN TRUE ELSE NULL END)")
		s.exec("CREATE POLICY upd ON t FOR UPDATE USING(id=2) WITH CHECK(TRUE)")
		s.exec("CREATE POLICY ins ON t FOR INSERT WITH CHECK(allowed)")
		h := combinationHeld(s, "SELECT id FROM t ORDER BY id")
		s.exec("SET ROLE comb_reader")
		combinationHeldIDs(s, h, 1, 2)
		combinationIDs(s, "UPDATE t SET v=99 WHERE id>0 RETURNING id", 2)
		for _, value := range []string{"FALSE", "NULL"} {
			combinationDeny(s, "INSERT INTO t VALUES(5,"+value+",50)", "", false)
		}
		s.exec("SET ROLE comb_group")
		combinationHeldIDs(s, h, 1)
		s.exec("RESET ROLE")
		s.exec("DROP POLICY ins ON t")
		s.exec("CREATE POLICY ins ON t FOR INSERT WITH CHECK(10/(v-v)>0)")
		s.exec("SET ROLE comb_reader")
		combinationError(s, "INSERT INTO t VALUES(5,TRUE,50)", "22012", "division by zero", false)
	})
}
