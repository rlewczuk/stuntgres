package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Bind each already-held statement once per Go transaction, not once per step.
// SQL BEGIN keeps the original connection-bound handles.
func transactionalPrivilegeBlock(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, goTx bool, held map[string]*sql.Stmt) (fixture.Executor, map[string]*sql.Stmt, func(bool), func()) {
	t.Helper()
	var e fixture.Executor = c
	var tx *sql.Tx
	bound := held
	if goTx {
		var err error
		tx, err = c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		e = tx
		bound = make(map[string]*sql.Stmt, len(held))
		for query, statement := range held {
			bound[query] = tx.StmtContext(p.Ctx, statement)
		}
	} else {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
	}
	ended := false
	finish := func(commit bool) {
		if tx != nil {
			var err error
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
		} else {
			query := "ROLLBACK"
			if commit {
				query = "COMMIT"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(query, 0))
		}
		ended = true
	}
	cleanup := func() {
		if tx != nil {
			tx.Rollback()
			for _, statement := range bound {
				statement.Close()
			}
		} else if !ended {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			c.ExecContext(ctx, "ROLLBACK")
		}
	}
	return e, bound, finish, cleanup
}

func transactionalPrivilegeHeld(e fixture.Executor, held map[string]*sql.Stmt, query string) fixture.Executor {
	if statement := held[query]; statement != nil {
		return fixture.HeldStatement{Executor: e, Statement: statement, SQL: query}
	}
	return e
}

func TestReferenceTransactionalPrivilegesRoutineKinds(t *testing.T) {
	type routine struct {
		signature, query, noun, name string
		rows                         [][]any
	}
	sum := routine{"pg_catalog.sum(integer)", "SELECT sum(x) AS v FROM (VALUES(1),(2)) AS s(x)", "aggregate", "sum", [][]any{{int64(3)}}}
	window := routine{"pg_catalog.row_number()", "SELECT row_number() OVER(ORDER BY x) AS v FROM (VALUES(1),(2)) AS s(x)", "function", "row_number", [][]any{{int64(1)}, {int64(2)}}}
	for _, mode := range []string{"sum", "window", "FUNCTIONS", "ROUTINES", "PROCEDURES"} {
		for _, goTx := range []bool{false, true} {
			control := "sql"
			if goTx {
				control = "go"
			}
			t.Run(mode+"/"+control, func(t *testing.T) {
				objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ func(fixture.Step), prepared bool) {
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
					defer objectGrantRoutineCleanup(t, p, c, []string{sum.signature, window.signature})
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
						defer cancel()
						c.ExecContext(ctx, "ROLLBACK")
						c.ExecContext(ctx, "RESET ROLE")
						peer.ExecContext(ctx, "RESET ROLE")
						if _, err := c.ExecContext(ctx, "REVOKE EXECUTE ON ALL ROUTINES IN SCHEMA pg_catalog FROM og_object_reader,og_object_other CASCADE"); err != nil {
							t.Error("routine ACL cleanup", err)
						}
					}()
					run(c, fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader,og_object_other", 0))
					run(c, fixture.Exec("REVOKE EXECUTE ON FUNCTION "+sum.signature+","+window.signature+" FROM PUBLIC", 0))
					selected := []routine{sum, window}
					if mode == "sum" {
						selected = []routine{sum}
					} else if mode == "window" {
						selected = []routine{window}
					}
					target := "EXECUTE ON ALL " + mode + " IN SCHEMA pg_catalog"
					if mode == "sum" || mode == "window" {
						target = "EXECUTE ON FUNCTION " + selected[0].signature
					}
					grant, revoke := "GRANT "+target+" TO og_object_reader", "REVOKE "+target+" FROM og_object_reader CASCADE"
					named := "EXECUTE ON FUNCTION " + sum.signature + "," + window.signature
					namedGrant, namedRevoke := "GRANT "+named+" TO og_object_reader", "REVOKE "+named+" FROM og_object_reader CASCADE"
					run(c, fixture.Exec(namedGrant, 0))
					ownerHeld, peerHeld := make(map[string]*sql.Stmt), make(map[string]*sql.Stmt)
					defer func() {
						for _, s := range ownerHeld {
							s.Close()
						}
						for _, s := range peerHeld {
							s.Close()
						}
					}()
					run(c, fixture.Exec("SET ROLE og_object_reader", 0))
					run(peer, fixture.Exec("SET ROLE og_object_reader", 0))
					for _, r := range selected {
						ownerHeld[r.query], err = c.PrepareContext(p.Ctx, r.query)
						if err != nil {
							t.Fatal("owner held consumer", err)
						}
						peerHeld[r.query], err = peer.PrepareContext(p.Ctx, r.query)
						if err != nil {
							t.Fatal("peer held consumer", err)
						}
					}
					run(c, fixture.Exec("RESET ROLE", 0))
					if prepared {
						for _, q := range []string{grant, revoke} {
							ownerHeld[q], err = c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
						}
					}
					command := func(e fixture.Executor, held map[string]*sql.Stmt, q string) {
						run(transactionalPrivilegeHeld(e, held, q), fixture.Exec(q, 0))
					}
					probe := func(e fixture.Executor, held map[string]*sql.Stmt, allowed, owner, inBlock bool) {
						for _, r := range selected {
							for _, heldConsumer := range []bool{false, true} {
								if owner && inBlock {
									run(e, fixture.Exec("SAVEPOINT consumer_probe", 0))
								}
								if owner {
									run(e, fixture.Exec("SET ROLE og_object_reader", 0))
								}
								var s fixture.Step
								if allowed {
									s = fixture.Select(r.query, []string{"v"}, []string{"INT8"}, r.rows)
									s.Bag = len(r.rows) > 1
								} else {
									s = fixture.ImplicitError(r.query, fixture.Diagnostic{Code: "42501", Message: "permission denied for " + r.noun + " " + r.name})
									s.SelectRows = true
									s.ErrorStage = "execute"
								}
								source := e
								if heldConsumer {
									source = transactionalPrivilegeHeld(e, held, r.query)
								}
								run(source, s)
								if owner && inBlock && !allowed {
									run(e, fixture.Exec("ROLLBACK TO consumer_probe", 0))
								} else if owner {
									run(e, fixture.Exec("RESET ROLE", 0))
								}
								if owner && inBlock {
									run(e, fixture.Exec("RELEASE consumer_probe", 0))
								}
							}
						}
					}
					acl := func(e fixture.Executor, allowed bool) {
						// Match the existing routine-expansion control: native pg_proc
						// does not expose proacl. Shared fresh and held aggregate/window
						// consumers prove the effective rights on both backends.
						if !b.PG {
							return
						}
						var count int64
						if allowed {
							count = 1
						}
						for _, r := range selected {
							q := "SELECT count(*) AS v FROM pg_proc p CROSS JOIN LATERAL aclexplode(p.proacl) a JOIN pg_roles g ON g.oid=a.grantor JOIN pg_roles r ON r.oid=a.grantee WHERE p.oid='" + r.signature + "'::regprocedure AND r.rolname='og_object_reader' AND g.oid=p.proowner AND a.privilege_type='EXECUTE' AND NOT a.is_grantable"
							run(e, fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{count}}))
						}
					}
					for _, commit := range []bool{false, true} {
						run(c, fixture.Exec(namedRevoke, 0))
						probe(peer, peerHeld, false, false, false)
						e, held, finish, cleanup := transactionalPrivilegeBlock(t, p, b, c, goTx, ownerHeld)
						defer cleanup()
						command(e, held, grant)
						if mode == "PROCEDURES" {
							probe(e, held, false, true, true)
							acl(e, false)
							probe(peer, peerHeld, false, false, false)
							finish(commit)
							probe(peer, peerHeld, false, false, false)
							acl(c, false)
							run(c, fixture.Exec(namedGrant, 0))
							e, held, finish, cleanup = transactionalPrivilegeBlock(t, p, b, c, goTx, ownerHeld)
							defer cleanup()
							run(e, fixture.Exec("SAVEPOINT exclusion_mark", 0))
							command(e, held, revoke)
							probe(e, held, true, true, true)
							acl(e, true)
							probe(peer, peerHeld, true, false, false)
							run(e, fixture.Exec("ROLLBACK TO exclusion_mark", 0))
							run(e, fixture.Exec("RELEASE exclusion_mark", 0))
							finish(commit)
							probe(peer, peerHeld, true, false, false)
							acl(c, true)
							continue
						}
						probe(e, held, true, true, true)
						probe(peer, peerHeld, false, false, false)
						acl(e, true)
						acl(peer, false)
						run(e, fixture.Exec("SAVEPOINT acl_mark", 0))
						command(e, held, revoke)
						probe(e, held, false, true, true)
						run(e, fixture.Exec("ROLLBACK TO acl_mark", 0))
						run(e, fixture.Exec("ROLLBACK TO acl_mark", 0))
						probe(e, held, true, true, true)
						run(e, fixture.Exec("RELEASE acl_mark", 0))
						finish(commit)
						probe(peer, peerHeld, commit, false, false)
						probe(c, ownerHeld, commit, true, false)
					}
					if mode == "PROCEDURES" {
						return
					}
					for _, commit := range []bool{false, true} {
						e, held, finish, cleanup := transactionalPrivilegeBlock(t, p, b, c, goTx, ownerHeld)
						defer cleanup()
						command(e, held, revoke)
						probe(e, held, false, true, true)
						probe(peer, peerHeld, true, false, false)
						finish(commit)
						probe(peer, peerHeld, !commit, false, false)
						probe(c, ownerHeld, !commit, true, false)
						acl(c, !commit)
					}
				})
			})
		}
	}
}
