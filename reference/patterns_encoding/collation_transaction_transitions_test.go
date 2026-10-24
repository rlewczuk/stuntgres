package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCollationTransactionTransitions(t *testing.T) {
	for _, action := range []struct{ name, query string }{{"rename", `ALTER COLLATION tx_origin.base RENAME TO renamed`}, {"schema", `ALTER COLLATION tx_origin.base SET SCHEMA tx_dest`}, {"owner", `ALTER COLLATION tx_origin.base OWNER TO tx_newowner`}, {"version", `ALTER COLLATION tx_origin.base REFRESH VERSION`}, {"drop", `DROP COLLATION tx_origin.base`}} {
		for _, route := range []string{"autocommit", "sql", "go"} {
			for _, held := range []bool{false, true} {
				for _, commit := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/held=%v/commit=%v", action.name, route, held, commit), func(t *testing.T) {
						fixture.RoleReferenceCleanup(t, []string{"tx_newowner"})
						fixture.SchemaReferenceCleanup(t, "tx_origin")
						fixture.SchemaReferenceCleanup(t, "tx_dest")
						p := fixture.NewScenario(t)
						for _, b := range p.Backends() {
							t.Run(b.Name, func(t *testing.T) {
								c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
								peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
								defer c.ExecContext(p.Ctx, "ROLLBACK")
								runOn := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
								for _, q := range []string{"CREATE ROLE tx_newowner", "CREATE SCHEMA tx_origin AUTHORIZATION tx_newowner", "CREATE SCHEMA tx_dest AUTHORIZATION tx_newowner", `CREATE COLLATION tx_origin.base (PROVIDER=builtin,LOCALE='C',VERSION='stale')`} {
									runOn(c, fixture.Exec(q, 0))
								}
								var oid int64
								if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_collation WHERE collname='base'").Scan(&oid); err != nil || oid <= 0 {
									t.Fatal(oid, err)
								}
								stmt, err := c.PrepareContext(p.Ctx, action.query)
								if err != nil {
									t.Fatal("quiet held Prepare", err)
								}
								defer stmt.Close()
								var x fixture.Executor = c
								var tx *sql.Tx
								if route == "go" {
									tx, err = c.BeginTx(p.Ctx, nil)
									if err != nil {
										t.Fatal(err)
									}
									defer tx.Rollback()
									x = tx
								} else if route == "sql" {
									runOn(c, fixture.Exec("BEGIN", 0))
								}
								run := func(s fixture.Step) {
									var executor fixture.Executor = x
									if held && s.Query == action.query {
										st := stmt
										if tx != nil {
											st = tx.StmtContext(p.Ctx, stmt)
											defer st.Close()
										}
										executor = fixture.HeldStatement{Executor: x, Statement: st, SQL: action.query}
									}
									runOn(executor, s)
								}
								state := func(executor fixture.Executor, changed bool) {
									if changed && action.name == "drop" {
										runOn(executor, fixture.Select("SELECT count(*) AS n FROM pg_collation WHERE oid=$1", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, oid))
										return
									}
									name, namespace, owner := "base", "tx_origin", false
									if changed {
										if action.name == "rename" {
											name = "renamed"
										}
										if action.name == "schema" {
											namespace = "tx_dest"
										}
										if action.name == "owner" {
											owner = true
										}
									}
									runOn(executor, fixture.Select("SELECT o.oid::bigint,o.collname,n.nspname,o.collowner=(SELECT oid FROM pg_roles WHERE rolname='tx_newowner') AS transferred FROM pg_collation o JOIN pg_namespace n ON o.collnamespace=n.oid WHERE o.oid=$1", []string{"oid", "collname", "nspname", "transferred"}, []string{"INT8", "NAME", "NAME", "BOOL"}, [][]any{{oid, name, namespace, owner}}, oid))
									version := "stale"
									if changed && action.name == "version" {
										version = "1"
									}
									runOn(executor, fixture.Select("SELECT collversion,pg_collation_actual_version(oid) AS actual FROM pg_collation WHERE oid=$1", []string{"collversion", "actual"}, []string{"TEXT", "TEXT"}, [][]any{{version, "1"}}, oid))
								}
								state(x, false)
								if route != "autocommit" {
									run(fixture.Exec("SAVEPOINT before_action", 0))
								}
								run(fixture.Exec(action.query, 0))
								state(x, true)
								if route != "autocommit" {
									state(peer, false)
									run(fixture.Exec("ROLLBACK TO before_action", 0))
									state(x, false)
									run(fixture.Exec(action.query, 0))
									run(fixture.Exec("RELEASE before_action", 0))
									state(x, true)
									if tx != nil {
										if commit {
											err = tx.Commit()
										} else {
											err = tx.Rollback()
										}
										if err != nil {
											t.Fatal(err)
										}
									} else {
										end := "ROLLBACK"
										if commit {
											end = "COMMIT"
										}
										run(fixture.Exec(end, 0))
									}
									state(peer, commit)
								} else {
									state(peer, true)
								}
							})
						}
					})
				}
			}
		}
	}
}
