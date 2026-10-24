package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalSchemaLifecycle(t *testing.T) {
	transactionalSchemaRollbackRoutes(t, true)
	for _, held := range []bool{false, true} {
		for _, goTx := range []bool{false, true} {
			t.Run(fmt.Sprint(held, goTx), func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "tx_schema_a", "tx_schema_b")
				fixture.RoleReferenceCleanup(t, []string{"tx_schema_owner"})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
						fixture.LockExec(t, p.Ctx, c, "CREATE ROLE tx_schema_owner", 0)
						fixture.LockExec(t, p.Ctx, c, "SET search_path TO public", 0)
						var caller, identity, path string
						if err := c.QueryRowContext(p.Ctx, "SELECT current_user,session_user").Scan(&caller, &identity); err != nil {
							t.Fatal(err)
						}
						if err := c.QueryRowContext(p.Ctx, "SHOW search_path").Scan(&path); err != nil {
							t.Fatal(err)
						}
						var e fixture.Executor = c
						if goTx {
							tx, err := c.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							e = tx
						} else {
							fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
							defer c.ExecContext(p.Ctx, "ROLLBACK")
						}
						run := func(s fixture.Step) { transactionalSchemaRun(t, p, b, e, held, s) }
						run(fixture.Exec("CREATE SCHEMA tx_schema_a AUTHORIZATION tx_schema_owner CREATE TABLE embedded(v INT DEFAULT 4)", 0))
						run(fixture.Select("SELECT current_user AS caller,session_user AS session_identity", []string{"caller", "session_identity"}, []string{"NAME", "NAME"}, [][]any{{caller, identity}}))
						run(fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{path}}))
						run(fixture.Exec("CREATE TABLE tx_schema_resolution(v INT)", 0))
						run(fixture.Select("SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relname='tx_schema_resolution'", []string{"nspname"}, []string{"NAME"}, [][]any{{"public"}}))
						run(fixture.Exec("DROP TABLE tx_schema_resolution", 0))
						run(fixture.Exec("CREATE TABLE tx_schema_a.later(v INT)", 0))
						run(fixture.Exec("INSERT INTO tx_schema_a.embedded DEFAULT VALUES", 1))
						run(fixture.Exec("ALTER SCHEMA tx_schema_a RENAME TO tx_schema_b", 0))
						run(fixture.Exec("ALTER SCHEMA tx_schema_b OWNER TO CURRENT_USER", 0))
						run(fixture.Select("SELECT v FROM tx_schema_b.embedded", []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}}))
						run(fixture.Select("SELECT pg_get_userbyid(relowner) AS owner FROM pg_class WHERE oid='tx_schema_b.embedded'::regclass", []string{"owner"}, []string{"NAME"}, [][]any{{"tx_schema_owner"}}))
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname IN ('tx_schema_a','tx_schema_b')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						run(fixture.Exec("SAVEPOINT before_schema_drop", 0))
						run(fixture.Error("DROP SCHEMA tx_schema_b RESTRICT", "2BP01"))
						run(fixture.Exec("ROLLBACK TO before_schema_drop", 0))
						run(fixture.Exec("DROP SCHEMA tx_schema_b CASCADE", 0))
						if goTx {
							if err := e.(interface{ Commit() error }).Commit(); err != nil {
								t.Fatal(err)
							}
						} else {
							run(fixture.Exec("COMMIT", 0))
						}
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname IN ('tx_schema_a','tx_schema_b')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					})
				}
			})
		}
	}
}
