package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleRenameTransactions(t *testing.T) {
	for _, mode := range []string{"sql", "go"} {
		for _, route := range []string{"direct", "fresh", "held"} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/commit=%v", mode, route, commit), func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"rr_tx_old", "rr_tx_mid", "rr_tx_final", "rr_tx_collision"})
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						c, _ := fixture.RoleTransactionConn(t, p, b)
						run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
						run(b.DB, fixture.Exec("CREATE ROLE rr_tx_old", 0))
						run(b.DB, fixture.Exec("CREATE ROLE rr_tx_collision", 0))
						owner, finish := fixture.RoleTransactionBegin(t, p, b, c, mode)
						execute := func(s fixture.Step) {
							if route == "held" {
								fixture.RoleTransactionStep(t, p, b, owner, true, s)
							} else {
								s.Prepared = route == "fresh"
								run(owner, s)
							}
						}
						execute(fixture.Exec("ALTER ROLE rr_tx_old RENAME TO rr_tx_mid", 0))
						run(owner, fixture.RoleTransactionExists("rr_tx_mid", true))
						run(owner, fixture.RoleTransactionExists("rr_tx_old", false))
						run(b.DB, fixture.RoleTransactionExists("rr_tx_old", true))
						run(b.DB, fixture.RoleTransactionExists("rr_tx_mid", false))
						run(owner, fixture.Exec("SAVEPOINT rename_suffix", 0))
						execute(fixture.Exec("ALTER USER rr_tx_mid RENAME TO rr_tx_final", 0))
						run(owner, fixture.RoleTransactionExists("rr_tx_final", true))
						run(owner, fixture.Exec("ROLLBACK TO rename_suffix", 0))
						run(owner, fixture.RoleTransactionExists("rr_tx_mid", true))
						run(owner, fixture.RoleTransactionExists("rr_tx_final", false))
						execute(fixture.RoleReferenceError("ALTER ROLE rr_tx_mid RENAME TO rr_tx_collision", "42710", `role "rr_tx_collision" already exists`, "", false, "execute"))
						run(owner, fixture.ErrorDiagnostic("SELECT 1", fixture.TransactionAborted()))
						run(owner, fixture.Exec("ROLLBACK TO rename_suffix", 0))
						run(owner, fixture.RoleTransactionExists("rr_tx_mid", true))
						run(owner, fixture.RoleTransactionExists("rr_tx_collision", true))
						run(owner, fixture.Exec("RELEASE rename_suffix", 0))
						finish(commit)
						run(b.DB, fixture.RoleTransactionExists("rr_tx_old", !commit))
						run(b.DB, fixture.RoleTransactionExists("rr_tx_mid", commit))
						run(b.DB, fixture.RoleTransactionExists("rr_tx_final", false))
					}
				})
			}
		}
	}
}

func TestReferenceRoleRenameHeldAuthority(t *testing.T) {
	for _, change := range []string{"admin", "createrole", "effective", "target-superuser"} {
		t.Run(change, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rr_held_actor", "rr_held_target", "rr_held_new", "rr_held_other"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				admin := func(s fixture.Step) { fixture.RoleAttributesRun(t, p, b, b.DB, false, s) }
				for _, q := range []string{"CREATE ROLE rr_held_actor LOGIN CREATEROLE PASSWORD 'role-attributes-fixture'", "CREATE ROLE rr_held_target", "CREATE ROLE rr_held_other", "GRANT rr_held_target TO rr_held_actor WITH ADMIN TRUE", "GRANT rr_held_other TO rr_held_actor"} {
					admin(fixture.Exec(q, 0))
				}
				pool := fixture.RoleAttributesPool(t, p, b, "rr_held_actor")
				c, err := pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				query := "ALTER ROLE rr_held_target RENAME TO rr_held_new"
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal("authorized held Prepare", err)
				}
				defer stmt.Close()
				switch change {
				case "admin":
					admin(fixture.Exec("REVOKE ADMIN OPTION FOR rr_held_target FROM rr_held_actor", 0))
				case "createrole":
					admin(fixture.Exec("ALTER ROLE rr_held_actor NOCREATEROLE", 0))
				case "effective":
					fixture.RoleAttributesRun(t, p, b, c, false, fixture.Exec("SET ROLE rr_held_other", 0))
				case "target-superuser":
					admin(fixture.Exec("ALTER ROLE rr_held_target SUPERUSER", 0))
				}
				execute := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				detail := `Only roles with the CREATEROLE attribute and the ADMIN option on role "rr_held_target" may rename this role.`
				if change == "target-superuser" {
					detail = "Only roles with the SUPERUSER attribute may rename roles with the SUPERUSER attribute."
				}
				denied := fixture.RoleReferenceError(query, "42501", "permission denied to rename role", detail, false, "execute")
				fixture.RunStep(t, p.Ctx, execute, b.Recorder, denied)
				admin(fixture.RoleTransactionExists("rr_held_target", true))
				admin(fixture.RoleTransactionExists("rr_held_new", false))
				switch change {
				case "admin":
					admin(fixture.Exec("GRANT rr_held_target TO rr_held_actor WITH ADMIN TRUE", 0))
				case "createrole":
					admin(fixture.Exec("ALTER ROLE rr_held_actor CREATEROLE", 0))
				case "effective":
					fixture.RoleAttributesRun(t, p, b, c, false, fixture.Exec("RESET ROLE", 0))
				case "target-superuser":
					admin(fixture.Exec("ALTER ROLE rr_held_target NOSUPERUSER", 0))
				}
				fixture.RunStep(t, p.Ctx, execute, b.Recorder, fixture.Exec(query, 0))
				admin(fixture.RoleTransactionExists("rr_held_new", true))
			}
		})
	}
}

func TestReferenceRoleRenameCancellation(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rr_cancel", "rr_cancel_new"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
				run(b.DB, fixture.Exec("CREATE ROLE rr_cancel", 0))
				holder, holderPID := fixture.RoleTransactionConn(t, p, b)
				actor, actorPID := fixture.RoleTransactionConn(t, p, b)
				query := "ALTER ROLE rr_cancel RENAME TO rr_cancel_new"
				var execute fixture.Executor = actor
				if held {
					stmt, err := actor.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					execute = fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: query}
				}
				run(holder, fixture.Exec("BEGIN", 0))
				run(holder, fixture.Exec("ALTER ROLE rr_cancel CREATEDB", 0))
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				done := make(chan struct{})
				join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(done)
					return fixture.RowExecute(ctx, execute, query, false)
				})
				// Registered after the worker so fatal paths release the holder first.
				t.Cleanup(func() { holder.ExecContext(p.Ctx, "ROLLBACK") })
				fixture.RoleTransactionWait(t, p, b, actorPID, holderPID, done)
				if b.PG {
					var signaled bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", actorPID).Scan(&signaled); err != nil || !signaled {
						t.Fatal(signaled, err)
					}
				} else {
					cancel()
				}
				outcome := join()
				if outcome.Result != nil || outcome.Rows != nil {
					t.Fatal("canceled rename exposed output")
				}
				if b.PG {
					rsCatalogUpdateCanceled(t, outcome.Err, "pg_authid")
				} else {
					fixture.LexicalOrdinary(t, outcome.Err, context.Canceled)
				}
				run(holder, fixture.Exec("ROLLBACK", 0))
				run(b.DB, fixture.RoleTransactionExists("rr_cancel", true))
				run(b.DB, fixture.RoleTransactionExists("rr_cancel_new", false))
				run(actor, fixture.Select("SELECT 7 AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(actor, fixture.Exec(query, 0))
				run(b.DB, fixture.RoleTransactionExists("rr_cancel", false))
				run(b.DB, fixture.RoleTransactionExists("rr_cancel_new", true))
			}
		})
	}
}
