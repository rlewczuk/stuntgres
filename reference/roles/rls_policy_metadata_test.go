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
func policyOID(s *membershipScenario, name string) int64 {
	s.t.Helper()
	if !s.backend.PG {
		return 0
	}
	var oid int64
	if err := s.c.QueryRowContext(s.p.Ctx, "SELECT oid FROM pg_policy WHERE polrelid='t'::regclass AND polname=$1", name).Scan(&oid); err != nil {
		s.t.Fatal(err)
	}
	if oid <= 0 || oid > 1<<32-1 {
		s.t.Fatal("invalid policy identity", oid)
	}
	return oid
}

func policySameOID(s *membershipScenario, name string, want int64) {
	s.t.Helper()
	if got := policyOID(s, name); got != want {
		s.t.Fatalf("policy identity changed: %d -> %d", want, got)
	}
}

func policyOrderedRoles(s *membershipScenario, name, roles string) {
	s.t.Helper()
	if !s.backend.PG {
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
	s.run(fixture.Select("SELECT polroles=ARRAY["+refs+"]::oid[] AS matches FROM pg_policy WHERE polrelid='t'::regclass AND polname="+pq.QuoteLiteral(name), []string{"matches"}, []string{"BOOL"}, [][]any{{true}}))
}

func policyResolvedRoleExport(s *membershipScenario) {
	s.t.Helper()
	if s.backend.PG {
		return
	} // Native Dump/Restore has no PostgreSQL counterpart.
	dump := policyDump(s)
	upper := strings.ToUpper(dump)
	if strings.Contains(upper, "TO CURRENT_USER") || strings.Contains(upper, "TO CURRENT_ROLE") || strings.Contains(upper, "TO SESSION_USER") || !strings.Contains(dump, "policy_session") {
		s.t.Fatal("TO principals remained symbolic instead of resolved", dump)
	}
	if !strings.Contains(upper, "CURRENT_USER") || !strings.Contains(upper, "CURRENT_ROLE") {
		s.t.Fatal("condition identity became a captured literal", dump)
	}
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump)); err != nil {
		s.t.Fatal(err)
	}
	if after := policyDump(s); after != dump {
		s.t.Fatalf("resolved-role roundtrip changed\n%s\n%s", dump, after)
	}
}
