package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func policyPosition(q, token string) int {
	i := strings.Index(q, token)
	if i < 0 {
		panic("diagnostic token absent")
	}
	return utf8.RuneCountInString(q[:i]) + 1
}

func TestReferenceRLSPolicyDiagnostics(t *testing.T) {
	// REL_18_0 policy.c: CREATE validates command/clauses, then roles, owner,
	// expressions and finally duplicates. ALTER validates roles before lookup.
	cases := []struct{ name, setup, q, code, message, detail, hint, token, stage string }{
		{"duplicate", "CREATE POLICY p ON t", "CREATE POLICY p ON t", "42710", `policy "p" for table "t" already exists`, "", "", "", "execute"},
		{"missing_alter", "", "ALTER POLICY p ON t", "42704", `policy "p" for table "t" does not exist`, "", "", "", "execute"},
		{"missing_rename", "", "ALTER POLICY p ON t RENAME TO q", "42704", `policy "p" for table "t" does not exist`, "", "", "", "execute"},
		{"missing_drop", "", "DROP POLICY p ON t", "42704", `policy "p" for table "t" does not exist`, "", "", "", "execute"},
		{"role", "", "CREATE POLICY p ON t TO policy_missing", "42704", `role "policy_missing" does not exist`, "", "", "", "execute"},
		{"reserved_role", "", "CREATE POLICY p ON t TO none", "42939", `role name "none" is reserved`, "", "", "none", "prepare"},
		{"select_check", "", "CREATE POLICY p ON t FOR SELECT WITH CHECK(TRUE)", "42601", "WITH CHECK cannot be applied to SELECT or DELETE", "", "", "", "execute"},
		{"delete_check", "", "CREATE POLICY p ON t FOR DELETE WITH CHECK(TRUE)", "42601", "WITH CHECK cannot be applied to SELECT or DELETE", "", "", "", "execute"},
		{"insert_using", "", "CREATE POLICY p ON t FOR INSERT USING(TRUE)", "42601", "only WITH CHECK expression allowed for INSERT", "", "", "", "execute"},
		{"alter_select_check", "CREATE POLICY p ON t FOR SELECT", "ALTER POLICY p ON t WITH CHECK(TRUE)", "42601", "only USING expression allowed for SELECT, DELETE", "", "", "", "execute"},
		{"alter_delete_check", "CREATE POLICY p ON t FOR DELETE", "ALTER POLICY p ON t WITH CHECK(TRUE)", "42601", "only USING expression allowed for SELECT, DELETE", "", "", "", "execute"},
		{"alter_insert_using", "CREATE POLICY p ON t FOR INSERT", "ALTER POLICY p ON t USING(TRUE)", "42601", "only WITH CHECK expression allowed for INSERT", "", "", "", "execute"},
		{"integer", "", "CREATE POLICY p ON t USING(1)", "42804", "argument of POLICY must be type boolean, not type integer", "", "", "", "execute"},
		{"text", "", "CREATE POLICY p ON t WITH CHECK('x'::text)", "42804", "argument of POLICY must be type boolean, not type text", "", "", "", "execute"},
		{"bad_boolean", "", "CREATE POLICY p ON t USING('not_boolean')", "22P02", `invalid input syntax for type boolean: "not_boolean"`, "", "", "", "execute"},
		{"column", "", "CREATE POLICY p ON t USING(missing)", "42703", `column "missing" does not exist`, "", "", "", "execute"},
		{"parameter", "", "CREATE POLICY p ON t USING($1)", "42P02", "there is no parameter $1", "", "", "", "execute"},
		{"nested_parameter", "", "CREATE POLICY p ON t USING(EXISTS(SELECT $1))", "42P02", "there is no parameter $1", "", "", "", "execute"},
		{"aggregate", "", "CREATE POLICY p ON t USING(count(*)>0)", "42803", "aggregate functions are not allowed in policy expressions", "", "", "", "execute"},
		{"dead_aggregate", "", "CREATE POLICY p ON t USING(FALSE AND count(*)>0)", "42803", "aggregate functions are not allowed in policy expressions", "", "", "", "execute"},
		{"window", "", "CREATE POLICY p ON t USING(row_number() OVER()>0)", "42P20", "window functions are not allowed in policy expressions", "", "", "", "execute"},
		{"dead_window", "", "CREATE POLICY p ON t USING(CASE WHEN TRUE THEN TRUE ELSE row_number() OVER()>0 END)", "42P20", "window functions are not allowed in policy expressions", "", "", "", "execute"},
		{"option", "", "CREATE POLICY p ON t AS bogus", "42601", `unrecognized row security option "bogus"`, "", "Only PERMISSIVE or RESTRICTIVE policies are supported currently.", "bogus", "prepare"},
		{"view", "CREATE VIEW v AS SELECT id FROM t", "CREATE POLICY p ON v", "42809", `"v" is not a table`, "", "", "", "execute"},
		{"system", "", "CREATE POLICY p ON pg_catalog.pg_class", "42501", `permission denied: "pg_class" is a system catalog`, "", "", "", "execute"},
		{"missing_table", "", "CREATE POLICY p ON missing", "42P01", `relation "missing" does not exist`, "", "", "", "execute"},
		{"missing_schema", "", "CREATE POLICY p ON missing.t", "3F000", `schema "missing" does not exist`, "", "", "", "execute"},
		{"drop_missing_table", "", "DROP POLICY p ON missing", "42P01", `relation "missing" does not exist`, "", "", "", "execute"},
		{"duplicate_after_expression", "CREATE POLICY p ON t", "CREATE POLICY p ON t USING(1)", "42804", "argument of POLICY must be type boolean, not type integer", "", "", "", "execute"},
		{"missing_after_expression", "", "ALTER POLICY absent ON t USING(1)", "42804", "argument of POLICY must be type boolean, not type integer", "", "", "", "execute"},
		{"command_before_role", "", "CREATE POLICY p ON t FOR INSERT TO policy_missing USING(TRUE)", "42601", "only WITH CHECK expression allowed for INSERT", "", "", "", "execute"},
		{"role_before_missing", "", "ALTER POLICY absent ON t TO policy_missing", "42704", `role "policy_missing" does not exist`, "", "", "", "execute"},
		{"rename_conflict_before_missing", "CREATE POLICY q ON t", "ALTER POLICY absent ON t RENAME TO q", "42710", `policy "q" for table "t" already exists`, "", "", "", "execute"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
				policySetup(s)
				if tc.setup != "" {
					s.Exec(tc.setup)
				}
				position := 0
				if tc.token != "" {
					position = policyPosition(tc.q, tc.token)
				}
				step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint, Position: fixture.Position(position)})
				step.ErrorStage = "execute"
				if s.Prepared {
					step.ErrorStage = tc.stage
				}
				s.Run(step)
				policyRows(s)
			})
		})
	}
	grammar := []struct{ name, q, token string }{
		{"replace", "CREATE OR REPLACE POLICY p ON t", "POLICY"},
		{"create_if_exists", "CREATE POLICY IF NOT EXISTS p ON t", "NOT"},
		{"alter_if_exists", "ALTER POLICY IF EXISTS p ON t", ""},
		{"alter_mode", "ALTER POLICY p ON t AS RESTRICTIVE", "AS"},
		{"alter_command", "ALTER POLICY p ON t FOR SELECT", "FOR"},
		{"invalid_command", "CREATE POLICY p ON t FOR bogus", "bogus"},
		{"mixed_rename", "ALTER POLICY p ON t RENAME TO q USING(TRUE)", "USING"},
		{"suffix", "CREATE POLICY p ON t; nonsense", "nonsense"},
		{"utf8_suffix", "CREATE POLICY \"策略\" ON t nonsense", "nonsense"},
		{"role_order", "CREATE POLICY p ON t USING(TRUE) TO PUBLIC", "TO"},
		{"expression_order", "CREATE POLICY p ON t WITH CHECK(TRUE) USING(TRUE)", "USING"},
		{"empty_using", "CREATE POLICY p ON t USING()", ")"},
		{"empty_check", "CREATE POLICY p ON t WITH CHECK()", ")"},
		{"alter_role_order", "ALTER POLICY p ON t USING(TRUE) TO PUBLIC", "TO"},
	}
	for _, tc := range grammar {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
				policySetup(s)
				message := `syntax error at or near "` + tc.token + `"`
				position := utf8.RuneCountInString(tc.q) + 1
				if tc.token == "" {
					message = "syntax error at end of input"
				} else {
					position = policyPosition(tc.q, tc.token)
				}
				step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42601", Message: message, Position: fixture.Position(position)})
				step.ErrorStage = "execute"
				if s.Prepared {
					step.ErrorStage = "prepare"
				}
				s.Run(step)
				// Whole-input failure cannot create the valid prefix policy.
				s.Exec("CREATE POLICY p ON t")
			})
		})
	}
	fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
		policySetup(s)
		for _, tc := range []struct{ q, message string }{
			{"DROP POLICY IF EXISTS absent ON t", `policy "absent" for relation "t" does not exist, skipping`},
			{"DROP POLICY IF EXISTS p ON missing", `relation "missing" does not exist, skipping`},
			{"DROP POLICY IF EXISTS p ON missing.t", `schema "missing" does not exist, skipping`},
		} {
			s.Exec(tc.q)
			policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: tc.message})
		}
		policyRows(s)
	})
}
