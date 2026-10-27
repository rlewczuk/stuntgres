package reference_test

import (
	"github.com/lib/pq"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
)

// Allocator values are not PostgreSQL goldens. Only validity and continuity are
// asserted; native immutable identity is covered by real descriptor units in
// the implementation round, not by adding a catalog surface to POLICY.
func policyOID(s *fixture.RoleMembershipScenario, name string) int64 {
	s.T.Helper()
	if !s.Backend.PG {
		return 0
	}
	var oid int64
	if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid FROM pg_policy WHERE polrelid='t'::regclass AND polname=$1", name).Scan(&oid); err != nil {
		s.T.Fatal(err)
	}
	if oid <= 0 || oid > 1<<32-1 {
		s.T.Fatal("invalid policy identity", oid)
	}
	return oid
}

func policySameOID(s *fixture.RoleMembershipScenario, name string, want int64) {
	s.T.Helper()
	if got := policyOID(s, name); got != want {
		s.T.Fatalf("policy identity changed: %d -> %d", want, got)
	}
}

func policyOrderedRoles(s *fixture.RoleMembershipScenario, name, roles string) {
	s.T.Helper()
	if !s.Backend.PG {
		return
	}
	// pg_policies presents a sorted role-name set, not stored duplicate order.
	// Compare the actual OID vector against independently named live principals.
	var refs string
	for _, role := range strings.Split(strings.Trim(roles, "{}"), ",") {
		if refs != "" {
			refs += ","
		}
		if role == "public" {
			refs += "0::oid"
		} else {
			refs += "(SELECT oid FROM pg_roles WHERE rolname=" + pq.QuoteLiteral(role) + ")"
		}
	}
	s.Run(fixture.Select("SELECT polroles=ARRAY["+refs+"]::oid[] AS matches FROM pg_policy WHERE polrelid='t'::regclass AND polname="+pq.QuoteLiteral(name), []string{"matches"}, []string{"BOOL"}, [][]any{{true}}))
}

func policyResolvedRoleExport(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	if s.Backend.PG {
		return
	} // Native Dump/Restore has no PostgreSQL counterpart.
	dump := policyDump(s)
	upper := strings.ToUpper(dump)
	if strings.Contains(upper, "TO CURRENT_USER") || strings.Contains(upper, "TO CURRENT_ROLE") || strings.Contains(upper, "TO SESSION_USER") || !strings.Contains(dump, "policy_session") {
		s.T.Fatal("TO principals remained symbolic instead of resolved", dump)
	}
	if !strings.Contains(upper, "CURRENT_USER") || !strings.Contains(upper, "CURRENT_ROLE") {
		s.T.Fatal("condition identity became a captured literal", dump)
	}
	if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(dump)); err != nil {
		s.T.Fatal(err)
	}
	if after := policyDump(s); after != dump {
		s.T.Fatalf("resolved-role roundtrip changed\n%s\n%s", dump, after)
	}
}
