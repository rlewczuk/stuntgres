package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalPrivilegesDefaults(t *testing.T) {
	transactionalPrivilegesDefaultConsumers(t)
	for _, goTx := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("goTx=%v/held=%v/commit=%v", goTx, held, commit), func(t *testing.T) {
					dpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, base func(fixture.Step), _ bool) {
						base(fixture.Exec("CREATE SCHEMA dp_s", 0))
						base(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA dp_s TO dp_other,dp_reader", 0))
						dpTable(base, "dp_s.old", 7)
						q := "ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO dp_reader"
						var utility *sql.Stmt
						if held {
							var err error
							utility, err = c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer utility.Close()
						}
						var e fixture.Executor = c
						finish := func() { base(fixture.Exec(map[bool]string{true: "COMMIT", false: "ROLLBACK"}[commit], 0)) }
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
							source = fixture.HeldStatement{Executor: e, Statement: utility, SQL: q}
						}
						fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.Exec(q, 0))
						run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s GRANT INSERT ON TABLES TO dp_reader", 0))
						dpTable(run, "dp_s.first", 9)
						run(fixture.Exec("ALTER DEFAULT PRIVILEGES IN SCHEMA dp_s REVOKE SELECT ON TABLES FROM dp_reader", 0))
						run(fixture.Exec("SET ROLE dp_reader", 0))
						dpRead(run, "dp_s.first", 9)
						run(fixture.Exec("INSERT INTO dp_s.first VALUES(11)", 1))
						run(fixture.Exec("SAVEPOINT old_probe", 0))
						s := fixture.RoleGrantDenied("SELECT x FROM dp_s.old", "old")
						s.SelectRows = true
						run(s)
						run(fixture.Exec("ROLLBACK TO old_probe", 0))
						run(fixture.Exec("RESET ROLE", 0))
						run(fixture.Exec("SAVEPOINT policy_mark", 0))
						run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM dp_reader", 0))
						dpTable(run, "dp_s.second", 13)
						run(fixture.Exec("SET ROLE dp_reader", 0))
						run(fixture.Exec("SAVEPOINT second_probe", 0))
						s = fixture.RoleGrantDenied("SELECT x FROM dp_s.second", "second")
						s.SelectRows = true
						run(s)
						run(fixture.Exec("ROLLBACK TO second_probe", 0))
						run(fixture.Exec("RESET ROLE", 0))
						run(fixture.Exec("ROLLBACK TO policy_mark", 0))
						run(fixture.Exec("ROLLBACK TO policy_mark", 0))
						run(fixture.Exec("RELEASE policy_mark", 0))
						run(fixture.Exec("SET ROLE dp_other", 0))
						dpTable(run, "dp_s.other_creator", 17)
						run(fixture.Exec("RESET ROLE", 0))
						run(fixture.Exec("SET ROLE dp_reader", 0))
						run(fixture.Exec("SAVEPOINT creator_probe", 0))
						s = fixture.RoleGrantDenied("SELECT x FROM dp_s.other_creator", "other_creator")
						s.SelectRows = true
						run(s)
						run(fixture.Exec("ROLLBACK TO creator_probe", 0))
						run(fixture.Exec("RESET ROLE", 0))
						finish()
						dpTable(base, "dp_s.after", 19)
						fixture.RoleGrantActor(base, "dp_reader", func() {
							if commit {
								dpRead(base, "dp_s.first", 9, 11)
								dpRead(base, "dp_s.after", 19)
							} else {
								fixture.RoleGrantPermissionDenied(base, "SELECT x FROM dp_s.after", "table", "after")
							}
						})
						base(fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='dp_s'::regnamespace AND relname='second'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					})
				})
			}
		}
	}
}
