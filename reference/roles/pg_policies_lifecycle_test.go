package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGPoliciesLifecycle(t *testing.T) {
	pgPoliciesCases(t, []string{"policies_old", "policies_new"}, func(s *fixture.RoleMembershipScenario) {
		s.Exec("CREATE TABLE t(id INT,allowed BOOL)")
		held := pgPoliciesHeld(s, pgPoliciesQuery)
		read := func(rows ...[]any) {
			if rows == nil { rows = [][]any{} }
			held(pgPoliciesSelect(pgPoliciesQuery, rows))
			pgPoliciesRead(s, rows...)
		}
		read()
		s.Exec("CREATE POLICY p ON t AS RESTRICTIVE FOR UPDATE TO policies_old USING(id>0) WITH CHECK(allowed)")
		row := pgPoliciesRow("t", "p", "RESTRICTIVE", "{policies_old}", "UPDATE", "(id > 0)", "allowed")
		read(row)
		saved := pgPoliciesSnapshot(s)
		s.Exec("ALTER POLICY p ON t")
		read(row)
		s.Exec("ALTER POLICY p ON t TO policies_new")
		row = pgPoliciesRow("t", "p", "RESTRICTIVE", "{policies_new}", "UPDATE", "(id > 0)", "allowed")
		read(row)
		s.Exec("ALTER POLICY p ON t USING(id>1)")
		row = pgPoliciesRow("t", "p", "RESTRICTIVE", "{policies_new}", "UPDATE", "(id > 1)", "allowed")
		read(row)
		s.Exec("ALTER POLICY p ON t WITH CHECK(id<10)")
		row = pgPoliciesRow("t", "p", "RESTRICTIVE", "{policies_new}", "UPDATE", "(id > 1)", "(id < 10)")
		read(row)
		policyError(s, "ALTER POLICY p ON t TO PUBLIC USING(TRUE) WITH CHECK(1)", "42804", "argument of POLICY must be type boolean, not type integer", "", 0)
		read(row)
		s.Exec("ALTER ROLE policies_new RENAME TO policies_renamed")
		defer s.C.ExecContext(s.P.Ctx, "ALTER ROLE policies_renamed RENAME TO policies_new")
		row = pgPoliciesRow("t", "p", "RESTRICTIVE", "{policies_renamed}", "UPDATE", "(id > 1)", "(id < 10)")
		read(row)
		s.Exec("ALTER POLICY p ON t RENAME TO renamed")
		row = pgPoliciesRow("t", "renamed", "RESTRICTIVE", "{policies_renamed}", "UPDATE", "(id > 1)", "(id < 10)")
		read(row)
		s.Exec("CREATE TABLE other(id INT)")
		s.Exec("CREATE POLICY renamed ON other")
		other := pgPoliciesRow("other", "renamed", "PERMISSIVE", "{public}", "ALL", nil, nil)
		read(other, row)
		s.Exec("ALTER TABLE t RENAME TO renamed_table")
		row = pgPoliciesRow("renamed_table", "renamed", "RESTRICTIVE", "{policies_renamed}", "UPDATE", "(id > 1)", "(id < 10)")
		read(other, row)
		saved()
		s.Exec("DROP POLICY renamed ON renamed_table")
		read(other)
		s.Exec("DROP POLICY renamed ON other")
		read()
	})
	t.Run("schema_rename", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "policies_before")
		fixture.SchemaReferenceCleanup(t, "policies_after")
		pgPoliciesCases(t, nil, func(s *fixture.RoleMembershipScenario) {
			s.Exec("CREATE SCHEMA policies_before")
			defer s.C.ExecContext(s.P.Ctx, "DROP SCHEMA IF EXISTS policies_before CASCADE")
			defer s.C.ExecContext(s.P.Ctx, "DROP SCHEMA IF EXISTS policies_after CASCADE")
			s.Exec("CREATE TABLE policies_before.t(id INT)")
			s.Exec("CREATE POLICY p ON policies_before.t USING(id>0)")
			const q = "SELECT * FROM pg_policies WHERE policyname='p' ORDER BY schemaname,tablename,policyname"
			held := pgPoliciesHeld(s, q)
			held(pgPoliciesSelect(q, [][]any{{"policies_before", "t", "p", "PERMISSIVE", "{public}", "ALL", "(id > 0)", nil}}))
			s.Exec("ALTER SCHEMA policies_before RENAME TO policies_after")
			held(pgPoliciesSelect(q, [][]any{{"policies_after", "t", "p", "PERMISSIVE", "{public}", "ALL", "(id > 0)", nil}}))
		})
	})
}

func pgPoliciesDenied(s *fixture.RoleMembershipScenario, query string, prepared bool) fixture.Step {
	s.T.Helper()
	step := fixture.RoleReferenceError(query, "42501", "permission denied for view pg_policies", "", prepared, "execute")
	step.SelectRows = true
	return step
}

// The administrative pool restores the view even if an assertion exits while
// the pinned actor is denied. This is view admission, not catalog confidentiality.
func pgPoliciesViewACL(s *fixture.RoleMembershipScenario, body func()) {
	s.T.Helper()
	s.T.Cleanup(func() {
		_, err := s.Backend.DB.ExecContext(s.P.Ctx, "GRANT SELECT ON pg_catalog.pg_policies TO PUBLIC")
		if err != nil { s.T.Error(err) }
	})
	body()
}

func TestReferencePGPoliciesAuthorization(t *testing.T) {
	pgPoliciesCases(t, []string{"policies_reader", "policies_stranger"}, func(s *fixture.RoleMembershipScenario) {
		s.Exec("CREATE TABLE t(id INT,allowed BOOL)")
		s.Exec("CREATE TABLE helper(id INT)")
		s.Exec("CREATE POLICY p ON t TO policies_reader USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id))")
		s.Exec("REVOKE USAGE ON SCHEMA public FROM PUBLIC")
		for _, actor := range []string{"", "policies_reader", "policies_stranger"} {
			s.Actor(actor)
			provider := "helper"
			if actor != "" { provider = "public.helper" }
			pgPoliciesRead(s, pgPoliciesRow("t", "p", "PERMISSIVE", "{policies_reader}", "ALL", "(EXISTS ( SELECT 1\n   FROM "+provider+"\n  WHERE (helper.id = t.id)))", nil))
		}
		s.Actor("policies_stranger")
		held := pgPoliciesHeld(s, pgPoliciesQuery)
		want := pgPoliciesRow("t", "p", "PERMISSIVE", "{policies_reader}", "ALL", "(EXISTS ( SELECT 1\n   FROM public.helper\n  WHERE (helper.id = t.id)))", nil)
		held(pgPoliciesSelect(pgPoliciesQuery, [][]any{want}))
		pgPoliciesViewACL(s, func() {
			s.Actor("")
			s.Exec("REVOKE SELECT ON pg_catalog.pg_policies FROM PUBLIC")
			s.Actor("policies_stranger")
			// Direct, fresh Prepare, and a genuinely held statement all reauthorize.
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{pgPoliciesQuery, "SELECT * FROM pg_catalog.pg_policies WHERE FALSE"} {
					fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, pgPoliciesDenied(s, query, prepared))
				}
			}
			held(pgPoliciesDenied(s, pgPoliciesQuery, false))
			s.Actor("")
			s.Exec("GRANT SELECT ON pg_catalog.pg_policies TO PUBLIC")
			s.Actor("policies_stranger")
			held(pgPoliciesSelect(pgPoliciesQuery, [][]any{want}))
			pgPoliciesRead(s, want)
			s.Actor("")
		})
	})
}
