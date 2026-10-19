package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDefaultACLVisibilityAndScope(t *testing.T) {
	pgDefaultACLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		const creator, reader, observer, newOwner = "dacl18_scope_creator", "dacl18_scope_reader", "dacl18_scope_observer", "dacl18_scope_owner"
		pgDefaultACLRoles(t, p, b, creator, reader, observer, newOwner)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		const schema, renamed = "dacl18_scope_schema", "dacl18_scope_renamed"
		run(fixture.Exec("CREATE SCHEMA "+schema, 0))
		defer func() {
			for _, name := range []string{schema, renamed} {
				if _, err := b.DB.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE"); err != nil {
					t.Error(err)
				}
			}
		}()
		creatorID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_roles WHERE rolname=$1", creator)
		readerID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_roles WHERE rolname=$1", reader)
		newOwnerID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_roles WHERE rolname=$1", newOwner)
		schemaID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_namespace WHERE nspname=$1", schema)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE "+creator+" IN SCHEMA "+schema+" GRANT SELECT ON TABLES TO "+reader+" WITH GRANT OPTION", 0))
		id := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1", creatorID)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE "+creator+" GRANT USAGE ON SEQUENCES TO "+reader, 0))
		globalID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1 AND defaclnamespace=0", creatorID)
		run(fixture.Exec("ALTER DEFAULT PRIVILEGES FOR ROLE "+newOwner+" IN SCHEMA "+schema+" GRANT SELECT ON TABLES TO "+reader, 0))
		otherID := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_default_acl WHERE defaclrole=$1", newOwnerID)
		q := "SELECT * FROM pg_default_acl WHERE defaclrole=$1 OR defaclrole=$2"
		global := []any{globalID, creatorID, int64(0), "S", pgDefaultACLText(t, creator, pgDefaultACLEntry{creatorID, creator, "rwU"}, pgDefaultACLEntry{readerID, reader, "U"})}
		expected := [][]any{
			{id, creatorID, schemaID, "r", pgDefaultACLText(t, creator, pgDefaultACLEntry{readerID, reader, "r*"})},
			global,
			{otherID, newOwnerID, schemaID, "r", pgDefaultACLText(t, newOwner, pgDefaultACLEntry{readerID, reader, "r"})},
		}
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET ROLE")
		actorRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		observe := func(actor string) {
			actorRun(fixture.Exec("SET ROLE "+actor, 0))
			s := pgDefaultACLSelect(q, b.PG, expected, creatorID, newOwnerID)
			actorRun(s)
			actorRun(fixture.Prepared(s))
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, s)
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			actorRun(fixture.Exec("RESET ROLE", 0))
		}
		for _, actor := range []string{creator, reader, observer, newOwner} {
			observe(actor)
		}
		for _, command := range []string{"GRANT " + creator + " TO " + observer + " WITH INHERIT FALSE", "GRANT " + creator + " TO " + observer + " WITH INHERIT TRUE", "ALTER ROLE " + observer + " NOINHERIT", "ALTER ROLE " + observer + " INHERIT", "ALTER SCHEMA " + schema + " RENAME TO " + renamed, "ALTER SCHEMA " + renamed + " OWNER TO " + newOwner, "REASSIGN OWNED BY " + creator + " TO " + newOwner} {
			run(fixture.Exec(command, 0))
			pgDefaultACLRead(t, p, b, q, expected, creatorID, newOwnerID)
			observe(observer)
		}
		run(fixture.Exec("DROP SCHEMA "+renamed+" CASCADE", 0))
		pgDefaultACLRead(t, p, b, q, [][]any{global}, creatorID, newOwnerID)
		run(fixture.Exec("CREATE SCHEMA "+schema, 0))
		fresh := pgDefaultACLOID(t, p, b, "SELECT oid FROM pg_namespace WHERE nspname=$1", schema)
		if fresh == schemaID {
			t.Fatal("recreated schema identity reused")
		}
		pgDefaultACLRead(t, p, b, q, [][]any{global}, creatorID, newOwnerID)
	})
}
