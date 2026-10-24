package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleTransactionalLifecycle(t *testing.T) {
	for _, alias := range []struct {
		create, alter, drop, initial string
		login                        bool
		initialMember                bool
	}{
		{"ROLE", "ROLE", "ROLE", "IN ROLE rt_parent", false, false},
		{"USER", "USER", "USER", "IN GROUP rt_parent", true, false},
		{"GROUP", "ROLE", "GROUP", "ROLE rt_member", false, true},
		{"ROLE", "ROLE", "ROLE", "ADMIN rt_member", false, true},
		{"GROUP", "ROLE", "GROUP", "USER rt_member", false, true},
	} {
		t.Run(alias.create+"/"+alias.initial, func(t *testing.T) {
			roleTransactionCases(t, []string{"rt_new", "rt_parent", "rt_member", "rt_drop"}, []string{"rt_owned"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held bool, mode string, commit bool) {
				run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
				for _, q := range []string{"CREATE ROLE rt_parent", "CREATE USER rt_member", "CREATE ROLE rt_drop", "CREATE TABLE rt_sentinel(i INT)", "INSERT INTO rt_sentinel VALUES(7)"} {
					count := int64(0)
					if q == "INSERT INTO rt_sentinel VALUES(7)" {
						count = 1
					}
					run(c, fixture.Exec(q, count))
				}
				var old string
				if err := c.QueryRowContext(p.Ctx, "SELECT oid::text FROM pg_catalog.pg_roles WHERE rolname='rt_drop'").Scan(&old); err != nil {
					t.Fatal(err)
				}
				roleTransactionValidOID(t, old)
				e, finish := roleTransactionBegin(t, p, b, c, mode)
				utility := func(q string) { roleTransactionStep(t, p, b, e, held, fixture.Exec(q, 0)) }
				utility("CREATE " + alias.create + " rt_new " + alias.initial)
				run(e, fixture.Select("SELECT rolcanlogin,rolinherit FROM pg_catalog.pg_roles WHERE rolname='rt_new'", []string{"rolcanlogin", "rolinherit"}, []string{"BOOL", "BOOL"}, [][]any{{alias.login, true}}))
				if alias.initialMember {
					run(e, fixture.Select("SELECT pg_has_role('rt_member','rt_new','MEMBER') AS member", []string{"member"}, []string{"BOOL"}, [][]any{{true}}))
				} else {
					run(e, fixture.Select("SELECT pg_has_role('rt_new','rt_parent','MEMBER') AS member", []string{"member"}, []string{"BOOL"}, [][]any{{true}}))
				}
				utility("ALTER " + alias.alter + " rt_new LOGIN CREATEDB CONNECTION LIMIT 3")
				utility("ALTER " + alias.alter + " rt_new NOINHERIT")
				run(e, fixture.Select("SELECT rolcanlogin,rolcreatedb,rolinherit,rolconnlimit FROM pg_catalog.pg_roles WHERE rolname='rt_new'", []string{"rolcanlogin", "rolcreatedb", "rolinherit", "rolconnlimit"}, []string{"BOOL", "BOOL", "BOOL", "INT4"}, [][]any{{true, true, false, int64(3)}}))
				utility("ALTER GROUP rt_parent ADD USER rt_member")
				utility("ALTER GROUP rt_parent DROP USER rt_member")
				run(e, fixture.Select("SELECT pg_has_role('rt_member','rt_parent','MEMBER') AS member", []string{"member"}, []string{"BOOL"}, [][]any{{false}}))
				utility("DROP " + alias.drop + " rt_drop")
				run(e, roleTransactionExists("rt_drop", false))
				if mode != "autocommit" {
					run(b.DB, roleTransactionExists("rt_new", false))
					run(b.DB, roleTransactionExists("rt_drop", true))
					run(b.DB, fixture.ErrorDiagnostic("SET ROLE rt_new", fixture.ControlsReferenceDiagnostic("22023", `role "rt_new" does not exist`)))
				}
				utility("CREATE SCHEMA rt_owned AUTHORIZATION rt_new")
				utility("SET ROLE rt_new")
				utility("CREATE TABLE rt_owned.items(i INT)")
				run(e, fixture.Exec("INSERT INTO rt_owned.items VALUES(8)", 1))
				run(e, fixture.Select("SELECT i FROM rt_owned.items", []string{"i"}, []string{"INT4"}, [][]any{{int64(8)}}))
				utility("RESET ROLE")
				run(e, fixture.Exec("INSERT INTO rt_sentinel VALUES(9)", 1))
				finish(commit)
				run(b.DB, roleTransactionExists("rt_new", commit))
				run(b.DB, roleTransactionExists("rt_drop", !commit))
				rows := [][]any{{int64(7)}}
				if commit {
					rows = append(rows, []any{int64(9)})
				}
				run(b.DB, fixture.Select("SELECT i FROM rt_sentinel ORDER BY i", []string{"i"}, []string{"INT4"}, rows))
				if commit {
					run(b.DB, fixture.Select("SELECT i FROM rt_owned.items", []string{"i"}, []string{"INT4"}, [][]any{{int64(8)}}))
					run(c, fixture.Exec("DROP SCHEMA rt_owned CASCADE", 0))
				} else {
					var restored string
					if err := c.QueryRowContext(p.Ctx, "SELECT oid::text FROM pg_catalog.pg_roles WHERE rolname='rt_drop'").Scan(&restored); err != nil || restored != old {
						t.Fatal("rollback identity", old, restored, err)
					}
					missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "rt_owned.items" does not exist`)
					missing.Position = fixture.Position(15)
					run(c, fixture.ErrorDiagnostic("SELECT i FROM rt_owned.items", missing))
				}
			})
		})
	}
}
