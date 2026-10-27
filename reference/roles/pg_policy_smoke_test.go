package reference_test

import (
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGPolicyFeatureSmoke(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"pgpolicy_smoke"}, func(s *fixture.RoleMembershipScenario) {
		policySetup(s)
		s.Exec("CREATE POLICY p ON t AS RESTRICTIVE FOR UPDATE TO pgpolicy_smoke,pgpolicy_smoke USING(id>0)")
		s.Exec("CREATE POLICY p ON u")
		oid := policyOID(s, "p")
		pgPolicyFacts(s, "t", "p", "w", false, "pgpolicy_smoke,pgpolicy_smoke", "(id > 0)", "")
		var relation int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT 't'::regclass::oid").Scan(&relation); err != nil {
			s.T.Fatal(err)
		}
		peer, err := s.Backend.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer peer.Close()
		private := false
		observePeer := func(table, name, command string, permissive bool, roles, using, check string) {
			step := pgPolicyRow(table, name, command, permissive, roles, using, check)
			step.Prepared = s.Prepared
			if private {
				step = pgPolicyKnownRow(step, table, name, oid, relation)
			}
			fixture.RunStep(s.T, s.P.Ctx, peer, s.Record, step)
		}
		observePeer("t", "p", "w", false, "pgpolicy_smoke,pgpolicy_smoke", "(id > 0)", "")
		s.Exec("BEGIN")
		private = true
		defer s.C.ExecContext(s.P.Ctx, "ROLLBACK")
		s.Exec("ALTER POLICY p ON t USING(id>1)")
		s.Exec("ALTER POLICY p ON t RENAME TO renamed")
		policySameOID(s, "renamed", oid)
		pgPolicyFacts(s, "t", "renamed", "w", false, "pgpolicy_smoke,pgpolicy_smoke", "(id > 1)", "")
		observePeer("t", "p", "w", false, "pgpolicy_smoke,pgpolicy_smoke", "(id > 0)", "")
		s.Exec("SAVEPOINT before_check")
		s.Exec("ALTER POLICY renamed ON t WITH CHECK(FALSE)")
		pgPolicyFacts(s, "t", "renamed", "w", false, "pgpolicy_smoke,pgpolicy_smoke", "(id > 1)", "false")
		s.Exec("ROLLBACK TO before_check")
		pgPolicyFacts(s, "t", "renamed", "w", false, "pgpolicy_smoke,pgpolicy_smoke", "(id > 1)", "")
		s.Exec("COMMIT")
		private = false
		observePeer("t", "renamed", "w", false, "pgpolicy_smoke,pgpolicy_smoke", "(id > 1)", "")
		policyError(s, "ALTER POLICY renamed ON t TO PUBLIC USING(TRUE) WITH CHECK(1)", "42804", "argument of POLICY must be type boolean, not type integer", "", 0)
		pgPolicyFacts(s, "t", "renamed", "w", false, "pgpolicy_smoke,pgpolicy_smoke", "(id > 1)", "")
		policySameOID(s, "renamed", oid)
		if !s.Backend.PG {
			// Genuine native control phase, after all shared discovery assertions.
			dump := policyDump(s)
			retained, err := s.C.QueryContext(s.P.Ctx, "SELECT polname,pg_get_expr(polqual,polrelid) FROM pg_policy WHERE polrelid='t'::regclass ORDER BY polname")
			if err != nil {
				s.T.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
			s.Run(fixture.Select("SELECT count(*) FROM pg_policy", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			var name, expr string
			if !retained.Next() {
				s.T.Fatal("missing retained metadata", retained.Err())
			}
			if err := retained.Scan(&name, &expr); err != nil || name != "renamed" || expr != "(id > 1)" {
				s.T.Fatal(name, expr, err)
			}
			if retained.Next() || retained.Err() != nil {
				s.T.Fatal("retained metadata tail", retained.Err())
			}
			retained.Close()
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(dump)); err != nil {
				s.T.Fatal(err)
			}
			pgPolicyFacts(s, "t", "renamed", "w", false, "pgpolicy_smoke,pgpolicy_smoke", "(id > 1)", "")
			if after := policyDump(s); after != dump {
				s.T.Fatalf("metadata control fidelity changed\nbefore=%s\nafter=%s", dump, after)
			}
		}
		s.Exec("DROP POLICY renamed ON t")
		s.Run(pgPolicyAbsent("t", "renamed"))
	})
}
