package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRLSCheckRows(t *testing.T) {
	t.Run("insert_select_and_new_not_old", func(t *testing.T) {
		rlsCheckCases(t, func(s *membershipScenario) {
			s.exec("CREATE TABLE t(id INT PRIMARY KEY,v INT)")
			s.run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
			rlsCheckPolicies(s, "v<5")
			s.exec("ALTER POLICY upd ON t USING(v>0)")
			s.exec("SET ROLE check_writer")
			s.run(fixture.Exec("INSERT INTO t SELECT 2,4", 1))
			s.run(fixture.Exec("UPDATE t SET v=3 WHERE id=1", 1))
			combinationDeny(s, "UPDATE t SET v=8 WHERE id=1", "", false)
			s.exec("RESET ROLE")
			rlsCheckRows(s, [][]any{{int64(1), int64(3)}, {int64(2), int64(4)}})
		})
	})
	for _, operation := range []string{"insert", "update"} {
		t.Run("all_fallback_"+operation, func(t *testing.T) {
			rlsCheckCases(t, func(s *membershipScenario) {
				s.exec("CREATE TABLE t(id INT PRIMARY KEY,v INT)")
				s.run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				rlsCheckEnable(s)
				s.exec("CREATE POLICY sel ON t FOR SELECT USING(TRUE)")
				s.exec("CREATE POLICY all_rows ON t FOR ALL USING(v>0)")
				q := "INSERT INTO t VALUES(2,0)"
				if operation == "update" {
					q = "UPDATE t SET v=0 WHERE id=1"
				}
				s.exec("SET ROLE check_writer")
				combinationDeny(s, q, "", false)
				s.exec("RESET ROLE")
				rlsCheckRows(s, [][]any{{int64(1), int64(10)}})
				s.exec("ALTER POLICY all_rows ON t WITH CHECK(v>=0)")
				s.exec("SET ROLE check_writer")
				s.run(fixture.Exec(q, 1))
				s.exec("RESET ROLE")
				want := [][]any{{int64(1), int64(10)}, {int64(2), int64(0)}}
				if operation == "update" {
					want = [][]any{{int64(1), int64(0)}}
				}
				rlsCheckRows(s, want)
			})
		})
	}
}

func TestReferenceRLSCheckConstraintPrecedence(t *testing.T) {
	for _, operation := range []string{"insert", "update"} {
		for _, constraint := range []string{"not_null", "check"} {
			t.Run(operation+"/"+constraint, func(t *testing.T) {
				rlsCheckCases(t, func(s *membershipScenario) {
					s.exec("CREATE TABLE t(id INT PRIMARY KEY,allowed BOOL,v INT NOT NULL,CONSTRAINT positive CHECK(v>0))")
					s.run(fixture.Exec("INSERT INTO t VALUES(1,TRUE,10)", 1))
					rlsCheckPolicies(s, "allowed")
					value := "NULL"
					want := fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Schema: "public", Table: "t", Column: "v"}
					if constraint == "check" {
						value = "0"
						want = fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "positive"`, Schema: "public", Table: "t", Constraint: "positive"}
					}
					statement := func(allowed string) string {
						if operation == "update" {
							return "UPDATE t SET allowed=" + allowed + ",v=" + value + " WHERE id=1"
						}
						return "INSERT INTO t VALUES(2," + allowed + "," + value + ")"
					}
					s.exec("SET ROLE check_writer")
					combinationDeny(s, statement("FALSE"), "", false)
					predefinedFailure(s, statement("TRUE"), want, false, "execute")
					s.exec("RESET ROLE")
					s.run(fixture.Select("SELECT id,allowed,v FROM t ORDER BY id", []string{"id", "allowed", "v"}, []string{"INT4", "BOOL", "INT4"}, [][]any{{int64(1), true, int64(10)}}))
				})
			})
		}
	}
}
