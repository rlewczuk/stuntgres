package reference_test

import (
	"github.com/lib/pq"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleMembershipGrammarDiagnostics(t *testing.T) {
	membershipCases(t, []string{"md_role", "md_member", "PUBLIC", "md_é\"slash\\"}, func(s *membershipScenario) {
		for _, q := range []string{"GRANT public TO md_member", "GRANT md_role TO public", "GRANT md_role TO md_member GRANTED BY public", "GRANT md_role TO \"public\"", "CREATE ROLE md_new IN ROLE public", "CREATE ROLE md_new ROLE public", "CREATE ROLE md_new ADMIN public"} {
			s.fail(q, "42704", `role "public" does not exist`, "")
		}
		s.exec(`GRANT "PUBLIC" TO md_member`)
		decoded := "md_é\"slash\\"
		s.exec("GRANT " + pq.QuoteIdentifier(decoded) + " TO md_member")
		s.fail("GRANT md_role TO md_member GRANTED BY "+pq.QuoteIdentifier(decoded), "42501", "permission denied to grant privileges as role \""+decoded+"\"", `The grantor must have the ADMIN option on role "md_role".`)
		for _, q := range []string{"GRANT md_role TO none", "REVOKE md_role FROM \"none\"", "GRANT md_role TO md_member GRANTED BY none", "ALTER GROUP none ADD USER md_member"} {
			step := fixture.RoleReferenceError(q, "42939", `role name "none" is reserved`, "", s.prepared, "prepare")
			position := strings.Index(q, "none") + 1
			if strings.Contains(q, "\"none\"") {
				position--
			}
			step.Diagnostic.Position = strconv.Itoa(position)
			s.run(step)
		}
		q := "GRANT md_role TO md_member WITH mystery TRUE"
		step := fixture.RoleReferenceError(q, "42601", `unrecognized role option "mystery"`, "", s.prepared, "execute")
		step.Diagnostic.Position = strconv.Itoa(strings.Index(q, "mystery") + 1)
		s.run(step)
		for _, tc := range []struct{ q, token string }{
			{"GRANT md_role TO md_member WITH ADMIN 1", "1"},
			{"GRANT md_role TO 'md_member'", "'md_member'"},
			{"GRANT md_role TO $1", "$1"},
			{"GRANT md_role TO public.md_member", "."},
			{"REVOKE ADMIN, INHERIT OPTION FOR md_role FROM md_member", "OPTION"},
			{"ALTER GROUP md_role ADD USER md_member CASCADE", "CASCADE"},
			{"ALTER GROUP md_role DROP USER md_member GRANTED BY md_member", "GRANTED"},
			{"CREATE ROLE md_never; GRANT md_role TO md_member WITH ADMIN TRUE nonsense", "nonsense"},
		} {
			step := fixture.RoleReferenceError(tc.q, "42601", "syntax error at or near \""+tc.token+"\"", "", s.prepared, "prepare")
			step.Diagnostic.Position = strconv.Itoa(strings.Index(tc.q, tc.token) + 1)
			s.run(step)
		}
		s.fail("DROP ROLE md_never", "42704", `role "md_never" does not exist`, "")
		s.fail("GRANT md_missing TO md_absent GRANTED BY md_grantor", "42704", `role "md_grantor" does not exist`, "")
		s.fail("GRANT md_missing TO md_absent", "42704", `role "md_absent" does not exist`, "")
		s.fail("GRANT md_missing TO md_member", "42704", `role "md_missing" does not exist`, "")
	})
}

func TestReferenceRoleMembershipAtomicityPrepare(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"mp_late"})
	membershipCases(t, []string{"mp_owner", "mp_admin", "mp_member", "mp_other"}, func(s *membershipScenario) {
		t := s.t
		s.owned("mp_owner")
		s.fail("GRANT mp_owner TO mp_member,mp_absent", "42704", `role "mp_absent" does not exist`, "")
		s.actor("mp_member")
		s.read(false)
		s.actor("")
		s.fail("GRANT mp_owner,mp_absent TO mp_member", "42704", `role "mp_absent" does not exist`, "")
		s.actor("mp_member")
		s.read(false)
		s.actor("")
		s.exec("GRANT mp_owner TO mp_admin WITH ADMIN TRUE")
		s.actor("mp_admin")
		s.exec("GRANT mp_owner TO mp_other")
		s.actor("")
		s.exec("GRANT mp_owner TO mp_member")
		s.restrict("REVOKE mp_owner FROM mp_member,mp_admin")
		s.actor("mp_member")
		s.read(true)
		s.actor("")
		stmt, err := s.c.PrepareContext(s.p.Ctx, "GRANT mp_late TO mp_member")
		if err != nil {
			t.Fatal("utility lookup during Prepare", err)
		}
		defer stmt.Close()
		s.exec("CREATE ROLE mp_late")
		result, err := stmt.ExecContext(s.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		count, err := result.RowsAffected()
		if err != nil || count != 0 {
			t.Fatal(count, err)
		}
		s.exec("DROP ROLE mp_late")
		s.actor("mp_admin")
		held, err := s.c.PrepareContext(s.p.Ctx, "GRANT mp_owner TO mp_member")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		s.actor("")
		s.exec("REVOKE ADMIN OPTION FOR mp_owner FROM mp_admin CASCADE")
		s.actor("mp_admin")
		_, err = held.ExecContext(s.p.Ctx)
		message, detail := membershipDenied("mp_owner", "grant")
		want := fixture.ControlsReferenceDiagnostic("42501", message)
		want.Detail = detail
		fixture.AssertDiagnostic(t, err, &want)
		s.actor("")
		symbolic, err := s.c.PrepareContext(s.p.Ctx, "GRANT mp_owner TO CURRENT_USER")
		if err != nil {
			t.Fatal(err)
		}
		defer symbolic.Close()
		s.exec("SET ROLE mp_member")
		_, err = symbolic.ExecContext(s.p.Ctx)
		message, detail = membershipDenied("mp_owner", "grant")
		want = fixture.ControlsReferenceDiagnostic("42501", message)
		want.Detail = detail
		fixture.AssertDiagnostic(t, err, &want)
		s.exec("RESET ROLE")
	})
}
