package reference_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The same selector-bearing SQL runs on PostgreSQL and in the private native
// Restore session. Trusted creation authority must not become a TO principal.
func TestReferenceRLSPolicyRestoreSelectors(t *testing.T) {
	const owner = "policy_restore_owner"
	statements := []string{
		"SET SESSION AUTHORIZATION " + owner,
		"CREATE TABLE t(id INTEGER)",
		"CREATE POLICY p ON t TO CURRENT_USER,CURRENT_ROLE,SESSION_USER",
	}
	membershipCases(t, []string{owner}, func(s *membershipScenario) {
		if s.backend.PG {
			s.exec("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC")
			for _, statement := range statements {
				s.exec(statement)
			}
		} else {
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(strings.Join(statements, ";")+";")); err != nil {
				s.t.Fatal("selector-bearing Restore", err)
			}
		}
		// Native Restore owns a private stage session; it must not change the
		// caller's identity. PostgreSQL executes the directives on this session.
		actor := s.bootstrap
		if s.backend.PG {
			actor = owner
		}
		s.run(fixture.Select("SELECT CURRENT_USER,CURRENT_ROLE,SESSION_USER", []string{"current_user", "current_role", "session_user"}, []string{"NAME", "NAME", "NAME"}, [][]any{{actor, actor, actor}}))
		s.actor("")
		s.run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		policyOrderedRoles(s, "p", "{"+owner+","+owner+","+owner+"}")
		if !s.backend.PG {
			stream := policyDump(s)
			want := []string{owner, owner, owner}
			if got := policyExportRoles(s, stream, "p"); !reflect.DeepEqual(got, want) {
				s.t.Fatalf("Restore TO targets = %q, want %q", got, want)
			}
			// Reimport the complete emitted bytes, not a reconstructed policy.
			for range 2 {
				if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(stream)); err != nil {
					s.t.Fatal("emitted selector policy reimport", err)
				}
				if got := policyExportRoles(s, policyDump(s), "p"); !reflect.DeepEqual(got, want) {
					s.t.Fatalf("reimported TO targets = %q, want %q", got, want)
				}
				s.run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
			policyRestoreSelectorsReplay(s, stream, owner)
		}
		// Ownership alone would also block DROP ROLE. The three additional
		// target occurrences distinguish the actual policy dependency vector.
		message := `role "` + owner + `" cannot be dropped because some objects depend on it`
		s.fail("DROP ROLE "+owner, "2BP01", message, "owner of table t\ntarget of policy p on table t\ntarget of policy p on table t\ntarget of policy p on table t")
		s.exec("ALTER POLICY p ON t TO PUBLIC")
		policyOrderedRoles(s, "p", "{public}")
		s.fail("DROP ROLE "+owner, "2BP01", message, "owner of table t")
		s.exec("DROP POLICY p ON t")
		s.run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		s.exec("DROP TABLE t")
		s.exec("DROP ROLE " + owner)
	})
}

// Supplement the shared SQL/control body with an independent pinned consumer
// of the actual Dump bytes. Clean its objects/role before membershipCases runs
// its PostgreSQL backend; the stream itself is never rewritten.
func policyRestoreSelectorsReplay(s *membershipScenario, stream, owner string) {
	s.t.Helper()
	if s.p.Postgres == nil {
		return
	}
	c, err := s.p.Postgres.Conn(s.p.Ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	defer c.Close()
	defer func() {
		c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		c.ExecContext(context.Background(), "DROP TABLE IF EXISTS t")
		c.ExecContext(context.Background(), "DROP ROLE IF EXISTS "+owner)
	}()
	consumer := *s
	consumer.c, consumer.record = c, s.p.PostgresRecord
	consumer.backend.PG = true
	consumer.exec("CREATE ROLE " + owner + " LOGIN")
	consumer.exec("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC")
	// Whole scripts are an actual PostgreSQL Exec route, not prepared SQL or
	// a fabricated server Restore API. Subsequent probes use both SQL routes.
	if _, err := c.ExecContext(s.p.Ctx, stream); err != nil {
		s.t.Fatal("PostgreSQL rejected actual selector policy Dump", err)
	}
	consumer.actor("")
	policyOrderedRoles(&consumer, "p", "{"+owner+","+owner+","+owner+"}")
	consumer.run(fixture.Select("SELECT r.rolname AS owner FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='t'::regclass", []string{"owner"}, []string{"NAME"}, [][]any{{owner}}))
	consumer.run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
}
