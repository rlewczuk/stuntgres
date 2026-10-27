package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGPolicyAuthorization(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"pgpolicy_reader", "pgpolicy_stranger"}, func(s *fixture.RoleMembershipScenario) {
		policySetup(s)
		s.Exec("CREATE TABLE helper(id INT)")
		s.Run(fixture.Exec("INSERT INTO helper VALUES(1)", 1))
		s.Exec("CREATE SEQUENCE pgpolicy_sequence START 41")
		s.Exec("CREATE POLICY p ON t TO pgpolicy_reader USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id)) WITH CHECK(nextval('pgpolicy_sequence')>0)")
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Exec("GRANT SELECT ON t TO pgpolicy_reader")
		var relation int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT 't'::regclass::oid").Scan(&relation); err != nil {
			s.T.Fatal(err)
		}
		q := fmt.Sprintf("SELECT polname,polcmd,polpermissive,pg_get_expr(polqual,polrelid) AS qual,pg_get_expr(polwithcheck,polrelid) AS check_expr FROM pg_policy WHERE polrelid=%d::oid ORDER BY polname", relation)
		// Native public starts with PUBLIC USAGE; the oracle fixture rebuilds
		// public without it. Establish the same denial precondition explicitly.
		s.Exec("REVOKE USAGE ON SCHEMA public FROM PUBLIC")
		// Neither ordinary actor has schema USAGE. Known numeric metadata and
		// deparse are not target lookup, execution or provider authorization.
		for _, actor := range []string{"", "pgpolicy_reader", "pgpolicy_stranger"} {
			s.Actor(actor)
			provider, sequence := "helper", "pgpolicy_sequence"
			if actor != "" {
				provider, sequence = "public.helper", "public.pgpolicy_sequence"
			}
			qual := "(EXISTS ( SELECT 1\n   FROM " + provider + "\n  WHERE (helper.id = t.id)))"
			check := "(nextval('" + sequence + "'::regclass) > 0)"
			want := fixture.Select(q, []string{"polname", "polcmd", "polpermissive", "qual", "check_expr"}, []string{"NAME", "CHAR", "BOOL", "TEXT", "TEXT"}, [][]any{{"p", "*", true, qual, check}})
			s.Run(want)
			s.Run(fixture.Select(fmt.Sprintf("SELECT polname FROM pg_policy WHERE polrelid=%d::oid AND polname='absent'", relation), []string{"polname"}, []string{"NAME"}, [][]any{}))
		}
		s.Actor("pgpolicy_stranger")
		deniedSchema := fixture.RoleReferenceError("SELECT id FROM public.t", "42501", "permission denied for schema public", "", s.Prepared, "execute")
		deniedSchema.Diagnostic.Position = fixture.Position(16)
		if s.Prepared {
			deniedSchema.ErrorStage = "prepare"
		}
		s.Run(deniedSchema)
		s.Actor("")
		s.Exec("GRANT USAGE ON SCHEMA public TO pgpolicy_reader,pgpolicy_stranger")
		s.Actor("pgpolicy_stranger")
		s.Run(fixture.RoleReferenceError("SELECT id FROM t", "42501", "permission denied for table t", "", s.Prepared, "execute"))
		s.Actor("pgpolicy_reader")
		s.Run(fixture.RoleReferenceError("SELECT id FROM t", "42501", "permission denied for table helper", "", s.Prepared, "execute"))
		s.Actor("")
		s.Run(fixture.Select("SELECT nextval('pgpolicy_sequence') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(41)}}))
		s.Run(fixture.Select("SELECT id FROM helper", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		s.Exec("GRANT SELECT ON helper TO pgpolicy_reader")
		s.Actor("pgpolicy_reader")
		s.Run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		s.Actor("")
	})
}

func TestReferencePGPolicyDiagnostics(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"pgpolicy_target"}, func(s *fixture.RoleMembershipScenario) {
		policySetup(s)
		s.Exec("CREATE POLICY p ON t AS RESTRICTIVE FOR UPDATE TO pgpolicy_target USING(id>0)")
		oid := policyOID(s, "p")
		policyError(s, "ALTER POLICY p ON t TO PUBLIC USING(TRUE) WITH CHECK(1)", "42804", "argument of POLICY must be type boolean, not type integer", "", 0)
		policySameOID(s, "p", oid)
		pgPolicyFacts(s, "t", "p", "w", false, "pgpolicy_target", "(id > 0)", "")
		s.Run(fixture.Select("SELECT pg_get_expr(polqual,4294967295::oid) AS missing FROM pg_policy WHERE polrelid='t'::regclass AND polname='p'", []string{"missing"}, []string{"TEXT"}, [][]any{{nil}}))
		policyError(s, "SELECT pg_get_expr(polqual,0::oid) FROM pg_policy WHERE polrelid='t'::regclass AND polname='p'", "22023", "expression contains variables", "", 0)
		pgPolicyFacts(s, "t", "p", "w", false, "pgpolicy_target", "(id > 0)", "")
		s.Exec("CREATE POLICY constant ON t USING(TRUE)")
		s.Run(fixture.Select("SELECT pg_get_expr(polqual,0::oid) AS implicit,pg_get_expr(polqual,0::oid,false) AS normal,pg_get_expr(polqual,0::oid,true) AS pretty FROM pg_policy WHERE polrelid='t'::regclass AND polname='constant'", []string{"implicit", "normal", "pretty"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"true", "true", "true"}}))
	})
}
