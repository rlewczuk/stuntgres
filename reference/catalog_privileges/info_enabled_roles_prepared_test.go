package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoEnabledRolesPrepared(t *testing.T) {
	roles := []string{"er_p_actor", "er_p_target", "er_p_created", "er_p_renamed"}
	enabledRolesScenario(t, roles, func(s *enabledRolesCase) {
		t := s.t
		s.exec("CREATE ROLE er_p_actor")
		s.exec("CREATE ROLE er_p_target")
		admin, err := s.b.DB.Conn(s.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminRun := func(q string) { fixture.RunStep(t, s.p.Ctx, admin, s.b.Recorder, fixture.Exec(q, 0)) }
		// A superuser-prepared statement must use its execution-time identity.
		held := s.held(enabledRolesQuery)
		s.exec("SET SESSION AUTHORIZATION er_p_actor")
		held(s.projection(enabledRolesQuery, "er_p_actor"))
		// Conversely an ordinary-prepared statement must refresh after restoration.
		query := enabledRolesQuery + " WHERE role_name IN ('er_p_actor','er_p_target','er_p_created','er_p_renamed')"
		ordinaryHeld := s.held(query)
		ordinaryHeld(s.projection(query, "er_p_actor"))
		adminRun("GRANT er_p_target TO er_p_actor WITH INHERIT TRUE, SET TRUE")
		held(s.projection(enabledRolesQuery, "er_p_actor", "er_p_target"))
		adminRun("GRANT er_p_target TO er_p_actor WITH INHERIT FALSE")
		held(s.projection(enabledRolesQuery, "er_p_actor"))
		s.exec("SET ROLE er_p_target")
		held(s.projection(enabledRolesQuery, "er_p_target"))
		s.exec("RESET ROLE")
		held(s.projection(enabledRolesQuery, "er_p_actor"))
		adminRun("GRANT er_p_target TO er_p_actor WITH INHERIT TRUE")
		held(s.projection(enabledRolesQuery, "er_p_actor", "er_p_target"))
		adminRun("REVOKE er_p_target FROM er_p_actor")
		held(s.projection(enabledRolesQuery, "er_p_actor"))

		// Fast pq cancellation has no stable transport/error outcome. The
		// ordinary context.Canceled boundary belongs to the native adapter.
		if !s.b.PG {
			ctx, cancel := context.WithCancel(s.p.Ctx)
			cancel()
			rows, err := s.c.QueryContext(ctx, enabledRolesQuery)
			if rows != nil {
				_ = rows.Close()
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled query error=%v", err)
			}
		}
		held(s.projection(enabledRolesQuery, "er_p_actor"))
		s.exec("RESET SESSION AUTHORIZATION")
		ordinaryHeld(s.projection(query, "er_p_actor", "er_p_target"))
		adminRun("CREATE ROLE er_p_created NOLOGIN NOINHERIT")
		ordinaryHeld(s.projection(query, "er_p_actor", "er_p_target", "er_p_created"))
		if s.b.PG {
			adminRun("ALTER ROLE er_p_created RENAME TO er_p_renamed")
			ordinaryHeld(s.projection(query, "er_p_actor", "er_p_target", "er_p_renamed"))
			adminRun("DROP ROLE er_p_renamed")
		} else {
			// Preserve the existing prerequisite boundary; this view does not
			// introduce ALTER ROLE RENAME support or suppress its diagnostic.
			d := fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported")
			d.Position = "1"
			fixture.RunStep(t, s.p.Ctx, admin, s.b.Recorder, fixture.ErrorDiagnostic("ALTER ROLE er_p_created RENAME TO er_p_renamed", d))
			ordinaryHeld(s.projection(query, "er_p_actor", "er_p_target", "er_p_created"))
			adminRun("DROP ROLE er_p_created")
		}
		ordinaryHeld(s.projection(query, "er_p_actor", "er_p_target"))
	})
}
