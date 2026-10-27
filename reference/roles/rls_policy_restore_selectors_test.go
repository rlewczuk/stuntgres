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
	fixture.RoleMembershipCases(t, []string{owner}, func(s *fixture.RoleMembershipScenario) {
		if s.Backend.PG {
			fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, owner, func() {
				for _, statement := range statements {
					s.Exec(statement)
				}
			})
		} else {
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(strings.Join(statements, ";")+";")); err != nil {
				s.T.Fatal("selector-bearing Restore", err)
			}
		}
		// Native Restore owns a private stage session; it must not change the
		// caller's identity. PostgreSQL executes the directives on this session.
		actor := s.Bootstrap
		if s.Backend.PG {
			actor = owner
		}
		s.Run(fixture.Select("SELECT CURRENT_USER,CURRENT_ROLE,SESSION_USER", []string{"current_user", "current_role", "session_user"}, []string{"NAME", "NAME", "NAME"}, [][]any{{actor, actor, actor}}))
		s.Actor("")
		s.Run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		policyOrderedRoles(s, "p", "{"+owner+","+owner+","+owner+"}")
		pgPolicyFacts(s, "t", "p", "*", true, owner+","+owner+","+owner, "", "")
		if !s.Backend.PG {
			stream := policyDump(s)
			want := []string{owner, owner, owner}
			if got := policyExportRoles(s, stream, "p"); !reflect.DeepEqual(got, want) {
				s.T.Fatalf("Restore TO targets = %q, want %q", got, want)
			}
			// Reimport the complete emitted bytes, not a reconstructed policy.
			for range 2 {
				if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(stream)); err != nil {
					s.T.Fatal("emitted selector policy reimport", err)
				}
				if got := policyExportRoles(s, policyDump(s), "p"); !reflect.DeepEqual(got, want) {
					s.T.Fatalf("reimported TO targets = %q, want %q", got, want)
				}
				s.Run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				pgPolicyFacts(s, "t", "p", "*", true, owner+","+owner+","+owner, "", "")
			}
			policyRestoreSelectorsReplay(s, stream, owner)
		}
		// Ownership alone would also block DROP ROLE. The three additional
		// target occurrences distinguish the actual policy dependency vector.
		message := `role "` + owner + `" cannot be dropped because some objects depend on it`
		s.Fail("DROP ROLE "+owner, "2BP01", message, "owner of table t\ntarget of policy p on table t\ntarget of policy p on table t\ntarget of policy p on table t")
		s.Exec("ALTER POLICY p ON t TO PUBLIC")
		policyOrderedRoles(s, "p", "{public}")
		pgPolicyFacts(s, "t", "p", "*", true, "public", "", "")
		s.Fail("DROP ROLE "+owner, "2BP01", message, "owner of table t")
		s.Exec("DROP POLICY p ON t")
		s.Run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		s.Exec("DROP TABLE t")
		s.Exec("DROP ROLE " + owner)
	})
}

// Supplement the shared SQL/control body with an independent pinned consumer
// of the actual Dump bytes. Clean its objects/role before fixture.RoleMembershipCases runs
// its PostgreSQL backend; the stream itself is never rewritten.
func policyRestoreSelectorsReplay(s *fixture.RoleMembershipScenario, stream, owner string) {
	s.T.Helper()
	if s.P.Postgres == nil {
		return
	}
	c, err := s.P.Postgres.Conn(s.P.Ctx)
	if err != nil {
		s.T.Fatal(err)
	}
	defer c.Close()
	defer func() {
		c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		c.ExecContext(context.Background(), "DROP TABLE IF EXISTS t")
		c.ExecContext(context.Background(), "DROP ROLE IF EXISTS "+owner)
	}()
	consumer := *s
	consumer.C, consumer.Record = c, s.P.PostgresRecord
	consumer.Backend.PG = true
	consumer.Exec("CREATE ROLE " + owner + " LOGIN")
	// Whole scripts are an actual PostgreSQL Exec route, not prepared SQL or
	// a fabricated server Restore API. Subsequent probes use both SQL routes.
	fixture.WithPublicSchemaOwner(s.T, s.P, fixture.Backend{Name: "postgres", DB: s.P.Postgres, Recorder: s.P.PostgresRecord, PG: true}, owner, func() {
		if _, err := c.ExecContext(s.P.Ctx, stream); err != nil {
			s.T.Fatal("PostgreSQL rejected actual selector policy Dump", err)
		}
	})
	consumer.Actor("")
	policyOrderedRoles(&consumer, "p", "{"+owner+","+owner+","+owner+"}")
	pgPolicyFacts(&consumer, "t", "p", "*", true, owner+","+owner+","+owner, "", "")
	consumer.Run(fixture.Select("SELECT r.rolname AS owner FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='t'::regclass", []string{"owner"}, []string{"NAME"}, [][]any{{owner}}))
	consumer.Run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
}
