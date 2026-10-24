package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalPrivilegesNonTableDelegation(t *testing.T) {
	for _, goTx := range []bool{false, true} {
		for _, independent := range []bool{false, true} {
			name := "sql/chain"
			if goTx {
				name = "go/chain"
			}
			if independent {
				name += "+independent"
			}
			t.Run(name, func(t *testing.T) {
				objectGrantScenario(t, []string{"tx_nt"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ func(fixture.Step), prepared bool) {
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					run := func(e fixture.Executor, s fixture.Step) {
						t.Helper()
						if !t.Run(fmt.Sprintf("%T/%s", e, s.Query), func(t *testing.T) {
							fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
						}) {
							t.FailNow()
						}
					}
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
						defer cancel()
						c.ExecContext(ctx, "ROLLBACK")
						c.ExecContext(ctx, "RESET ROLE")
						peer.ExecContext(ctx, "RESET ROLE")
					}()
					var owner string
					if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
						t.Fatal(err)
					}
					for _, q := range []string{"GRANT USAGE ON SCHEMA public TO og_object_reader,og_object_other", "CREATE SCHEMA tx_nt", "REVOKE ALL ON SCHEMA tx_nt FROM PUBLIC", "GRANT USAGE ON SCHEMA tx_nt TO og_object_reader,og_object_other", "CREATE TABLE tx_nt.t(a INT)", "GRANT SELECT ON tx_nt.t TO og_object_reader,og_object_other"} {
						run(c, fixture.Exec(q, 0))
					}
					run(c, fixture.Exec("INSERT INTO tx_nt.t VALUES(7)", 1))
					// Keep lookup authority independent: this chain delegates CREATE,
					// and each execution must check it even on the same held statement.
					grantA := "GRANT CREATE ON SCHEMA tx_nt TO og_object_reader WITH GRANT OPTION"
					grantB := "GRANT CREATE ON SCHEMA tx_nt TO og_object_other"
					option := "REVOKE GRANT OPTION FOR CREATE ON SCHEMA tx_nt FROM og_object_reader"
					cascade := option + " CASCADE"
					ordinary := "REVOKE CREATE ON SCHEMA tx_nt FROM og_object_other"
					reset := "REVOKE CREATE ON SCHEMA tx_nt FROM og_object_reader,og_object_other CASCADE"
					ownerQuery := "CREATE TABLE tx_nt.owner_probe(a INT)"
					peerQuery := "CREATE TABLE tx_nt.peer_probe(a INT)"
					ownerHeld, peerHeld := make(map[string]*sql.Stmt), make(map[string]*sql.Stmt)
					defer func() {
						for _, s := range ownerHeld {
							s.Close()
						}
						for _, s := range peerHeld {
							s.Close()
						}
					}()
					if prepared {
						for _, q := range []string{grantA, grantB, option, option + " RESTRICT", cascade, ordinary} {
							ownerHeld[q], err = c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
						}
					}
					command := func(e fixture.Executor, held map[string]*sql.Stmt, q string) {
						run(transactionalPrivilegeHeld(e, held, q), fixture.Exec(q, 0))
					}
					chain := func(e fixture.Executor, held map[string]*sql.Stmt) {
						command(e, held, grantA)
						run(e, fixture.Exec("SET ROLE og_object_reader", 0))
						command(e, held, grantB)
						run(e, fixture.Exec("RESET ROLE", 0))
						if independent {
							command(e, held, grantB)
						}
					}
					chain(c, ownerHeld)
					run(c, fixture.Exec("SET ROLE og_object_other", 0))
					run(peer, fixture.Exec("SET ROLE og_object_other", 0))
					ownerHeld[ownerQuery], err = c.PrepareContext(p.Ctx, ownerQuery)
					if err != nil {
						t.Fatal("owner held consumer", err)
					}
					peerHeld[peerQuery], err = peer.PrepareContext(p.Ctx, peerQuery)
					if err != nil {
						t.Fatal("peer held consumer", err)
					}
					run(c, fixture.Exec("RESET ROLE", 0))
					probe := func(e fixture.Executor, held map[string]*sql.Stmt, actor string, allowed, inBlock bool) {
						table, query := "owner_probe", ownerQuery
						if actor == "" {
							table, query = "peer_probe", peerQuery
						}
						for _, heldConsumer := range []bool{false, true} {
							if actor != "" && inBlock {
								run(e, fixture.Exec("SAVEPOINT consumer_probe", 0))
							}
							if actor != "" {
								run(e, fixture.Exec("SET ROLE "+actor, 0))
							}
							var s fixture.Step
							if allowed {
								s = fixture.Exec(query, 0)
							} else {
								s = fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema tx_nt", Position: fixture.Position(14)})
								s.ErrorStage = "execute"
							}
							source := e
							if heldConsumer {
								source = transactionalPrivilegeHeld(e, held, query)
							}
							run(source, s)
							if allowed {
								run(e, fixture.Exec("INSERT INTO tx_nt."+table+" VALUES(7)", 1))
								run(e, fixture.Select("SELECT a FROM tx_nt."+table, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
								run(e, fixture.Exec("DROP TABLE tx_nt."+table, 0))
							}
							if actor != "" && inBlock && !allowed {
								run(e, fixture.Exec("ROLLBACK TO consumer_probe", 0))
							} else if actor != "" {
								run(e, fixture.Exec("RESET ROLE", 0))
							}
							if actor != "" && inBlock {
								run(e, fixture.Exec("RELEASE consumer_probe", 0))
							}
							// Both successful cleanup and denied creation leave no target.
							run(e, fixture.Select("SELECT count(*) AS v FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='tx_nt' AND c.relname='"+table+"'", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}))
						}
					}
					acl := func(e fixture.Executor, aOption, delegated, direct bool) {
						rows := [][]any{{owner, owner, false}, {"og_object_reader", owner, aOption}}
						if delegated {
							rows = append(rows, []any{"og_object_other", "og_object_reader", false})
						}
						if direct {
							rows = append(rows, []any{"og_object_other", owner, false})
						}
						// USAGE is a separate committed prerequisite, not an origin in
						// this dependent CREATE graph.
						s := fixture.Select("SELECT r.rolname AS grantee,g.rolname AS grantor,a.is_grantable AS opt FROM pg_namespace n CROSS JOIN LATERAL aclexplode(n.nspacl) a JOIN pg_roles r ON r.oid=a.grantee JOIN pg_roles g ON g.oid=a.grantor WHERE n.nspname='tx_nt' AND a.privilege_type='CREATE'", []string{"grantee", "grantor", "opt"}, []string{"NAME", "NAME", "BOOL"}, rows)
						s.Bag = true
						run(e, s)
					}
					tableACL := func(e fixture.Executor) {
						s := fixture.Select("SELECT r.rolname AS grantee,a.privilege_type AS privilege,a.is_grantable AS opt FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE c.oid='tx_nt.t'::regclass AND r.rolname IN ('og_object_reader','og_object_other')", []string{"grantee", "privilege", "opt"}, []string{"NAME", "TEXT", "BOOL"}, [][]any{{"og_object_reader", "SELECT", false}, {"og_object_other", "SELECT", false}})
						s.Bag = true
						run(e, s)
						run(e, fixture.Exec("SET ROLE og_object_other", 0))
						run(e, fixture.Select("SELECT a FROM tx_nt.t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
						run(e, fixture.Exec("RESET ROLE", 0))
					}
					for _, commit := range []bool{false, true} {
						run(c, fixture.Exec(reset, 0))
						probe(peer, peerHeld, "", false, false)
						e, held, finish, cleanup := transactionalPrivilegeBlock(t, p, b, c, goTx, ownerHeld)
						defer cleanup()
						chain(e, held)
						acl(e, true, true, independent)
						probe(e, held, "og_object_other", true, true)
						probe(peer, peerHeld, "", false, false)
						tableACL(e)
						finish(commit)
						probe(peer, peerHeld, "", commit, false)
						probe(c, ownerHeld, "og_object_other", commit, false)
					}
					// Both spellings fail atomically; aborted-state recovery restores the
					// complete chain, not only a flattened effective right.
					e, held, finish, cleanup := transactionalPrivilegeBlock(t, p, b, c, goTx, ownerHeld)
					defer cleanup()
					run(e, fixture.Exec("SAVEPOINT restrict_mark", 0))
					for _, q := range []string{option, option + " RESTRICT"} {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."})
						s.ErrorStage = "execute"
						run(transactionalPrivilegeHeld(e, held, q), s)
						run(e, fixture.ImplicitError("SELECT 1", fixture.TransactionAborted()))
						run(e, fixture.Exec("ROLLBACK TO restrict_mark", 0))
						run(e, fixture.Exec("ROLLBACK TO restrict_mark", 0))
						acl(e, true, true, independent)
						probe(e, held, "og_object_reader", true, true)
						probe(e, held, "og_object_other", true, true)
						probe(peer, peerHeld, "", true, false)
					}
					run(e, fixture.Exec("RELEASE restrict_mark", 0))
					finish(false)
					for _, commit := range []bool{false, true} {
						run(c, fixture.Exec(reset, 0))
						chain(c, ownerHeld)
						e, held, finish, cleanup := transactionalPrivilegeBlock(t, p, b, c, goTx, ownerHeld)
						defer cleanup()
						run(e, fixture.Exec("SAVEPOINT cascade_mark", 0))
						command(e, held, cascade)
						acl(e, false, false, independent)
						acl(peer, true, true, independent)
						tableACL(e)
						probe(e, held, "og_object_reader", true, true)
						probe(e, held, "og_object_other", independent, true)
						probe(peer, peerHeld, "", true, false)
						for range 2 {
							run(e, fixture.Exec("ROLLBACK TO cascade_mark", 0))
							acl(e, true, true, independent)
							probe(e, held, "og_object_other", true, true)
						}
						run(e, fixture.Exec("RELEASE cascade_mark", 0))
						run(e, fixture.Exec("SAVEPOINT released_mark", 0))
						command(e, held, cascade)
						run(e, fixture.Exec("RELEASE released_mark", 0))
						acl(e, false, false, independent)
						finish(commit)
						acl(c, !commit, !commit, independent)
						probe(c, ownerHeld, "og_object_reader", true, false)
						probe(c, ownerHeld, "og_object_other", !commit || independent, false)
						probe(peer, peerHeld, "", !commit || independent, false)
						tableACL(c)
					}
					if independent {
						// Restore A's option and a real A-origin edge before revoking that
						// ordinary edge as A; the independent owner-origin must survive.
						chain(c, ownerHeld)
						acl(c, true, true, true)
						e, held, finish, cleanup := transactionalPrivilegeBlock(t, p, b, c, goTx, ownerHeld)
						defer cleanup()
						run(e, fixture.Exec("SET ROLE og_object_reader", 0))
						command(e, held, ordinary)
						run(e, fixture.Exec("RESET ROLE", 0))
						acl(e, true, false, true)
						probe(e, held, "og_object_other", true, true)
						probe(peer, peerHeld, "", true, false)
						finish(true)
						acl(c, true, false, true)
						probe(peer, peerHeld, "", true, false)
						tableACL(c)
					}
				})
			})
		}
	}
}
