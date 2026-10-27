package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSFilterDMLSelectPrivilege(t *testing.T) {
	for _, command := range []string{"UPDATE", "DELETE"} {
		t.Run(command, func(t *testing.T) {
			combinationCases(t, func(s *fixture.RoleMembershipScenario) {
				// Reader inherits group: neither grant may supply SELECT here.
				s.Exec("REVOKE SELECT ON t FROM comb_reader,comb_group")
				policy := "CREATE POLICY mutation ON t FOR " + command + " USING(id<=2)"
				plain, read, intersect := "DELETE FROM t", "DELETE FROM t WHERE id=1", "DELETE FROM t WHERE id<=3"
				if command == "UPDATE" {
					policy += " WITH CHECK(TRUE)"
					plain, read, intersect = "UPDATE t SET v=99", "UPDATE t SET v=v+1 WHERE id=1", "UPDATE t SET v=v+1 WHERE id<=3"
				}
				s.Exec(policy)
				s.Exec("SET ROLE comb_reader")
				s.Run(fixture.Exec(plain, 2))
				s.Exec("RESET ROLE")
				if command == "UPDATE" {
					s.Run(fixture.Select("SELECT id,allowed,v FROM t ORDER BY id", []string{"id", "allowed", "v"}, []string{"INT4", "BOOL", "INT4"}, [][]any{
						{int64(1), true, int64(99)}, {int64(2), false, int64(99)}, {int64(3), nil, int64(30)}, {int64(4), true, int64(40)},
					}))
					s.Run(fixture.Exec("UPDATE t SET v=id*10", 4))
				} else {
					s.Run(fixture.Select("SELECT id,allowed,v FROM t ORDER BY id", []string{"id", "allowed", "v"}, []string{"INT4", "BOOL", "INT4"}, [][]any{
						{int64(3), nil, int64(30)}, {int64(4), true, int64(40)},
					}))
					s.Run(fixture.Exec("INSERT INTO t VALUES(1,TRUE,10),(2,FALSE,20)", 2))
				}
				filterSeedRows(s)
				s.Exec("SET ROLE comb_reader")
				predefinedFailure(s, read, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}, false, "execute")
				s.Exec("RESET ROLE")
				filterSeedRows(s)
				s.Exec("GRANT SELECT ON t TO comb_reader")
				s.Exec("SET ROLE comb_reader")
				s.Run(fixture.Exec(read, 0))
				s.Exec("RESET ROLE")
				filterSeedRows(s)
				s.Exec("CREATE POLICY selected ON t FOR SELECT USING(id=2)")
				s.Exec("SET ROLE comb_reader")
				s.Run(fixture.Exec(intersect, 1))
				s.Exec("RESET ROLE")
				if command == "UPDATE" {
					s.Run(fixture.Select("SELECT id,allowed,v FROM t ORDER BY id", []string{"id", "allowed", "v"}, []string{"INT4", "BOOL", "INT4"}, [][]any{
						{int64(1), true, int64(10)}, {int64(2), false, int64(21)}, {int64(3), nil, int64(30)}, {int64(4), true, int64(40)},
					}))
				} else {
					s.Run(fixture.Select("SELECT id,allowed,v FROM t ORDER BY id", []string{"id", "allowed", "v"}, []string{"INT4", "BOOL", "INT4"}, [][]any{
						{int64(1), true, int64(10)}, {int64(3), nil, int64(30)}, {int64(4), true, int64(40)},
					}))
				}
				if len(*s.Notices) != 0 {
					s.T.Fatalf("unexpected notices: %+v", *s.Notices)
				}
			})
		})
	}
}

func TestReferenceRLSFilterOrdering(t *testing.T) {
	combinationCases(t, func(s *fixture.RoleMembershipScenario) {
		s.Exec("ALTER TABLE t ADD COLUMN owner_name TEXT")
		s.Run(fixture.Exec("UPDATE t SET owner_name=CASE WHEN id IN(1,4) THEN 'comb_reader' WHEN id=2 THEN 'comb_group' ELSE NULL END", 4))
		s.Run(fixture.Exec("UPDATE t SET v=0 WHERE id IN(2,3)", 2))
		s.Exec("CREATE POLICY owned ON t USING(owner_name=current_user) WITH CHECK(TRUE)")
		s.Exec("SET ROLE comb_reader")
		// Division depends on the row, avoiding constant-folding semantics.
		s.Run(fixture.Select("SELECT id,100/v AS quotient FROM t WHERE 100/v>0 ORDER BY id", []string{"id", "quotient"}, []string{"INT4", "INT4"}, [][]any{
			{int64(1), int64(10)}, {int64(4), int64(2)},
		}))
		s.Run(fixture.Exec("UPDATE t SET v=100/v WHERE 100/v>0", 2))
		s.Exec("RESET ROLE")
		s.Run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{
			{int64(1), int64(10)}, {int64(2), int64(0)}, {int64(3), int64(0)}, {int64(4), int64(2)},
		}))
		s.Exec("SET ROLE comb_reader")
		s.Run(fixture.Exec("DELETE FROM t WHERE 100/v>0", 2))
		s.Exec("RESET ROLE")
		s.Run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{
			{int64(2), int64(0)}, {int64(3), int64(0)},
		}))
		if len(*s.Notices) != 0 {
			s.T.Fatalf("unexpected notices: %+v", *s.Notices)
		}
	})
}

func TestReferenceRLSFilterEvaluationErrors(t *testing.T) {
	for _, command := range []string{"SELECT", "UPDATE", "DELETE"} {
		t.Run(command, func(t *testing.T) {
			combinationCases(t, func(s *fixture.RoleMembershipScenario) {
				policy := "CREATE POLICY danger ON t FOR " + command + " USING(CASE WHEN id=2 THEN 10/(id-id)>0 ELSE TRUE END)"
				// Aggregate input demands the policy error before any row streams.
				query := "SELECT count(*) FROM t"
				if command == "UPDATE" {
					policy += " WITH CHECK(TRUE)"
					query = "UPDATE t SET v=v+1"
					// Reading v requires the independent SELECT policy group.
					s.Exec("CREATE POLICY selected ON t FOR SELECT USING(TRUE)")
				} else if command == "DELETE" {
					query = "DELETE FROM t"
				}
				s.Exec(policy)
				s.Exec("SET ROLE comb_reader")
				predefinedFailure(s, query, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, command == "SELECT", "execute")
				s.Exec("RESET ROLE")
				filterSeedRows(s)
				if command != "SELECT" {
					s.Exec("BEGIN")
					transactionOpen := true
					defer func() {
						if transactionOpen {
							s.C.ExecContext(s.P.Ctx, "ROLLBACK")
						}
					}()
					s.Exec("SET ROLE comb_reader")
					s.Exec("SAVEPOINT before_failure")
					predefinedFailure(s, query, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, false, "execute")
					s.Exec("ROLLBACK TO SAVEPOINT before_failure")
					s.Exec("RESET ROLE")
					filterSeedRows(s)
					// Recovery admits a real suffix mutation; final rollback removes it.
					s.Run(fixture.Exec("UPDATE t SET v=11 WHERE id=1", 1))
					s.Run(fixture.Select("SELECT id,v FROM t WHERE id=1", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}}))
					s.Exec("ROLLBACK")
					transactionOpen = false
					filterSeedRows(s)
				}
				if len(*s.Notices) != 0 {
					s.T.Fatalf("unexpected notices: %+v", *s.Notices)
				}
			})
		})
	}
}
