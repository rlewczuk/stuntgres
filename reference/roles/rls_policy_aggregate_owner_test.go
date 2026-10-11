package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_0 parse_agg.c selects the lowest semantic argument level before
// checking that owner's expression kind. SELECT syntax alone does not make an
// aggregate local: t.id belongs to the policy frame, helper.id to the child.
// These are source-grounded expectations for pinned-first oracle discovery,
// not observations from native execution.
func TestReferenceRLSPolicyAggregateOwner(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{"outer", "(SELECT sum(t.id))>0"},
		{"outer_with_local_source", "(SELECT sum(t.id) FROM helper)>0"},
		{"deep_outer", "(SELECT (SELECT sum(t.id)))>0"},
		{"deep_outer_with_local_source", "(SELECT (SELECT sum(t.id)) FROM helper)>0"},
		{"dead_and", "FALSE AND (SELECT sum(t.id))>0"},
		{"dead_case", "CASE WHEN TRUE THEN TRUE ELSE (SELECT (SELECT sum(t.id)))>0 END"},
	}
	for _, tc := range cases {
		for _, clause := range []string{"USING", "WITH CHECK"} {
			t.Run(tc.name+"/"+clause, func(t *testing.T) {
				membershipCases(t, []string{"policy_aggregate_target"}, func(s *membershipScenario) {
					policyAggregateOwnerSetup(s)
					policyAggregateOwnerFailures(s, clause, tc.expr, "42803", "aggregate functions are not allowed in policy expressions", "")
				})
			})
		}
	}
}

// A blanket subquery/outer-column ban would wrongly reject all but the first
// two cases. Mixed local and outer arguments select the child owner, and a
// deeper aggregate may belong to an intermediate SELECT rather than policy.
func TestReferenceRLSPolicyAggregateOwnerLocal(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{"child_column", "(SELECT sum(helper.id) FROM helper)>0"},
		{"child_constant", "(SELECT sum(1))>0"},
		{"mixed_child_and_outer", "(SELECT sum(helper.id+t.id) FROM helper)>0"},
		{"deep_child", "(SELECT (SELECT sum(helper.id) FROM helper))>0"},
		{"promoted_to_child", "(SELECT (SELECT sum(helper.id)) FROM helper)>0"},
	}
	for _, tc := range cases {
		for _, clause := range []string{"USING", "WITH CHECK"} {
			t.Run(tc.name+"/"+clause, func(t *testing.T) {
				membershipCases(t, nil, func(s *membershipScenario) {
					policyAggregateOwnerSetup(s)
					s.exec("CREATE POLICY p ON t " + clause + "(" + tc.expr + ")")
					s.exec("ALTER POLICY p ON t " + clause + "(" + tc.expr + ")")
					policyRows(s)
					if tc.name != "child_constant" {
						policyRestrict(s, "DROP TABLE helper RESTRICT", "cannot drop table helper because other objects depend on it", "policy p on table t depends on table helper")
					}
					s.exec("DROP POLICY p ON t")
					s.exec("DROP TABLE helper RESTRICT")
					policyRows(s)
				})
			})
		}
	}
}

// parse_func.c resolves signatures before transformAggregateCall, and
// parse_agg.c checks same-level aggregate nesting before owner-context bans.
// In contrast, a nested policy-owned inner aggregate fails its own context
// check before the enclosing aggregate can be analyzed.
func TestReferenceRLSPolicyAggregateOwnerPrecedence(t *testing.T) {
	cases := []struct {
		name    string
		expr    string
		code    string
		message string
		hint    string
	}{
		{"direct_signature", "sum(t.allowed)>0", "42883", "function sum(boolean) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
		{"promoted_signature", "(SELECT sum(t.allowed))>0", "42883", "function sum(boolean) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
		{"child_nesting", "(SELECT sum(sum(helper.id)) FROM helper)>0", "42803", "aggregate function calls cannot be nested", ""},
		{"child_nesting_with_outer", "(SELECT sum(sum(helper.id)+t.id) FROM helper)>0", "42803", "aggregate function calls cannot be nested", ""},
		{"inner_policy_context", "(SELECT sum(sum(t.id)))>0", "42803", "aggregate functions are not allowed in policy expressions", ""},
	}
	for _, tc := range cases {
		for _, clause := range []string{"USING", "WITH CHECK"} {
			t.Run(tc.name+"/"+clause, func(t *testing.T) {
				membershipCases(t, []string{"policy_aggregate_target"}, func(s *membershipScenario) {
					policyAggregateOwnerSetup(s)
					policyAggregateOwnerFailures(s, clause, tc.expr, tc.code, tc.message, tc.hint)
				})
			})
		}
	}
}

func policyAggregateOwnerSetup(s *membershipScenario) {
	s.t.Helper()
	policySetup(s)
	s.exec("CREATE TABLE helper(id INTEGER)")
	s.run(fixture.Exec("INSERT INTO helper VALUES(3),(4)", 2))
}

func policyAggregateOwnerFailures(s *membershipScenario, clause, expr, code, message, hint string) {
	s.t.Helper()
	using, check := expr, "TRUE"
	if clause == "WITH CHECK" {
		using, check = "TRUE", expr
	}
	fail := func(q string) {
		step := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Hint: hint})
		// Utility Prepare describes no policy expression. The semantic error
		// must occur at Execute for direct and prepared callers alike; all
		// other exposed diagnostic fields, including Position, are empty.
		step.ErrorStage = "execute"
		s.run(step)
	}
	fail("CREATE POLICY rejected ON t TO policy_aggregate_target USING(" + using + ") WITH CHECK(" + check + ")")
	// Failed CREATE publishes neither its name nor its staged role target.
	s.exec("CREATE POLICY rejected ON t USING(TRUE) WITH CHECK(TRUE)")
	s.exec("DROP POLICY rejected ON t")
	s.exec("DROP ROLE policy_aggregate_target")
	s.exec("CREATE ROLE policy_aggregate_target LOGIN")

	s.exec("CREATE POLICY p ON t AS RESTRICTIVE FOR UPDATE TO policy_aggregate_target USING(id>0) WITH CHECK(allowed)")
	oid := policyOID(s, "p")
	before := ""
	if !s.backend.PG {
		before = policyDump(s)
	}
	fail("ALTER POLICY p ON t TO PUBLIC USING(" + using + ") WITH CHECK(" + check + ")")
	policySameOID(s, "p", oid)
	policyFacts(s, "p", "RESTRICTIVE", "UPDATE", "policy_aggregate_target", "(id > 0)", "allowed")
	if !s.backend.PG {
		if after := policyDump(s); after != before {
			s.t.Fatalf("failed aggregate ALTER changed export\nbefore=%s\nafter=%s", before, after)
		}
	}
	policyRoleBlocked(s, "policy_aggregate_target", "p")
	policyColumnBlocked(s, "id", "p")
	policyColumnBlocked(s, "allowed", "p")
	// Even when the rejected condition mentioned helper, no new provider
	// dependency may survive failed CREATE or ALTER.
	s.exec("DROP TABLE helper RESTRICT")
	policyRows(s)
	s.exec("DROP POLICY p ON t")
	s.exec("DROP ROLE policy_aggregate_target")
	policyRows(s)
}
