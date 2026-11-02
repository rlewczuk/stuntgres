package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoApplicableRolesVisibility(t *testing.T) {
	applicableRolesVisibility(t)
}

func TestReferenceInfoApplicableRolesFeatureSmoke(t *testing.T) {
	applicableRolesVisibility(t)
}

func applicableRolesVisibility(t *testing.T) {
	roles := []string{"ar_v_actor", "ar_v_admin", "ar_v_wheel", "ar_v_island", "ar_v_adminonly", "ar_v_hidden", "ar_v_secret", "ar_v_leaf", "ar_v_power"}
	applicableRolesScenario(t, roles, func(s *applicableRolesCase) {
		for _, role := range roles {
			options := ""
			if role == "ar_v_power" {
				options = " SUPERUSER"
			}
			s.exec("CREATE ROLE " + role + options)
		}
		for _, query := range []string{
			"GRANT ar_v_admin TO ar_v_actor WITH INHERIT TRUE, SET TRUE",
			"GRANT ar_v_wheel TO ar_v_admin WITH INHERIT FALSE, SET TRUE",
			"GRANT ar_v_island TO ar_v_actor WITH INHERIT TRUE, SET FALSE",
			"GRANT ar_v_adminonly TO ar_v_actor WITH ADMIN TRUE, INHERIT FALSE, SET FALSE",
			"GRANT ar_v_secret TO ar_v_hidden",
		} {
			s.exec(query)
		}
		admin, err := s.b.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer admin.Close()
		adminRun := func(query string) { fixture.RunStep(s.t, s.p.Ctx, admin, s.b.Recorder, fixture.Exec(query, 0)) }
		s.exec("SET SESSION AUTHORIZATION ar_v_actor")
		held := s.held(applicableRolesQuery)
		actor := [][3]string{
			{"ar_v_actor", "ar_v_admin", "NO"},
			{"ar_v_admin", "ar_v_wheel", "NO"},
			{"ar_v_actor", "ar_v_island", "NO"},
			{"ar_v_actor", "ar_v_adminonly", "YES"},
		}
		check := func(current string, triples ...[3]string) {
			s.read(triples...)
			held(s.projection(applicableRolesQuery, triples...))
			s.run(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"ar_v_actor", current}}))
		}
		check("ar_v_actor", actor...)
		// enabled_roles is independently a closure inventory; applicable_roles is not.
		s.enabledRolesCase.read("ar_v_actor", "ar_v_admin", "ar_v_island")
		s.exec("SET ROLE ar_v_admin")
		check("ar_v_admin", [3]string{"ar_v_admin", "ar_v_wheel", "NO"})
		s.exec("SET ROLE ar_v_wheel")
		check("ar_v_wheel")
		s.exec("RESET ROLE")
		check("ar_v_actor", actor...)
		d := fixture.ControlsReferenceDiagnostic("42501", `permission denied to set role "ar_v_island"`)
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic("SET ROLE ar_v_island", d)
			step.Prepared, step.ErrorStage = prepared, "execute"
			s.run(step)
			check("ar_v_actor", actor...)
		}
		// A visible grantee exposes even a completely blocked outgoing edge.
		adminRun("GRANT ar_v_leaf TO ar_v_wheel WITH INHERIT FALSE, SET FALSE")
		check("ar_v_actor", actor...)
		adminRun("GRANT ar_v_wheel TO ar_v_admin WITH INHERIT TRUE")
		check("ar_v_actor",
			[3]string{"ar_v_actor", "ar_v_admin", "NO"},
			[3]string{"ar_v_admin", "ar_v_wheel", "NO"},
			[3]string{"ar_v_actor", "ar_v_island", "NO"},
			[3]string{"ar_v_actor", "ar_v_adminonly", "YES"},
			[3]string{"ar_v_wheel", "ar_v_leaf", "NO"},
		)
		adminRun("GRANT ar_v_wheel TO ar_v_admin WITH INHERIT FALSE")
		check("ar_v_actor", actor...)
		adminRun("REVOKE ar_v_leaf FROM ar_v_wheel")
		// SUPERUSER is an attribute of current_user, not an inherited privilege.
		adminRun("GRANT ar_v_power TO ar_v_actor WITH INHERIT TRUE, SET FALSE")
		check("ar_v_actor",
			[3]string{"ar_v_actor", "ar_v_admin", "NO"},
			[3]string{"ar_v_admin", "ar_v_wheel", "NO"},
			[3]string{"ar_v_actor", "ar_v_island", "NO"},
			[3]string{"ar_v_actor", "ar_v_adminonly", "YES"},
			[3]string{"ar_v_actor", "ar_v_power", "NO"},
		)
		adminRun("REVOKE ar_v_power FROM ar_v_actor")
		check("ar_v_actor", actor...)
		s.readQuery(applicableRolesQuery + " WHERE is_grantable IS NULL")
		s.missing("missing")
		s.exec("RESET SESSION AUTHORIZATION")
		query := applicableRolesQuery + " WHERE grantee IN ('ar_v_actor','ar_v_admin','ar_v_wheel','ar_v_island','ar_v_adminonly','ar_v_hidden','ar_v_secret','ar_v_leaf','ar_v_power')"
		s.readQuery(query,
			[3]string{"ar_v_actor", "ar_v_admin", "NO"},
			[3]string{"ar_v_admin", "ar_v_wheel", "NO"},
			[3]string{"ar_v_actor", "ar_v_island", "NO"},
			[3]string{"ar_v_actor", "ar_v_adminonly", "YES"},
			[3]string{"ar_v_hidden", "ar_v_secret", "NO"},
		)
	})
}
