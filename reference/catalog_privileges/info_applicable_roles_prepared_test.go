package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoApplicableRolesPrepared(t *testing.T) {
	roles := []string{"ar_p_actor", "ar_p_target", "ar_p_leaf", "ar_p_hidden", "ar_p_secret", "ar_p_actor_new", "ar_p_target_new"}
	applicableRolesScenario(t, roles, func(s *applicableRolesCase) {
		for _, role := range roles[:5] {
			s.exec("CREATE ROLE " + role)
		}
		for _, query := range []string{
			"GRANT ar_p_target TO ar_p_actor WITH ADMIN FALSE, INHERIT TRUE, SET TRUE",
			"GRANT ar_p_leaf TO ar_p_target WITH INHERIT FALSE, SET FALSE",
			"GRANT ar_p_secret TO ar_p_hidden",
		} {
			s.exec(query)
		}
		admin, err := s.b.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer admin.Close()
		adminRun := func(query string) { fixture.RunStep(s.t, s.p.Ctx, admin, s.b.Recorder, fixture.Exec(query, 0)) }
		query := applicableRolesQuery + " WHERE grantee IN ('ar_p_actor','ar_p_target','ar_p_leaf','ar_p_hidden','ar_p_secret','ar_p_actor_new','ar_p_target_new')"
		// Prepare as a real superuser; execution must not retain that authority.
		held := s.held(query)
		held(s.projection(query,
			[3]string{"ar_p_actor", "ar_p_target", "NO"},
			[3]string{"ar_p_target", "ar_p_leaf", "NO"},
			[3]string{"ar_p_hidden", "ar_p_secret", "NO"},
		))
		s.exec("SET SESSION AUTHORIZATION ar_p_actor")
		ordinaryHeld := s.held(query)
		check := func(triples ...[3]string) {
			held(s.projection(query, triples...))
			ordinaryHeld(s.projection(query, triples...))
			s.read(triples...)
		}
		check([3]string{"ar_p_actor", "ar_p_target", "NO"}, [3]string{"ar_p_target", "ar_p_leaf", "NO"})
		adminRun("GRANT ar_p_target TO ar_p_actor WITH INHERIT FALSE")
		check([3]string{"ar_p_actor", "ar_p_target", "NO"})
		adminRun("GRANT ar_p_target TO ar_p_actor WITH SET FALSE")
		check([3]string{"ar_p_actor", "ar_p_target", "NO"})
		adminRun("GRANT ar_p_target TO ar_p_actor WITH ADMIN TRUE")
		check([3]string{"ar_p_actor", "ar_p_target", "YES"})
		adminRun("GRANT ar_p_target TO ar_p_actor WITH INHERIT TRUE")
		check([3]string{"ar_p_actor", "ar_p_target", "YES"}, [3]string{"ar_p_target", "ar_p_leaf", "NO"})
		adminRun("GRANT ar_p_target TO ar_p_actor WITH SET TRUE")
		check([3]string{"ar_p_actor", "ar_p_target", "YES"}, [3]string{"ar_p_target", "ar_p_leaf", "NO"})
		s.exec("SET ROLE ar_p_target")
		check([3]string{"ar_p_target", "ar_p_leaf", "NO"})
		s.run(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"ar_p_actor", "ar_p_target"}}))
		s.exec("RESET ROLE")
		check([3]string{"ar_p_actor", "ar_p_target", "YES"}, [3]string{"ar_p_target", "ar_p_leaf", "NO"})
		adminRun("REVOKE ar_p_target FROM ar_p_actor")
		check()
		adminRun("GRANT ar_p_target TO ar_p_actor WITH ADMIN FALSE, INHERIT TRUE, SET TRUE")
		check([3]string{"ar_p_actor", "ar_p_target", "NO"}, [3]string{"ar_p_target", "ar_p_leaf", "NO"})

		// Native Rows are eagerly captured. Retain the actual returned object
		// across independent role publications, rather than copying its cells.
		// pq streams and has no corresponding eager capture guarantee.
		var retained *sql.Rows
		var saved []*sql.ColumnType
		if !s.b.PG {
			retained, err = s.c.QueryContext(s.p.Ctx, query)
			if err != nil {
				s.t.Fatal(err)
			}
			defer retained.Close()
			saved, err = retained.ColumnTypes()
			if err != nil {
				s.t.Fatal(err)
			}
			applicableRolesMetadata(s.t, saved, false)
		}
		adminRun("ALTER ROLE ar_p_target RENAME TO ar_p_target_new")
		adminRun("ALTER ROLE ar_p_actor RENAME TO ar_p_actor_new")
		adminRun("GRANT ar_p_target_new TO ar_p_actor_new WITH ADMIN TRUE")
		if retained != nil {
			s.consume(retained, [3]string{"ar_p_actor", "ar_p_target", "NO"}, [3]string{"ar_p_target", "ar_p_leaf", "NO"})
			applicableRolesMetadata(s.t, saved, false)
		}
		check([3]string{"ar_p_actor_new", "ar_p_target_new", "YES"}, [3]string{"ar_p_target_new", "ar_p_leaf", "NO"})
		s.run(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"ar_p_actor_new", "ar_p_actor_new"}}))
		s.exec("RESET SESSION AUTHORIZATION")
		// The very same ordinary-prepared statement now sees the hidden edge.
		all := s.projection(query,
			[3]string{"ar_p_actor_new", "ar_p_target_new", "YES"},
			[3]string{"ar_p_target_new", "ar_p_leaf", "NO"},
			[3]string{"ar_p_hidden", "ar_p_secret", "NO"},
		)
		held(all)
		ordinaryHeld(all)
		adminRun("REVOKE ar_p_target_new FROM ar_p_actor_new")
		s.exec("SET SESSION AUTHORIZATION ar_p_actor_new")
		check()
		s.exec("RESET SESSION AUTHORIZATION")
		adminRun("DROP ROLE ar_p_target_new")
		adminRun("CREATE ROLE ar_p_target_new")
		adminRun("DROP ROLE ar_p_actor_new")
		adminRun("CREATE ROLE ar_p_actor_new")
		s.exec("SET SESSION AUTHORIZATION ar_p_actor_new")
		check() // Same spellings, new identities: no old incoming/outgoing edges.
		adminRun("GRANT ar_p_target_new TO ar_p_actor_new WITH ADMIN FALSE, INHERIT TRUE, SET TRUE")
		check([3]string{"ar_p_actor_new", "ar_p_target_new", "NO"})
		if !s.b.PG {
			ctx, cancel := context.WithCancel(s.p.Ctx)
			cancel()
			rows, err := s.c.QueryContext(ctx, applicableRolesQuery)
			if rows != nil {
				_ = rows.Close()
			}
			if !errors.Is(err, context.Canceled) {
				s.t.Fatalf("precanceled native query error=%v", err)
			}
		}
		check([3]string{"ar_p_actor_new", "ar_p_target_new", "NO"})
		s.exec("RESET SESSION AUTHORIZATION")
		ordinaryHeld(s.projection(query,
			[3]string{"ar_p_actor_new", "ar_p_target_new", "NO"},
			[3]string{"ar_p_hidden", "ar_p_secret", "NO"},
		))
	})
}
