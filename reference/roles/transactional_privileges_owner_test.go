package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func transactionalPrivilegesOwner(t *testing.T) {
	for _, goTx := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("self-owner/goTx=%v/held=%v", goTx, held), func(t *testing.T) {
				fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, base func(fixture.Step), _ bool) {
					base(fixture.Exec("CREATE TABLE t(a INT)", 0))
					base(fixture.Exec("INSERT INTO t VALUES(7)", 1))
					base(fixture.Exec("ALTER TABLE t OWNER TO rg_owner", 0))
					base(fixture.Exec("SET ROLE rg_owner", 0))
					q := "REVOKE SELECT ON t FROM CURRENT_USER"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET ROLE rg_owner", 0))
					want := fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
					for _, commit := range []bool{false, true} {
						var e fixture.Executor = c
						finish := func() { base(fixture.Exec(map[bool]string{false: "ROLLBACK", true: "COMMIT"}[commit], 0)) }
						if goTx {
							tx, err := c.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							e = tx
							finish = func() {
								var err error
								if commit {
									err = tx.Commit()
								} else {
									err = tx.Rollback()
								}
								if err != nil {
									t.Fatal(err)
								}
							}
						} else {
							base(fixture.Exec("BEGIN", 0))
							defer c.ExecContext(p.Ctx, "ROLLBACK")
						}
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
						source := e
						if held {
							source = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
						}
						fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Exec(q, 0))
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, want)
						run(fixture.Exec("SAVEPOINT self_probe", 0))
						run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
						run(fixture.Exec("ROLLBACK TO self_probe", 0))
						// Ordinary use is gone, but implicit owner grant authority remains.
						run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0))
						finish()
						if commit {
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.RoleGrantDenied("SELECT a FROM t", "t"))
						} else {
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, want)
						}
					}
					base(fixture.Exec("GRANT SELECT ON t TO CURRENT_USER", 0))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, want)
					base(fixture.Exec("RESET ROLE", 0))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("RESET ROLE", 0))
				})
			})
		}
	}
}

func transactionalPrivilegesPreparedTransactions(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared-transaction/commit=%v", commit), func(t *testing.T) {
			fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), held bool) {
				const gid = "transactional_privileges_prepared"
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					c.ExecContext(ctx, "ROLLBACK")
					c.ExecContext(ctx, "ROLLBACK PREPARED '"+gid+"'")
					c.ExecContext(ctx, "ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM rg_reader")
				}()
				run(fixture.Exec("CREATE TABLE t(a INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
				q := "GRANT SELECT ON t TO rg_reader"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				run(fixture.Exec("BEGIN", 0))
				var e fixture.Executor = c
				if held {
					e = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				}
				fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
				run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO rg_reader", 0))
				run(fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET ROLE rg_reader", 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.RoleGrantDenied("SELECT a FROM t", "t"))
				end := "ROLLBACK PREPARED"
				if commit {
					end = "COMMIT PREPARED"
				}
				run(fixture.Exec(end+" '"+gid+"'", 0))
				if commit {
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				} else {
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.RoleGrantDenied("SELECT a FROM t", "t"))
				}
				run(fixture.Exec("CREATE TABLE prepared_future(a INT)", 0))
				run(fixture.Exec("INSERT INTO prepared_future VALUES(9)", 1))
				if commit {
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT a FROM prepared_future", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}}))
				} else {
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.RoleGrantDenied("SELECT a FROM prepared_future", "prepared_future"))
				}
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("RESET ROLE", 0))
				run(fixture.Select("SELECT 1 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
}

func transactionalPrivilegesReadOnlyGo(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ func(fixture.Step), held bool) {
		for _, query := range []string{
			"GRANT SELECT ON tx_absent TO tx_absent_role",
			"REVOKE SELECT ON tx_absent FROM tx_absent_role",
			"ALTER DEFAULT PRIVILEGES FOR ROLE tx_absent_role GRANT SELECT ON TABLES TO tx_absent_role",
		} {
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal("syntax-only outside Prepare", err)
			}
			defer stmt.Close()
			tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer tx.Rollback()
				inside, err := tx.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal("syntax-only inside read-only Prepare", err)
				}
				inside.Close()
				var source fixture.Executor = tx
				if held {
					source = fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: query}
				}
				command := "GRANT"
				if query[0] == 'R' {
					command = "REVOKE"
				}
				if query[0] == 'A' {
					command = "ALTER DEFAULT PRIVILEGES"
				}
				s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + command + " in a read-only transaction"})
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, source, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			}()
		}
	})
}
