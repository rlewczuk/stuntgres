package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// A completed ACL utility is not a pending query-derived producer. An unrelated
// committed role must survive together with the retained ACL, data and own role.
func TestReferenceRoleTransactionalCompletedACL(t *testing.T) {
	for _, privateRole := range []bool{false, true} {
		t.Run(fmt.Sprintf("private_role=%v", privateRole), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rm_pending", "rm_peer"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					owner, ownerPID := fixture.RoleTransactionConn(t, p, b)
					peer, peerPID := fixture.RoleTransactionConn(t, p, b)
					run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
					run(peer, fixture.Exec("CREATE TABLE rm_data(v INT)", 0))
					run(peer, fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
					if !privateRole {
						run(peer, fixture.Exec("CREATE ROLE rm_pending LOGIN", 0))
					}
					run(peer, fixture.Exec("SELECT pg_advisory_lock(7543019::BIGINT)", 1))
					prefix := ""
					if privateRole {
						prefix = "CREATE ROLE rm_pending LOGIN; "
					}
					query := prefix + "GRANT SELECT ON rm_data TO rm_pending; INSERT INTO rm_data VALUES(1); SELECT pg_advisory_xact_lock(7543019::BIGINT)"
					join, completed := roleTransactionT2Worker(t, p, owner, query, false)
					// Release the real session lock before worker cleanup on failure.
					t.Cleanup(func() { peer.ExecContext(p.Ctx, "SELECT pg_advisory_unlock(7543019::BIGINT)") })
					fixture.RoleTransactionWait(t, p, b, ownerPID, peerPID, completed)
					run(peer, fixture.RoleTransactionExists("rm_pending", !privateRole))
					run(peer, fixture.Select("SELECT v FROM rm_data", []string{"v"}, []string{"INT4"}, nil))
					run(peer, fixture.Exec("CREATE ROLE rm_peer LOGIN", 0))
					run(peer, fixture.RoleTransactionExists("rm_peer", true))
					run(peer, fixture.Select("SELECT pg_advisory_unlock(7543019::BIGINT)", []string{"pg_advisory_unlock"}, []string{"BOOL"}, [][]any{{true}}))
					outcome := join()
					if outcome.Err != nil {
						t.Fatal("compatible retained ACL publication rejected", outcome.Err)
					}
					fixture.LockAffected(t, outcome.Result, 1)
					if _, err := outcome.Result.LastInsertId(); err == nil {
						t.Fatal("LastInsertId unexpectedly supported")
					}
					run(peer, fixture.RoleTransactionExists("rm_pending", true))
					run(peer, fixture.RoleTransactionExists("rm_peer", true))
					run(peer, fixture.Select("SELECT v FROM rm_data", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
					// Functional access proves the actual ACL survived; this is not
					// merely successful commit or role/catalog presence.
					run(owner, fixture.Exec("SET SESSION AUTHORIZATION rm_pending", 0))
					run(owner, fixture.RoleTransactionIdentity("rm_pending", "rm_pending"))
					run(owner, fixture.Select("SELECT v FROM rm_data", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
					run(owner, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(owner, fixture.Select("SELECT 42 AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(42)}}))
				})
			}
		})
	}
}
