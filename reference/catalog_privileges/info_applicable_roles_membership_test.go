package reference_test

import (
	"context"
	"testing"
	"time"

	"github.com/lib/pq"
)

func TestReferenceInfoApplicableRolesMembershipOptions(t *testing.T) {
	t.Run("alternate_paths_and_options", func(t *testing.T) {
		roles := []string{"ar_m_actor", "ar_m_block", "ar_m_bridge", "ar_m_target", "ar_m_leaf", "ar_m_new"}
		applicableRolesScenario(t, roles, func(s *applicableRolesCase) {
			for _, role := range roles {
				s.exec("CREATE ROLE " + role)
			}
			for _, query := range []string{
				"GRANT ar_m_block TO ar_m_actor WITH INHERIT TRUE, SET TRUE",
				"GRANT ar_m_target TO ar_m_block WITH INHERIT FALSE, SET FALSE",
				"GRANT ar_m_leaf TO ar_m_target WITH INHERIT FALSE, SET FALSE",
			} {
				s.exec(query)
			}
			actor := func(triples ...[3]string) {
				s.exec("SET SESSION AUTHORIZATION ar_m_actor")
				s.read(triples...)
				s.exec("RESET SESSION AUTHORIZATION")
			}
			blocked := [][3]string{{"ar_m_actor", "ar_m_block", "NO"}, {"ar_m_block", "ar_m_target", "NO"}}
			actor(blocked...)
			s.exec("GRANT ar_m_bridge TO ar_m_actor WITH INHERIT TRUE")
			s.exec("GRANT ar_m_target TO ar_m_bridge WITH INHERIT TRUE")
			alternate := [][3]string{
				{"ar_m_actor", "ar_m_block", "NO"}, {"ar_m_block", "ar_m_target", "NO"},
				{"ar_m_actor", "ar_m_bridge", "NO"}, {"ar_m_bridge", "ar_m_target", "NO"},
				{"ar_m_target", "ar_m_leaf", "NO"},
			}
			actor(alternate...)
			s.exec("GRANT ar_m_target TO ar_m_block WITH INHERIT TRUE")
			actor(alternate...) // Diamond does not duplicate the target's outgoing edge.
			s.exec("GRANT ar_m_target TO ar_m_block WITH INHERIT FALSE")
			s.exec("REVOKE ar_m_target FROM ar_m_bridge")
			withoutAlternate := [][3]string{{"ar_m_actor", "ar_m_block", "NO"}, {"ar_m_block", "ar_m_target", "NO"}, {"ar_m_actor", "ar_m_bridge", "NO"}}
			actor(withoutAlternate...)
			s.exec("GRANT ar_m_target TO ar_m_block WITH SET TRUE")
			actor(withoutAlternate...)
			s.exec("GRANT ar_m_target TO ar_m_block WITH SET FALSE")
			actor(withoutAlternate...)
			s.exec("GRANT ar_m_target TO ar_m_block WITH ADMIN TRUE")
			actor([3]string{"ar_m_actor", "ar_m_block", "NO"}, [3]string{"ar_m_block", "ar_m_target", "YES"}, [3]string{"ar_m_actor", "ar_m_bridge", "NO"})
			s.exec("GRANT ar_m_target TO ar_m_block WITH ADMIN FALSE")
			actor(withoutAlternate...)
			// Role defaults affect newly omitted INHERIT, never an existing edge.
			s.exec("ALTER ROLE ar_m_actor NOINHERIT")
			actor(withoutAlternate...)
			s.exec("GRANT ar_m_new TO ar_m_actor")
			s.exec("GRANT ar_m_leaf TO ar_m_new WITH INHERIT FALSE, SET FALSE")
			actor([3]string{"ar_m_actor", "ar_m_block", "NO"}, [3]string{"ar_m_block", "ar_m_target", "NO"}, [3]string{"ar_m_actor", "ar_m_bridge", "NO"}, [3]string{"ar_m_actor", "ar_m_new", "NO"})
			s.exec("GRANT ar_m_new TO ar_m_actor WITH INHERIT TRUE")
			actor([3]string{"ar_m_actor", "ar_m_block", "NO"}, [3]string{"ar_m_block", "ar_m_target", "NO"}, [3]string{"ar_m_actor", "ar_m_bridge", "NO"}, [3]string{"ar_m_actor", "ar_m_new", "NO"}, [3]string{"ar_m_new", "ar_m_leaf", "NO"})
		})
	})
	t.Run("distinct_grantors", func(t *testing.T) {
		roles := []string{"ar_g_actor", "ar_g_target", "ar_g_one", "ar_g_two"}
		applicableRolesScenario(t, roles, func(s *applicableRolesCase) {
			for _, role := range roles {
				s.exec("CREATE ROLE " + role)
			}
			s.exec("GRANT ar_g_target TO ar_g_one WITH ADMIN OPTION")
			s.exec("GRANT ar_g_target TO ar_g_two WITH ADMIN OPTION")
			s.exec("GRANT ar_g_target TO ar_g_actor WITH ADMIN FALSE, INHERIT TRUE, SET TRUE GRANTED BY ar_g_one")
			s.exec("GRANT ar_g_target TO ar_g_actor WITH ADMIN FALSE, INHERIT TRUE, SET TRUE GRANTED BY ar_g_two")
			query := applicableRolesQuery + " WHERE grantee='ar_g_actor'"
			actor := func(triples ...[3]string) {
				s.exec("SET SESSION AUTHORIZATION ar_g_actor")
				s.readQuery(query, triples...)
				s.exec("RESET SESSION AUTHORIZATION")
			}
			actor([3]string{"ar_g_actor", "ar_g_target", "NO"})
			s.exec("GRANT ar_g_target TO ar_g_actor WITH ADMIN TRUE GRANTED BY ar_g_two")
			actor([3]string{"ar_g_actor", "ar_g_target", "NO"}, [3]string{"ar_g_actor", "ar_g_target", "YES"})
			s.exec("REVOKE ar_g_target FROM ar_g_actor GRANTED BY ar_g_two")
			actor([3]string{"ar_g_actor", "ar_g_target", "NO"})
			s.exec("REVOKE ar_g_target FROM ar_g_actor GRANTED BY ar_g_one")
			actor()
		})
	})
	t.Run("implicit_database_owner", func(t *testing.T) {
		roles := []string{"ar_o_owner", "ar_o_next", "ar_o_heir", "ar_o_plain"}
		applicableRolesScenario(t, roles, func(s *applicableRolesCase) {
			var database, bootstrap string
			if err := s.c.QueryRowContext(s.p.Ctx, "SELECT current_database(),session_user").Scan(&database, &bootstrap); err != nil {
				s.t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				for _, query := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO " + pq.QuoteIdentifier(bootstrap)} {
					if _, err := s.c.ExecContext(ctx, query); err != nil {
						s.t.Error(err)
					}
				}
			}()
			for _, role := range roles {
				s.exec("CREATE ROLE " + role)
			}
			s.exec("ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO ar_o_owner")
			s.exec("GRANT ar_o_owner TO ar_o_heir WITH INHERIT TRUE")
			query := applicableRolesQuery + " WHERE role_name='pg_database_owner'"
			held := s.held(query)
			held(s.projection(query, [3]string{"ar_o_owner", "pg_database_owner", "NO"}))
			s.exec("SET SESSION AUTHORIZATION ar_o_owner")
			s.read([3]string{"ar_o_owner", "pg_database_owner", "NO"})
			s.exec("RESET SESSION AUTHORIZATION")
			s.exec("SET SESSION AUTHORIZATION ar_o_heir")
			s.read([3]string{"ar_o_heir", "ar_o_owner", "NO"}, [3]string{"ar_o_owner", "pg_database_owner", "NO"})
			held(s.projection(query, [3]string{"ar_o_owner", "pg_database_owner", "NO"}))
			s.exec("RESET SESSION AUTHORIZATION")
			s.exec("SET SESSION AUTHORIZATION ar_o_plain")
			s.read()
			held(s.projection(query))
			s.exec("RESET SESSION AUTHORIZATION")
			s.exec("ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO ar_o_next")
			held(s.projection(query, [3]string{"ar_o_next", "pg_database_owner", "NO"}))
			s.exec("SET SESSION AUTHORIZATION ar_o_owner")
			s.read()
			held(s.projection(query))
			s.exec("RESET SESSION AUTHORIZATION")
			s.exec("SET SESSION AUTHORIZATION ar_o_heir")
			s.read([3]string{"ar_o_heir", "ar_o_owner", "NO"})
			held(s.projection(query))
			s.exec("RESET SESSION AUTHORIZATION")
			s.exec("SET SESSION AUTHORIZATION ar_o_next")
			s.read([3]string{"ar_o_next", "pg_database_owner", "NO"})
			held(s.projection(query, [3]string{"ar_o_next", "pg_database_owner", "NO"}))
		})
	})
	t.Run("quoted_spelling", func(t *testing.T) {
		applicableRolesScenario(t, []string{"Ar_Actor_雪", "Ar_Target_É", "Ar_Leaf_雪"}, func(s *applicableRolesCase) {
			s.exec(`CREATE ROLE "Ar_Actor_雪"`)
			s.exec(`CREATE ROLE "Ar_Target_É"`)
			s.exec(`CREATE ROLE "Ar_Leaf_雪"`)
			s.exec(`GRANT "Ar_Target_É" TO "Ar_Actor_雪" WITH INHERIT TRUE`)
			s.exec(`GRANT "Ar_Leaf_雪" TO "Ar_Target_É" WITH ADMIN TRUE, INHERIT FALSE, SET FALSE`)
			s.exec(`SET SESSION AUTHORIZATION "Ar_Actor_雪"`)
			s.read([3]string{"Ar_Actor_雪", "Ar_Target_É", "NO"}, [3]string{"Ar_Target_É", "Ar_Leaf_雪", "YES"})
		})
	})
}
