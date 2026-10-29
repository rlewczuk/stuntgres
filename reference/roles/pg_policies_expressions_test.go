package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGPoliciesExpressions(t *testing.T) {
	cases := []struct{ name, sql, want string }{
		{"true", "TRUE", "true"},
		{"false", "FALSE", "false"},
		{"null", "NULL", "NULL::boolean"},
		{"predicate", "id>0", "(id > 0)"},
		{"column", "allowed", "allowed"},
		{"identity", "current_user=current_role", "(CURRENT_USER = CURRENT_ROLE)"},
		{"correlated", "EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id)", "(EXISTS ( SELECT 1\n   FROM helper\n  WHERE (helper.id = t.id)))"},
		{"scalar", "(SELECT helper.id>0 FROM helper ORDER BY helper.id LIMIT 1)", "( SELECT (helper.id > 0)\n   FROM helper\n  ORDER BY helper.id\n LIMIT 1)"},
		{"aggregate", "(SELECT count(*)>0 FROM helper)", "( SELECT (count(*) > 0)\n   FROM helper)"},
		{"window", "(SELECT row_number() OVER(ORDER BY helper.id)>0 FROM helper LIMIT 1)", "( SELECT (row_number() OVER (ORDER BY helper.id) > 0)\n   FROM helper\n LIMIT 1)"},
		{"any", "id=ANY(SELECT helper.id FROM helper)", "(id IN ( SELECT helper.id\n   FROM helper))"},
		{"all", "id<=ALL(SELECT helper.id FROM helper)", "(id <= ALL ( SELECT helper.id\n   FROM helper))"},
		{"row", "ROW(id,allowed)=(SELECT 1,TRUE)", "((id, allowed) = ( SELECT 1,\n    true))"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pgPoliciesCases(t, nil, func(s *fixture.RoleMembershipScenario) {
				s.Exec("CREATE TABLE t(id INT,allowed BOOL)")
				s.Exec("CREATE TABLE helper(id INT)")
				s.Exec("CREATE POLICY p ON t USING("+tc.sql+")")
				pgPoliciesRead(s, pgPoliciesRow("t", "p", "PERMISSIVE", "{public}", "ALL", tc.want, nil))
				s.Exec("ALTER POLICY p ON t WITH CHECK("+tc.sql+")")
				pgPoliciesRead(s, pgPoliciesRow("t", "p", "PERMISSIVE", "{public}", "ALL", tc.want, tc.want))
				s.Exec("CREATE POLICY check_only ON t WITH CHECK("+tc.sql+")")
				pgPoliciesRead(s,
					pgPoliciesRow("t", "check_only", "PERMISSIVE", "{public}", "ALL", nil, tc.want),
					pgPoliciesRow("t", "p", "PERMISSIVE", "{public}", "ALL", tc.want, tc.want))
			})
		})
	}
	t.Run("renamed_correlation", func(t *testing.T) {
		pgPoliciesCases(t, nil, func(s *fixture.RoleMembershipScenario) {
			s.Exec("CREATE TABLE t(id INT,allowed BOOL)")
			s.Exec("CREATE TABLE helper(id INT)")
			s.Exec("CREATE POLICY p ON t USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id))")
			saved := pgPoliciesSnapshot(s)
			s.Exec("ALTER TABLE helper RENAME TO renamed_helper")
			s.Exec("ALTER TABLE renamed_helper RENAME COLUMN id TO helper_key")
			s.Exec("ALTER TABLE t RENAME COLUMN id TO target_key")
			s.Exec("ALTER TABLE t RENAME TO renamed_target")
			s.Exec("CREATE TABLE helper(id INT)")
			s.Exec("CREATE TABLE t(id INT)")
			pgPoliciesRead(s, pgPoliciesRow("renamed_target", "p", "PERMISSIVE", "{public}", "ALL", "(EXISTS ( SELECT 1\n   FROM renamed_helper\n  WHERE (renamed_helper.helper_key = renamed_target.target_key)))", nil))
			saved()
		})
	})
}

func TestReferencePGPoliciesNoExecution(t *testing.T) {
	pgPoliciesCases(t, nil, func(s *fixture.RoleMembershipScenario) {
		s.Exec("CREATE TABLE t(id INT,allowed BOOL)")
		s.Exec("CREATE TABLE helper(id INT)")
		s.Run(fixture.Exec("INSERT INTO t VALUES(1,TRUE),(2,FALSE)", 2))
		s.Run(fixture.Exec("INSERT INTO helper VALUES(9)", 1))
		s.Exec("CREATE SEQUENCE policies_sequence START 41")
		s.Exec("CREATE POLICY p ON t USING(nextval('policies_sequence')>0)")
		for range 2 {
			pgPoliciesRead(s, pgPoliciesRow("t", "p", "PERMISSIVE", "{public}", "ALL", "(nextval('policies_sequence'::regclass) > 0)", nil))
		}
		s.Run(fixture.Select("SELECT nextval('policies_sequence') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(41)}}))
		s.Run(fixture.Select("SELECT id,allowed FROM t ORDER BY id", []string{"id", "allowed"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}}))
		s.Run(fixture.Select("SELECT id FROM helper", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
		s.Run(fixture.Select("SELECT relrowsecurity FROM pg_class WHERE oid='t'::regclass", []string{"relrowsecurity"}, []string{"BOOL"}, [][]any{{false}}))
	})
}
