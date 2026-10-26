package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSCheckFeatureSmoke(t *testing.T) {
	rlsCheckCases(t, func(s *membershipScenario) {
		s.exec("CREATE TABLE t(id INT PRIMARY KEY,v INT,g INT GENERATED ALWAYS AS(v*2) STORED)")
		rlsCheckTrigger(s, "t", "1")
		rlsCheckPolicies(s, "g<10")
		s.exec("SET ROLE check_writer")
		s.run(fixture.Exec("INSERT INTO t(id,v) VALUES(1,3)", 1))
		s.exec("RESET ROLE")
		rlsCheckGeneratedRows(s, [][]any{{int64(1), int64(4), int64(8)}})
		s.exec("SET ROLE check_writer")
		combinationDeny(s, "INSERT INTO t(id,v) VALUES(2,4)", "", false)
		s.exec("BEGIN")
		defer s.c.ExecContext(s.p.Ctx, "ROLLBACK")
		s.run(fixture.Exec("INSERT INTO t(id,v) VALUES(2,1)", 1))
		s.exec("SAVEPOINT keep")
		rlsCheckDeny(s, "UPDATE t SET v=CASE WHEN id=1 THEN 2 ELSE 4 END WHERE id IN(1,2) RETURNING id,v,g", true)
		rlsCheckAborted(s)
		s.exec("ROLLBACK TO keep")
		rlsCheckGeneratedRows(s, [][]any{{int64(1), int64(4), int64(8)}, {int64(2), int64(2), int64(4)}})
		s.run(fixture.Exec("UPDATE t SET v=2 WHERE id=1", 1))
		s.exec("COMMIT")
		s.exec("RESET ROLE")
		rlsCheckGeneratedRows(s, [][]any{{int64(1), int64(3), int64(6)}, {int64(2), int64(2), int64(4)}})
	})
}
