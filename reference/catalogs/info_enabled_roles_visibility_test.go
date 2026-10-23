package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoEnabledRolesVisibility(t *testing.T) {
	enabledRolesVisibility(t)
}

// The feature smoke is the complete public identity/graph/freshness scenario,
// not a second copy or a wrapper around an unrelated baseline capability.
func TestReferenceInfoEnabledRolesFeatureSmoke(t *testing.T) {
	enabledRolesVisibility(t)
}

func enabledRolesVisibility(t *testing.T) {
	roles := []string{"er_v_actor", "er_v_admin", "er_v_wheel", "er_v_island", "er_v_unrelated", "er_v_adminonly"}
	enabledRolesScenario(t, roles, func(s *enabledRolesCase) {
		t := s.t
		for _, role := range roles {
			s.exec("CREATE ROLE " + role)
		}
		for _, q := range []string{
			"GRANT er_v_admin TO er_v_actor WITH INHERIT TRUE, SET TRUE",
			"GRANT er_v_wheel TO er_v_admin WITH INHERIT FALSE, SET TRUE",
			"GRANT er_v_island TO er_v_actor WITH INHERIT TRUE, SET FALSE",
			"GRANT er_v_adminonly TO er_v_actor WITH ADMIN TRUE, INHERIT FALSE, SET FALSE",
		} {
			s.exec(q)
		}
		admin, err := s.b.DB.Conn(s.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminRun := func(q string) { fixture.RunStep(t, s.p.Ctx, admin, s.b.Recorder, fixture.Exec(q, 0)) }
		s.exec("SET SESSION AUTHORIZATION er_v_actor")
		held := s.held(enabledRolesQuery)
		check := func(current string, names ...string) {
			s.read(names...)
			held(s.projection(enabledRolesQuery, names...))
			s.run(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"er_v_actor", current}}))
		}
		check("er_v_actor", "er_v_actor", "er_v_admin", "er_v_island")
		s.exec("SET ROLE er_v_admin")
		check("er_v_admin", "er_v_admin")
		// SET authority still starts from the original session identity.
		s.exec("SET ROLE er_v_wheel")
		check("er_v_wheel", "er_v_wheel")
		s.exec("RESET ROLE")
		check("er_v_actor", "er_v_actor", "er_v_admin", "er_v_island")
		d := fixture.ControlsReferenceDiagnostic("42501", `permission denied to set role "er_v_island"`)
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic("SET ROLE er_v_island", d)
			step.Prepared, step.ErrorStage = prepared, "execute"
			s.run(step)
			check("er_v_actor", "er_v_actor", "er_v_admin", "er_v_island")
		}
		adminRun("GRANT er_v_wheel TO er_v_admin WITH INHERIT TRUE")
		check("er_v_actor", "er_v_actor", "er_v_admin", "er_v_island", "er_v_wheel")
		adminRun("REVOKE er_v_wheel FROM er_v_admin")
		check("er_v_actor", "er_v_actor", "er_v_admin", "er_v_island")
		s.run(s.projection(enabledRolesQuery + " WHERE role_name IS NULL"))
		s.missing("missing")
		s.exec("RESET SESSION AUTHORIZATION")
		// Literal scenario inventory, not a set learned from the target view.
		query := enabledRolesQuery + " WHERE role_name IN ('er_v_actor','er_v_admin','er_v_wheel','er_v_island','er_v_unrelated','er_v_adminonly')"
		all := s.projection(query, roles...)
		s.run(all)
		s.run(fixture.Prepared(all))
	})
}
