package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGPoliciesFeatureSmoke(t *testing.T) {
	pgPoliciesCases(t, []string{"policies_smoke_a", "policies_smoke_z", "policies_smoke_reader"}, func(s *fixture.RoleMembershipScenario) {
		s.Exec("CREATE TABLE t(id INT,allowed BOOL)")
		held := pgPoliciesHeld(s, pgPoliciesQuery)
		held(pgPoliciesSelect(pgPoliciesQuery, [][]any{}))
		s.Exec("CREATE POLICY p ON t AS RESTRICTIVE FOR UPDATE TO policies_smoke_z,policies_smoke_a,policies_smoke_a USING(id>0)")
		row := pgPoliciesRow("t", "p", "RESTRICTIVE", "{policies_smoke_a,policies_smoke_z}", "UPDATE", "(id > 0)", nil)
		pgPoliciesRead(s, row)
		held(pgPoliciesSelect(pgPoliciesQuery, [][]any{row}))
		s.Exec("ALTER POLICY p ON t WITH CHECK(allowed)")
		row = pgPoliciesRow("t", "p", "RESTRICTIVE", "{policies_smoke_a,policies_smoke_z}", "UPDATE", "(id > 0)", "allowed")
		pgPoliciesRead(s, row)
		held(pgPoliciesSelect(pgPoliciesQuery, [][]any{row}))
		s.Exec("ALTER POLICY p ON t RENAME TO renamed")
		row = pgPoliciesRow("t", "renamed", "RESTRICTIVE", "{policies_smoke_a,policies_smoke_z}", "UPDATE", "(id > 0)", "allowed")
		s.Actor("policies_smoke_reader")
		pgPoliciesRead(s, row)
		readerHeld := pgPoliciesHeld(s, pgPoliciesQuery)
		readerHeld(pgPoliciesSelect(pgPoliciesQuery, [][]any{row}))
		pgPoliciesViewACL(s, func() {
			s.Actor("")
			s.Exec("REVOKE SELECT ON pg_catalog.pg_policies FROM PUBLIC")
			s.Actor("policies_smoke_reader")
			readerHeld(pgPoliciesDenied(s, pgPoliciesQuery, false))
			fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, pgPoliciesDenied(s, "SELECT * FROM pg_catalog.pg_policies WHERE FALSE", s.Prepared))
			s.Actor("")
			s.Exec("GRANT SELECT ON pg_catalog.pg_policies TO PUBLIC")
			s.Actor("policies_smoke_reader")
			readerHeld(pgPoliciesSelect(pgPoliciesQuery, [][]any{row}))
			s.Actor("")
		})
		pgPoliciesRoundtrip(s, row)
		held(pgPoliciesSelect(pgPoliciesQuery, [][]any{row}))
		s.Exec("DROP POLICY renamed ON t")
		pgPoliciesRead(s)
		held(pgPoliciesSelect(pgPoliciesQuery, [][]any{}))
	})
}
