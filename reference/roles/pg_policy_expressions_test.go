package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// These independent ruleutils-style expectations are deliberately exact. New
// default/pretty subquery layout must be resolved by the pinned 18.6 oracle,
// never copied from native output or replaced with replay SQL comparisons.
func TestReferencePGPolicyExpressions(t *testing.T) {
	cases := []struct{ name, expr, normal, pretty string }{
		{"true", "TRUE", "true", "true"},
		{"false", "FALSE", "false", "false"},
		{"null", "NULL", "NULL::boolean", "NULL::boolean"},
		{"predicate", "id>0", "(id > 0)", "id > 0"},
		{"column", "allowed", "allowed", "allowed"},
		{"correlated", "EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id)",
			"(EXISTS ( SELECT 1\n   FROM helper\n  WHERE (helper.id = t.id)))",
			"(EXISTS ( SELECT 1\n   FROM helper\n  WHERE helper.id = t.id))"},
		{"scalar", "(SELECT helper.id>0 FROM helper ORDER BY helper.id LIMIT 1)",
			"( SELECT (helper.id > 0)\n   FROM helper\n  ORDER BY helper.id\n LIMIT 1)",
			"( SELECT helper.id > 0\n   FROM helper\n  ORDER BY helper.id\n LIMIT 1)"},
		{"aggregate", "(SELECT count(*)>0 FROM helper)",
			"( SELECT (count(*) > 0)\n   FROM helper)",
			"( SELECT count(*) > 0\n   FROM helper)"},
		{"window", "(SELECT row_number() OVER(ORDER BY helper.id)>0 FROM helper LIMIT 1)",
			"( SELECT (row_number() OVER (ORDER BY helper.id) > 0)\n   FROM helper\n LIMIT 1)",
			"( SELECT row_number() OVER (ORDER BY helper.id) > 0\n   FROM helper\n LIMIT 1)"},
		{"any", "id=ANY(SELECT helper.id FROM helper)",
			"(id IN ( SELECT helper.id\n   FROM helper))",
			"(id IN ( SELECT helper.id\n   FROM helper))"},
		{"all", "id<=ALL(SELECT helper.id FROM helper)",
			"(id <= ALL ( SELECT helper.id\n   FROM helper))",
			"(id <= ALL ( SELECT helper.id\n   FROM helper))"},
		{"row", "ROW(id,allowed)=(SELECT 1,TRUE)",
			"((id, allowed) = ( SELECT 1,\n    true))",
			"((id, allowed) = ( SELECT 1,\n    true))"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
				policySetup(s)
				s.Exec("CREATE TABLE helper(id INT)")
				s.Exec("CREATE POLICY p ON t USING(" + tc.expr + ") WITH CHECK(" + tc.expr + ")")
				pgPolicyExpressions(s, "t", "p", tc.normal, tc.pretty)
				s.Exec("ALTER POLICY p ON t USING(" + tc.expr + ")")
				pgPolicyExpressions(s, "t", "p", tc.normal, tc.pretty)
				policyRows(s)
			})
		})
	}
	t.Run("renamed_correlation", func(t *testing.T) {
		fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
			policySetup(s)
			s.Exec("CREATE TABLE helper(id INT)")
			s.Exec("CREATE POLICY p ON t USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id)) WITH CHECK(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id))")
			s.Exec("ALTER TABLE helper RENAME TO renamed_helper")
			s.Exec("ALTER TABLE renamed_helper RENAME COLUMN id TO helper_key")
			s.Exec("ALTER TABLE t RENAME COLUMN id TO target_key")
			s.Exec("ALTER TABLE t RENAME TO renamed_target")
			s.Exec("CREATE TABLE helper(id INT)")
			s.Exec("CREATE TABLE t(id INT)")
			pgPolicyExpressions(s, "renamed_target", "p",
				"(EXISTS ( SELECT 1\n   FROM renamed_helper\n  WHERE (renamed_helper.helper_key = renamed_target.target_key)))",
				"(EXISTS ( SELECT 1\n   FROM renamed_helper\n  WHERE renamed_helper.helper_key = renamed_target.target_key))")
		})
	})
}
