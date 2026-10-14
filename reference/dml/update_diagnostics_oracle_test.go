package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceUpdateCoreColumnPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"update_acl_role"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
		// Explicit schema recreation has no default PostgreSQL PUBLIC USAGE.
		// Match the native namespace baseline without granting any table access.
		if backend.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, execute := range []func(fixture.Step){run} {
			execute(fixture.Exec("CREATE ROLE update_acl_role", 0))
			execute(fixture.Exec("CREATE TABLE t(a INT,b INT,c INT)", 0))
			execute(fixture.Exec("INSERT INTO t VALUES(1,2,3)", 1))
			execute(fixture.Exec("SET SESSION AUTHORIZATION update_acl_role", 0))
			execute(fixture.ImplicitError("UPDATE t SET a=7", fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}))
			execute(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("GRANT UPDATE(a) ON t TO update_acl_role", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION update_acl_role", 0))
		run(fixture.Exec("UPDATE t SET a=7", 1))
		for _, q := range []string{"UPDATE t SET a=b", "UPDATE t SET a=8 WHERE c=3", "UPDATE t SET b=8"} {
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"}))
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(a,b) ON t TO update_acl_role", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION update_acl_role", 0))
		run(fixture.Exec("UPDATE t SET a=b WHERE a=7", 1))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT a,b,c FROM t", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(2), int64(3)}}))
	}
}

func TestReferenceUpdateCoreNotNullDetailColumnAuthority(t *testing.T) {
	for _, mode := range []string{"update_only", "select_readable", "all_column_select", "table_select", "modified_readable", "owner", "superuser"} {
		for _, prepared := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				t.Run(mode+map[bool]string{false: "/direct", true: "/prepared"}[prepared]+map[bool]string{false: "/implicit", true: "/explicit"}[explicit], func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"nn_acl_actor", "nn_acl_owner"})
					p := fixture.NewScenario(t)
					ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
					defer cancel()
					for _, backend := range p.Backends() {
						conn, err := backend.DB.Conn(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						defer func() {
							cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
							defer stop()
							for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION"} {
								if _, err := conn.ExecContext(cleanup, q); err != nil {
									t.Error("column detail fixture cleanup", q, err)
								}
							}
						}()
						run := func(step fixture.Step) { fixture.RunStep(t, ctx, conn, backend.Recorder, step) }
						for _, q := range []string{
							"CREATE ROLE nn_acl_owner LOGIN", "CREATE ROLE nn_acl_actor LOGIN",
							"SET SESSION AUTHORIZATION nn_acl_owner",
							"CREATE TABLE t(n INT NOT NULL,secret TEXT,readable TEXT)",
							"INSERT INTO t VALUES(1,'private','visible')",
							"RESET SESSION AUTHORIZATION",
							"GRANT UPDATE(n) ON t TO nn_acl_actor",
						} {
							n := int64(0)
							if q == "INSERT INTO t VALUES(1,'private','visible')" {
								n = 1
							}
							if q == "CREATE TABLE t(n INT NOT NULL,secret TEXT,readable TEXT)" {
								fixture.WithPublicSchemaOwner(t, p, backend, "nn_acl_owner", func() { run(fixture.Exec(q, n)) })
							} else {
								run(fixture.Exec(q, n))
							}
						}
						detail := "Failing row contains (n) = (null)."
						switch mode {
						case "select_readable":
							run(fixture.Exec("GRANT SELECT(readable) ON t TO nn_acl_actor", 0))
							detail = "Failing row contains (n, readable) = (null, visible)."
						case "all_column_select":
							run(fixture.Exec("GRANT SELECT(n,secret,readable) ON t TO nn_acl_actor", 0))
							detail = "Failing row contains (n, secret, readable) = (null, private, visible)."
						case "table_select":
							run(fixture.Exec("GRANT SELECT ON t TO nn_acl_actor", 0))
							detail = "Failing row contains (null, private, visible)."
						case "modified_readable":
							run(fixture.Exec("GRANT UPDATE(readable) ON t TO nn_acl_actor", 0))
							detail = "Failing row contains (n, readable) = (null, new)."
						case "owner", "superuser":
							detail = "Failing row contains (null, private, visible)."
						}
						actor := "nn_acl_actor"
						if mode == "owner" {
							actor = "nn_acl_owner"
						}
						if mode != "superuser" {
							run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
						}
						query := "UPDATE t SET n=NULL"
						if prepared {
							query = "UPDATE t SET n=$1"
						}
						if mode == "modified_readable" {
							if prepared {
								query = "UPDATE t SET readable='new',n=$1"
							} else {
								query = "UPDATE t SET readable='new',n=NULL"
							}
						}
						var stmt *sql.Stmt
						if prepared {
							stmt, err = conn.PrepareContext(ctx, query)
							if err != nil {
								t.Fatal("NOT NULL assignment must prepare", err)
							}
							defer stmt.Close()
						}
						repeats := 1
						if prepared {
							repeats = 2
						}
						for range repeats {
							if explicit {
								run(fixture.Exec("BEGIN", 0))
								run(fixture.Exec("UPDATE t SET n=2", 1))
							}
							var result sql.Result
							if prepared {
								result, err = stmt.ExecContext(ctx, nil)
							} else {
								result, err = conn.ExecContext(ctx, query)
							}
							if result != nil {
								t.Fatal("failed UPDATE returned result", result)
							}
							fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{
								Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502",
								Message: `null value in column "n" of relation "t" violates not-null constraint`,
								Detail:  detail, Schema: "public", Table: "t", Column: "n",
							})
							if explicit {
								run(fixture.ImplicitError("UPDATE t SET n=3", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
								run(fixture.Exec("ROLLBACK", 0))
							}
							run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
							run(fixture.Select("SELECT n,secret,readable FROM t", []string{"n", "secret", "readable"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "private", "visible"}}))
							if mode != "superuser" {
								run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
							}
						}
					}
				})
			}
		}
	}
}
