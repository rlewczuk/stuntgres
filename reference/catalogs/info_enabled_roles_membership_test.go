package reference_test

import (
	"context"
	"testing"
	"time"

	"github.com/lib/pq"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoEnabledRolesMembershipOptions(t *testing.T) {
	t.Run("alternate_paths", func(t *testing.T) {
		roles := []string{"er_m_actor", "er_m_admin", "er_m_wheel", "er_m_island", "er_m_bridge", "er_m_adminonly"}
		enabledRolesScenario(t, roles, func(s *enabledRolesCase) {
			for _, role := range roles {
				s.exec("CREATE ROLE " + role)
			}
			for _, q := range []string{
				"GRANT er_m_admin TO er_m_actor WITH INHERIT TRUE, SET TRUE",
				"GRANT er_m_wheel TO er_m_admin WITH INHERIT FALSE, SET TRUE",
				"GRANT er_m_island TO er_m_actor WITH INHERIT TRUE, SET FALSE",
				"GRANT er_m_adminonly TO er_m_actor WITH ADMIN TRUE, INHERIT FALSE, SET FALSE",
			} {
				s.exec(q)
			}
			actor := func(names ...string) {
				s.exec("SET SESSION AUTHORIZATION er_m_actor")
				s.read(names...)
				s.exec("RESET SESSION AUTHORIZATION")
			}
			actor("er_m_actor", "er_m_admin", "er_m_island")
			s.exec("GRANT er_m_bridge TO er_m_actor WITH INHERIT TRUE")
			s.exec("GRANT er_m_wheel TO er_m_bridge WITH INHERIT TRUE")
			actor("er_m_actor", "er_m_admin", "er_m_island", "er_m_bridge", "er_m_wheel")
			s.exec("GRANT er_m_wheel TO er_m_admin WITH INHERIT TRUE")
			actor("er_m_actor", "er_m_admin", "er_m_island", "er_m_bridge", "er_m_wheel")
			s.exec("REVOKE er_m_wheel FROM er_m_bridge")
			actor("er_m_actor", "er_m_admin", "er_m_island", "er_m_bridge", "er_m_wheel")
			s.exec("GRANT er_m_wheel TO er_m_admin WITH INHERIT FALSE")
			actor("er_m_actor", "er_m_admin", "er_m_island", "er_m_bridge")
			s.exec("REVOKE er_m_wheel FROM er_m_admin")
			actor("er_m_actor", "er_m_admin", "er_m_island", "er_m_bridge")
		})
	})
	t.Run("edge_defaults", func(t *testing.T) {
		enabledRolesScenario(t, []string{"er_d_actor", "er_d_old", "er_d_new"}, func(s *enabledRolesCase) {
			for _, role := range []string{"er_d_actor", "er_d_old", "er_d_new"} {
				s.exec("CREATE ROLE " + role)
			}
			s.exec("GRANT er_d_old TO er_d_actor")
			s.exec("ALTER ROLE er_d_actor NOINHERIT")
			s.exec("GRANT er_d_new TO er_d_actor")
			actor := func(names ...string) {
				s.exec("SET SESSION AUTHORIZATION er_d_actor")
				s.read(names...)
				s.exec("RESET SESSION AUTHORIZATION")
			}
			actor("er_d_actor", "er_d_old")
			s.exec("GRANT er_d_new TO er_d_actor WITH INHERIT TRUE")
			actor("er_d_actor", "er_d_old", "er_d_new")
			s.exec("GRANT er_d_new TO er_d_actor WITH ADMIN TRUE, SET FALSE")
			actor("er_d_actor", "er_d_old", "er_d_new")
			s.exec("GRANT er_d_new TO er_d_actor WITH INHERIT FALSE")
			actor("er_d_actor", "er_d_old")
			s.exec("GRANT er_d_new TO er_d_actor WITH ADMIN FALSE, SET TRUE")
			actor("er_d_actor", "er_d_old")
		})
	})
	t.Run("superuser_and_spelling", func(t *testing.T) {
		roles := []string{"er_s_actor", "er_s_power", "er_s_unrelated", "Er_Mix_雪"}
		enabledRolesScenario(t, roles, func(s *enabledRolesCase) {
			s.exec("CREATE ROLE er_s_actor")
			s.exec("CREATE ROLE er_s_power SUPERUSER")
			s.exec("CREATE ROLE er_s_unrelated NOLOGIN NOINHERIT")
			s.exec(`CREATE ROLE "Er_Mix_雪"`)
			query := enabledRolesQuery + ` WHERE role_name IN ('er_s_actor','er_s_power','er_s_unrelated','Er_Mix_雪')`
			s.run(s.projection(query, roles...))
			s.run(fixture.Prepared(s.projection(query, roles...)))
			s.exec(`GRANT "Er_Mix_雪" TO er_s_actor WITH INHERIT TRUE, SET FALSE`)
			s.exec("GRANT er_s_power TO er_s_actor WITH INHERIT TRUE, SET FALSE")
			s.exec("SET SESSION AUTHORIZATION er_s_actor")
			// Inheriting a superuser role's privileges does not inherit its attribute.
			s.read("er_s_actor", "er_s_power", "Er_Mix_雪")
		})
	})
	t.Run("implicit_database_owner", func(t *testing.T) {
		enabledRolesScenario(t, []string{"er_o_owner", "er_o_plain", "er_o_heir"}, func(s *enabledRolesCase) {
			t := s.t
			var database, bootstrap string
			if err := s.c.QueryRowContext(s.p.Ctx, "SELECT current_database(),session_user").Scan(&database, &bootstrap); err != nil {
				t.Fatal(err)
			}
			// The scenario fixture owns the database as the bootstrap identity.
			// Restore before returning this connection or removing ordinary roles.
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO " + pq.QuoteIdentifier(bootstrap)} {
					if _, err := s.c.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			for _, role := range []string{"er_o_owner", "er_o_plain", "er_o_heir"} {
				s.exec("CREATE ROLE " + role)
			}
			s.exec("ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO er_o_owner")
			s.exec("GRANT er_o_owner TO er_o_heir WITH INHERIT TRUE")
			s.exec("SET SESSION AUTHORIZATION er_o_owner")
			s.read("er_o_owner", "pg_database_owner")
			s.exec("RESET SESSION AUTHORIZATION")
			s.exec("SET SESSION AUTHORIZATION er_o_heir")
			s.read("er_o_heir", "er_o_owner", "pg_database_owner")
			s.exec("RESET SESSION AUTHORIZATION")
			s.exec("SET SESSION AUTHORIZATION er_o_plain")
			s.read("er_o_plain")
		})
	})
}
