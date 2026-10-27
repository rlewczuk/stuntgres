package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRLSPolicyExpressions(t *testing.T) {
	cases := []struct{ name, expr string }{
		{"true", "TRUE"}, {"false", "FALSE"}, {"null", "NULL"}, {"unknown", "'yes'"},
		{"qualified", "t.id>0 AND t.allowed IS NOT NULL"},
		{"case", "CASE WHEN allowed THEN id>0 ELSE FALSE END"},
		{"identity", "current_user=current_role OR session_user=current_user"},
		{"stable", "CURRENT_DATE IS NOT NULL"},
		{"volatile", "random()>=0"},
		// No definition-time evaluation, including an expression that would fail
		// for one existing row. Avoid constant folding as an oracle assumption.
		{"no_evaluation", "10/(id-1)>0"},
		{"aggregate_subquery", "(SELECT count(*)>0 FROM helper)"},
		{"correlated", "EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id)"},
		{"scalar", "(SELECT helper.id>0 FROM helper ORDER BY helper.id LIMIT 1)"},
		{"window_subquery", "(SELECT row_number() OVER(ORDER BY helper.id)>0 FROM helper LIMIT 1)"},
		{"any", "id=ANY(SELECT helper.id FROM helper)"},
		{"all", "id<=ALL(SELECT helper.id FROM helper)"},
		{"row", "ROW(id,allowed)=(SELECT 1,TRUE)"},
		{"domain", "id::policy_integer>0"},
		{"collation", "'a' COLLATE policy_collation='a'"},
		{"sequence", "nextval('policy_sequence')>0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
				policySetup(s)
				s.Exec("CREATE TABLE helper(id INTEGER)")
				s.Run(fixture.Exec("INSERT INTO helper VALUES(1),(2)", 2))
				s.Exec("CREATE DOMAIN policy_integer AS INTEGER")
				s.Exec("CREATE COLLATION policy_collation FROM \"C\"")
				s.Exec("CREATE SEQUENCE policy_sequence START 41")
				s.Exec("CREATE POLICY p ON t USING(" + tc.expr + ") WITH CHECK(" + tc.expr + ")")
				// Creation neither evaluates sequence conditions nor scans rows.
				s.Run(fixture.Select("SELECT nextval('policy_sequence') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(41)}}))
				policyRows(s)
				s.Exec("ALTER POLICY p ON t USING(" + tc.expr + ")")
				if !s.Backend.PG {
					var before, after bytes.Buffer
					if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &before); err != nil {
						s.T.Fatal(err)
					}
					if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(before.String())); err != nil {
						s.T.Fatal(err)
					}
					if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &after); err != nil {
						s.T.Fatal(err)
					}
					if before.String() != after.String() {
						s.T.Fatalf("unstable policy expression roundtrip\nbefore=%s\nafter=%s", before.String(), after.String())
					}
					policyRows(s)
				}
				s.Exec("DROP POLICY p ON t")
			})
		})
	}
	fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
		policySetup(s)
		s.Exec("CREATE TABLE helper(id INTEGER)")
		s.Exec("CREATE POLICY p ON t USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id)) WITH CHECK(allowed)")
		s.Exec("ALTER TABLE helper RENAME TO renamed_helper")
		s.Exec("ALTER TABLE renamed_helper RENAME COLUMN id TO helper_key")
		s.Exec("ALTER TABLE t RENAME COLUMN id TO target_key")
		s.Exec("ALTER TABLE t RENAME TO renamed_target")
		// Reusing old spellings must not retarget the frozen definition.
		s.Exec("CREATE TABLE helper(id INTEGER)")
		s.Exec("CREATE TABLE t(id INTEGER)")
		s.Exec("DROP TABLE helper")
		s.Exec("DROP TABLE t")
		if !s.Backend.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			if !strings.Contains(dump.String(), "helper_key") || !strings.Contains(dump.String(), "target_key") {
				s.T.Fatal("renamed identities absent", dump.String())
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(dump.String())); err != nil {
				s.T.Fatal(err)
			}
		}
		s.Exec("DROP TABLE renamed_helper CASCADE")
		policyError(s, "ALTER POLICY p ON renamed_target", "42704", `policy "p" for table "renamed_target" does not exist`, "", 0)
		s.Run(fixture.Select("SELECT target_key,allowed FROM renamed_target ORDER BY target_key", []string{"target_key", "allowed"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}}))
	})
}

func TestReferenceRLSPolicyExpressionsRegtype(t *testing.T) {
	fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
		policySetup(s)
		s.Exec("CREATE TABLE policy_type(id INTEGER)")
		s.Exec("CREATE DOMAIN policy_integer AS INTEGER")
		s.Exec("CREATE POLICY p ON t USING('policy_type'::regtype IS NOT NULL AND 'policy_integer'::regtype IS NOT NULL AND CURRENT_USER IS NOT NULL) WITH CHECK(NULL::regtype IS NULL)")
		s.Exec("CREATE VIEW policy_type_view AS SELECT 'policy_type'::regtype AS type_identity")
		s.Exec("ALTER TABLE policy_type RENAME TO renamed_policy_type")
		// Reusing the original spelling must not retarget either frozen leaf.
		s.Exec("CREATE TABLE policy_type(id INTEGER)")
		assertIdentity := func() {
			s.Run(fixture.Select("SELECT type_identity='renamed_policy_type'::regtype AS same_identity,type_identity='policy_type'::regtype AS replacement_identity FROM policy_type_view", []string{"same_identity", "replacement_identity"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
			policyRows(s)
		}
		assertIdentity()
		s.Exec("ALTER POLICY p ON t")
		if !s.Backend.PG {
			dump := policyDump(s)
			foundPolicy := false
			for _, line := range strings.Split(dump, "\n") {
				if !strings.HasPrefix(line, "CREATE POLICY ") {
					continue
				}
				foundPolicy = true
				if !strings.Contains(line, `'"public"."renamed_policy_type"'`) || !strings.Contains(line, `'"public"."policy_integer"'`) {
					s.T.Fatal("resolved type identity missing from policy export", line)
				}
			}
			if !foundPolicy {
				s.T.Fatal("policy omitted from export", dump)
			}
			for round := range 3 {
				if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(dump)); err != nil {
					s.T.Fatal(err)
				}
				if again := policyDump(s); again != dump {
					s.T.Fatalf("unstable typed expression replay round %d\nbefore=%s\nafter=%s", round, dump, again)
				}
				assertIdentity()
			}
		}
		s.Exec("DROP POLICY p ON t")
		s.Exec("DROP VIEW policy_type_view")
		s.Exec("DROP TABLE renamed_policy_type")
		s.Exec("DROP TABLE policy_type")
		s.Exec("DROP DOMAIN policy_integer")
		policyRows(s)
	})
}
