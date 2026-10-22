package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRLSCombinationProposalChecks(t *testing.T) {
	combinationCases(t, func(s *membershipScenario) {
		s.exec("CREATE POLICY sel ON t FOR SELECT USING(TRUE)")
		s.exec("CREATE POLICY upd ON t FOR UPDATE USING(v>0)")
		s.exec("CREATE POLICY ins ON t FOR INSERT WITH CHECK(allowed)")
		s.exec("SET ROLE comb_reader")
		combinationDeny(s, "UPDATE t SET v=0 WHERE id=1", "", false)
		s.run(fixture.Select("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
		s.exec("RESET ROLE")
		s.exec("ALTER POLICY upd ON t WITH CHECK(v>=0)")
		s.exec("SET ROLE comb_reader")
		s.run(fixture.Exec("UPDATE t SET v=0 WHERE id=1", 1))
		s.run(fixture.Exec("UPDATE t SET v=10 WHERE id=1", 0))
		s.run(fixture.Exec("INSERT INTO t VALUES(5,TRUE,50)", 1))
		for _, v := range []string{"FALSE", "NULL"} {
			combinationDeny(s, "INSERT INTO t VALUES(6,"+v+",60)", "", false)
			combinationDeny(s, "INSERT INTO t SELECT 6,"+v+",60", "", false)
		}
		combinationDeny(s, "INSERT INTO t VALUES(6,TRUE,60),(7,FALSE,70)", "", false)
		combinationDeny(s, "INSERT INTO t SELECT 6,TRUE,60 UNION ALL SELECT 7,NULL,70", "", false)
		combinationDeny(s, "UPDATE t SET v=CASE WHEN id=2 THEN 1 ELSE -1 END WHERE id IN(2,3) RETURNING id", "", true)
		combinationDeny(s, "UPDATE t SET v=NULL WHERE id=2", "", false)
		s.exec("RESET ROLE")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2, 3, 4, 5)
		s.run(fixture.Select("SELECT v FROM t WHERE id IN(2,3) ORDER BY id", []string{"v"}, []string{"INT4"}, [][]any{{int64(20)}, {int64(30)}}))
		s.exec("CREATE POLICY r_name ON t AS RESTRICTIVE FOR INSERT WITH CHECK(v<100)")
		s.exec("SET ROLE comb_reader")
		combinationDeny(s, "INSERT INTO t VALUES(6,TRUE,100)", "r_name", false)
		s.exec("RESET ROLE")
		s.exec("ALTER POLICY r_name ON t WITH CHECK(NULL)")
		s.exec("SET ROLE comb_reader")
		combinationDeny(s, "INSERT INTO t VALUES(6,TRUE,60)", "r_name", false)
	})
}
func TestReferenceRLSCombinationSelectNewChecks(t *testing.T) {
	combinationCases(t, func(s *membershipScenario) {
		s.exec("CREATE POLICY all_rows ON t USING(id<10) WITH CHECK(TRUE)")
		s.exec("SET ROLE comb_reader")
		s.run(fixture.Exec("INSERT INTO t VALUES(10,TRUE,10)", 1))
		combinationDeny(s, "INSERT INTO t VALUES(11,TRUE,11) RETURNING id", "", true)
		combinationDeny(s, "UPDATE t SET id=12 WHERE id=1", "", false)
		s.run(fixture.Exec("UPDATE t SET v=99", 4))
		s.exec("RESET ROLE")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2, 3, 4, 10)
		s.exec("DROP POLICY all_rows ON t")
		s.exec("CREATE POLICY sel ON t FOR SELECT USING(id<10)")
		s.exec("CREATE POLICY ins ON t FOR INSERT WITH CHECK(TRUE)")
		s.exec("CREATE POLICY upd ON t FOR UPDATE USING(TRUE) WITH CHECK(TRUE)")
		s.exec("SET ROLE comb_reader")
		combinationDeny(s, "INSERT INTO t VALUES(11,TRUE,11) RETURNING id", "", true)
		combinationDeny(s, "UPDATE t SET id=12 WHERE id=1", "", false)
		s.run(fixture.Exec("UPDATE t SET v=98", 5))
	})
}
func TestReferenceRLSCombinationEvaluationErrors(t *testing.T) {
	combinationCases(t, func(s *membershipScenario) {
		s.exec("CREATE POLICY danger ON t FOR SELECT USING(10/(id-id)>0)")
		s.exec("SET ROLE comb_reader")
		combinationError(s, "SELECT id FROM t", "22012", "division by zero", true)
		s.exec("RESET ROLE")
		s.exec("ALTER POLICY danger ON t TO comb_other")
		s.exec("CREATE POLICY ok ON t FOR SELECT USING(TRUE)")
		s.exec("CREATE POLICY wrong_command ON t FOR UPDATE USING(10/(id-id)>0)")
		s.exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2, 3, 4)
		s.exec("RESET ROLE")
		s.exec("DROP POLICY ok ON t")
		s.exec("CREATE POLICY restrictive ON t AS RESTRICTIVE FOR SELECT USING(10/(id-id)>0)")
		s.exec("SET ROLE comb_reader")
		combinationIDs(s, "SELECT id FROM t ORDER BY id")
		s.exec("RESET ROLE")
		s.exec("CREATE POLICY ins ON t FOR INSERT WITH CHECK(10/(v-v)>0)")
		s.exec("SET ROLE comb_reader")
		combinationError(s, "INSERT INTO t VALUES(5,TRUE,50)", "22012", "division by zero", false)
		s.exec("RESET ROLE")
		combinationIDs(s, "SELECT id FROM t ORDER BY id", 1, 2, 3, 4)
		s.run(fixture.Exec("DELETE FROM t", 4))
		s.exec("ALTER POLICY danger ON t TO PUBLIC")
		s.exec("SET ROLE comb_reader")
		s.run(fixture.Select("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
