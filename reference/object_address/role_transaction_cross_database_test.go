package reference_test

import (
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoleTransactionalCrossDatabase(t *testing.T) {
	for _, kind := range []string{"owner_block", "default_block", "drop_commit", "drop_rollback", "creator_commit", "cross_deadlock", "local_resources", "reset"} {
		t.Run(kind, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rx_role", "rx_second_role", "rx_member", "rx_private"})
			databases := []string{"rx_second"}
			fixture.DatabaseReferenceCleanup(t, &databases)
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, apid := fixture.RoleTransactionConn(t, p, b)
					run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
					for _, q := range []string{"CREATE ROLE rx_role", "CREATE ROLE rx_second_role", "CREATE ROLE rx_member", "CREATE DATABASE rx_second"} {
						run(a, fixture.Exec(q, 0))
					}
					pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "rx_second", "")
					second := b
					second.DB, second.Recorder = pool, recorder
					w, wpid := fixture.RoleTransactionConn(t, p, second)
					// Cluster role publication is visible through the genuine alternate database attachment.
					run(a, fixture.Exec("BEGIN", 0))
					run(a, fixture.Exec("CREATE ROLE rx_private", 0))
					run(w, fixture.RoleTransactionExists("rx_private", false))
					run(a, fixture.Exec("COMMIT", 0))
					run(w, fixture.RoleTransactionExists("rx_private", true))
					if kind == "cross_deadlock" {
						roleTransactionCrossDeadlock(t, p, b, a, apid, w, wpid)
						return
					}
					if kind == "local_resources" || kind == "reset" {
						for _, e := range []fixture.Executor{a, w} {
							run(e, fixture.Exec("CREATE TABLE rx_local(i INT)", 0))
						}
						run(w, fixture.Exec("INSERT INTO rx_local VALUES(7)", 1))
						run(a, fixture.Exec("BEGIN", 0))
						run(a, fixture.Exec("LOCK TABLE rx_local IN ACCESS EXCLUSIVE MODE", 0))
						run(a, fixture.Exec("SELECT pg_advisory_xact_lock(7)", 1))
						run(w, fixture.Exec("BEGIN", 0))
						// Equal keys and local relation names in different DBs must not conflict.
						run(w, fixture.Exec("LOCK TABLE rx_local IN ACCESS EXCLUSIVE MODE", 0))
						run(w, fixture.Exec("SELECT pg_advisory_xact_lock(7)", 1))
						run(a, fixture.Exec("GRANT rx_role TO rx_member", 0))
						run(w, fixture.Exec("GRANT rx_second_role TO rx_member", 0))
						if kind == "reset" && !b.PG {
							if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
								t.Fatal(err)
							}
							_, err := a.ExecContext(p.Ctx, "COMMIT")
							fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
							run(a, fixture.Exec("ROLLBACK", 0))
							// DB2 retains its shared-role hold while DB1's owner is fenced.
							peer, peerPID := fixture.RoleTransactionConn(t, p, b)
							join, done := roleTransactionT2Worker(t, p, peer, "GRANT rx_second_role TO rx_private", false)
							t.Cleanup(func() { w.ExecContext(p.Ctx, "ROLLBACK") })
							fixture.RoleTransactionWait(t, p, b, peerPID, wpid, done)
							run(w, fixture.Exec("COMMIT", 0))
							roleTransactionT2Success(t, join())
						} else {
							run(a, fixture.Exec("COMMIT", 0))
							run(w, fixture.Exec("COMMIT", 0))
						}
						run(w, fixture.Select("SELECT i FROM rx_local", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
						run(w, fixture.RoleTransactionExists("rx_role", true))
						return
					}
					if kind == "owner_block" || kind == "default_block" {
						q := "CREATE SCHEMA rx_dep AUTHORIZATION rx_role"
						if kind == "default_block" {
							q = "ALTER DEFAULT PRIVILEGES FOR ROLE rx_role GRANT SELECT ON TABLES TO rx_member"
						}
						run(w, fixture.Exec(q, 0))
						run(a, fixture.Exec("BEGIN", 0))
						want := fixture.ControlsReferenceDiagnostic("2BP01", `role "rx_role" cannot be dropped because some objects depend on it`)
						want.Detail = "1 object in database rx_second"
						run(a, fixture.ErrorDiagnostic("DROP ROLE rx_role", want))
						run(a, fixture.Exec("ROLLBACK", 0))
						run(w, fixture.RoleTransactionExists("rx_role", true))
						return
					}
					var oid int64
					if err := a.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname='rx_role'").Scan(&oid); err != nil || oid <= 0 {
						t.Fatal(oid, err)
					}
					creator := "CREATE SCHEMA rx_dep AUTHORIZATION rx_role"
					if kind == "creator_commit" {
						run(w, fixture.Exec("BEGIN", 0))
						run(w, fixture.Exec(creator, 0))
						run(a, fixture.Exec("BEGIN", 0))
						join, done := roleTransactionT2Worker(t, p, a, "DROP ROLE rx_role", true)
						t.Cleanup(func() { w.ExecContext(p.Ctx, "ROLLBACK") })
						fixture.RoleTransactionWait(t, p, b, apid, wpid, done)
						run(w, fixture.Exec("COMMIT", 0))
						outcome := join()
						want := fixture.ControlsReferenceDiagnostic("2BP01", `role "rx_role" cannot be dropped because some objects depend on it`)
						want.Detail = "1 object in database rx_second"
						fixture.AssertDiagnostic(t, outcome.Err, &want)
						run(a, fixture.Exec("ROLLBACK", 0))
					} else {
						run(a, fixture.Exec("BEGIN", 0))
						run(a, fixture.Exec("DROP ROLE rx_role", 0))
						join, done := roleTransactionT2Worker(t, p, w, creator, true)
						t.Cleanup(func() { a.ExecContext(p.Ctx, "ROLLBACK") })
						fixture.RoleTransactionWait(t, p, b, wpid, apid, done)
						end := "ROLLBACK"
						if kind == "drop_commit" {
							end = "COMMIT"
						}
						run(a, fixture.Exec(end, 0))
						outcome := join()
						if kind == "drop_commit" {
							want := fixture.ControlsReferenceDiagnostic("42704", fmt.Sprintf("role %d was concurrently dropped", oid))
							fixture.AssertDiagnostic(t, outcome.Err, &want)
						} else {
							roleTransactionT2Success(t, outcome)
						}
					}
					present := kind != "drop_commit"
					run(w, fixture.RoleTransactionExists("rx_role", present))
					var count int64
					if present {
						count = 1
					}
					fixture.RunStep(t, p.Ctx, w, second.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_namespace WHERE nspname='rx_dep'", []string{"n"}, []string{"INT8"}, [][]any{{count}}))
				})
			}
		})
	}
}
