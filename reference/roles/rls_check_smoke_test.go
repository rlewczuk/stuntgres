package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSCheckFeatureSmoke(t *testing.T) {
	rlsCheckCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Exec("CREATE TABLE t(id INT PRIMARY KEY,v INT,g INT GENERATED ALWAYS AS(v*2) STORED)")
		rlsCheckTrigger(s, "t", "1")
		rlsCheckPolicies(s, "g<10")
		s.Exec("SET ROLE check_writer")
		s.Run(fixture.Exec("INSERT INTO t(id,v) VALUES(1,3)", 1))
		s.Exec("RESET ROLE")
		rlsCheckGeneratedRows(s, [][]any{{int64(1), int64(4), int64(8)}})
		s.Exec("SET ROLE check_writer")
		combinationDeny(s, "INSERT INTO t(id,v) VALUES(2,4)", "", false)
		s.Exec("BEGIN")
		defer s.C.ExecContext(s.P.Ctx, "ROLLBACK")
		s.Run(fixture.Exec("INSERT INTO t(id,v) VALUES(2,1)", 1))
		s.Exec("SAVEPOINT keep")
		rlsCheckDeny(s, "UPDATE t SET v=CASE WHEN id=1 THEN 2 ELSE 4 END WHERE id IN(1,2) RETURNING id,v,g", true)
		rlsCheckAborted(s)
		s.Exec("ROLLBACK TO keep")
		rlsCheckGeneratedRows(s, [][]any{{int64(1), int64(4), int64(8)}, {int64(2), int64(2), int64(4)}})
		s.Run(fixture.Exec("UPDATE t SET v=2 WHERE id=1", 1))
		s.Exec("COMMIT")
		s.Exec("RESET ROLE")
		rlsCheckGeneratedRows(s, [][]any{{int64(1), int64(3), int64(6)}, {int64(2), int64(2), int64(4)}})
	})
}
