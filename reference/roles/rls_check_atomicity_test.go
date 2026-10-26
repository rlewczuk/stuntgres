package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSCheckAtomicity(t *testing.T) {
	for _, operation := range []string{"values", "select", "update"} {
		for _, query := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/returning=%v", operation, query), func(t *testing.T) {
				rlsCheckCases(t, func(s *membershipScenario) {
					s.exec("CREATE TABLE t(id INT PRIMARY KEY,v INT)")
					s.run(fixture.Exec("INSERT INTO t VALUES(1,1),(2,2)", 2))
					rlsCheckPolicies(s, "v<10")
					seed := [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}
					q := "INSERT INTO t VALUES(6,6),(7,70)"
					switch operation {
					case "select":
						q = "INSERT INTO t SELECT 6,6 UNION ALL SELECT 7,70"
					case "update":
						q = "UPDATE t SET v=CASE WHEN id=1 THEN 3 ELSE 70 END WHERE id IN(1,2)"
					}
					if query {
						q += " RETURNING id,v"
					}
					s.exec("SET ROLE check_writer")
					rlsCheckDeny(s, q, query)
					s.exec("RESET ROLE")
					rlsCheckRows(s, seed)
					s.exec("SET ROLE check_writer")
					s.exec("BEGIN")
					defer s.c.ExecContext(s.p.Ctx, "ROLLBACK")
					s.run(fixture.Exec("INSERT INTO t VALUES(5,5)", 1))
					s.exec("SAVEPOINT keep")
					// The owner peer cannot observe the earlier uncommitted write.
					fixture.RunStep(s.t, s.p.Ctx, s.backend.DB, s.backend.Recorder, fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, seed))
					rlsCheckDeny(s, q, query)
					rlsCheckAborted(s)
					s.exec("ROLLBACK TO keep")
					rlsCheckRows(s, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(5), int64(5)}})
					fixture.RunStep(s.t, s.p.Ctx, s.backend.DB, s.backend.Recorder, fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, seed))
					s.run(fixture.Exec("INSERT INTO t VALUES(8,8)", 1))
					s.exec("COMMIT")
					s.exec("RESET ROLE")
					want := [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(5), int64(5)}, {int64(8), int64(8)}}
					rlsCheckRows(s, want)
					fixture.RunStep(s.t, s.p.Ctx, s.backend.DB, s.backend.Recorder, fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
				})
			})
		}
	}
}
