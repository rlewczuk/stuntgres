package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleRenameIdentity(t *testing.T) {
	for _, route := range []string{"direct", "fresh", "held"} {
		for _, alias := range []string{"ROLE", "USER"} {
			t.Run(route+"/"+alias, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"rr_old", "rr_new", "rr_member", "rr_parent"})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RoleAttributesRun(t, p, b, b.DB, false, s) }
						for _, q := range []string{"CREATE ROLE rr_old LOGIN PASSWORD 'role-attributes-fixture'", "CREATE ROLE rr_member", "CREATE ROLE rr_parent", "GRANT rr_old TO rr_member WITH ADMIN TRUE", "GRANT rr_parent TO rr_old", "GRANT USAGE ON SCHEMA public TO rr_old", "CREATE TABLE rr_owned(i INT)", "INSERT INTO rr_owned VALUES(7)", "ALTER TABLE rr_owned OWNER TO rr_old"} {
							count := int64(0)
							if q == "INSERT INTO rr_owned VALUES(7)" {
								count = 1
							}
							run(fixture.Exec(q, count))
						}
						var oid string
						if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::text FROM pg_roles WHERE rolname='rr_old'").Scan(&oid); err != nil {
							t.Fatal(err)
						}
						fixture.RoleTransactionValidOID(t, oid)
						pool := fixture.RoleAttributesPool(t, p, b, "rr_old")
						old, err := pool.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer old.Close()
						identity := "SELECT session_user,current_user"
						heldIdentity, err := old.PrepareContext(p.Ctx, identity)
						if err != nil {
							t.Fatal(err)
						}
						defer heldIdentity.Close()
						retained, err := old.QueryContext(p.Ctx, identity)
						if err != nil {
							t.Fatal(err)
						}
						defer retained.Close()
						columns, err := retained.ColumnTypes()
						if err != nil || len(columns) != 2 {
							t.Fatal(columns, err)
						}
						for _, column := range columns {
							if column.DatabaseTypeName() != "NAME" {
								t.Fatal(column)
							}
						}
						q := "ALTER " + alias + " rr_old RENAME TO rr_new"
						s := fixture.Exec(q, 0)
						switch route {
						case "direct":
							run(s)
						case "fresh":
							run(fixture.Prepared(s))
						case "held":
							fixture.RoleTransactionStep(t, p, b, b.DB, true, s)
						}
						if !retained.Next() {
							t.Fatal(retained.Err())
						}
						var session, current string
						if err := retained.Scan(&session, &current); err != nil || session != "rr_old" || current != "rr_old" {
							t.Fatal("retained identity", session, current, err)
						}
						if retained.Next() || retained.Err() != nil {
							t.Fatal(retained.Err())
						}
						retained.Close()
						fixture.RunStep(t, p.Ctx, old, b.Recorder, fixture.RoleTransactionIdentity("rr_new", "rr_new"))
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: old, Statement: heldIdentity, SQL: identity}, b.Recorder, fixture.RoleTransactionIdentity("rr_new", "rr_new"))
						run(fixture.RoleTransactionExists("rr_old", false))
						run(fixture.Select("SELECT oid::text FROM pg_roles WHERE rolname='rr_new'", []string{"oid"}, []string{"TEXT"}, [][]any{{oid}}))
						run(fixture.Select("SELECT pg_has_role('rr_member','rr_new','MEMBER') AS member,pg_has_role('rr_new','rr_parent','MEMBER') AS parent", []string{"member", "parent"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
						run(fixture.Select("SELECT relowner::text FROM pg_class WHERE relname='rr_owned'", []string{"relowner"}, []string{"TEXT"}, [][]any{{oid}}))
						fixture.RunStep(t, p.Ctx, old, b.Recorder, fixture.Exec("ALTER TABLE rr_owned ADD COLUMN retained INT", 0))
						fresh := fixture.RoleAttributesPool(t, p, b, "rr_new")
						fixture.RoleAttributesRun(t, p, b, fresh, false, fixture.RoleTransactionIdentity("rr_new", "rr_new"))
						fixture.RoleAttributesRun(t, p, b, fresh, false, fixture.Select("SELECT i FROM rr_owned", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
						missing, err := fixture.RoleAttributesPhysical(t, p, b, "rr_old")
						if missing != nil {
							missing.Close()
							t.Fatal("old login remains connectable")
						}
						if err == nil {
							t.Fatal("old login succeeded")
						}
						// Drop the owned object before role cleanup on both success and failure.
						run(fixture.Exec("DROP TABLE rr_owned", 0))
					})
				}
			})
		}
	}
}

func TestReferenceRoleRenameDefaultsBinding(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"rr_default", "rr_default_new"})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				run := func(s fixture.Step) { fixture.RoleAttributesRun(t, p, b, b.DB, prepared, s) }
				run(fixture.Exec("CREATE ROLE rr_default LOGIN PASSWORD 'role-attributes-fixture'", 0))
				run(fixture.Exec("ALTER ROLE rr_default SET application_name='rename-bound'", 0))
				old := fixture.RoleAttributesPool(t, p, b, "rr_default")
				show := fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"rename-bound"}})
				fixture.RoleAttributesRun(t, p, b, old, prepared, show)
				run(fixture.Exec("ALTER USER rr_default RENAME TO rr_default_new", 0))
				fixture.RoleAttributesRun(t, p, b, old, prepared, show)
				fresh := fixture.RoleAttributesPool(t, p, b, "rr_default_new")
				fixture.RoleAttributesRun(t, p, b, fresh, prepared, show)
				run(fixture.Exec("CREATE ROLE rr_default LOGIN PASSWORD 'role-attributes-fixture'", 0))
				recreated := fixture.RoleAttributesPool(t, p, b, "rr_default")
				fixture.RoleAttributesRun(t, p, b, recreated, prepared, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{""}}))
			}
		})
	}
}
