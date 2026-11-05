package reference_test

import "stuntgres/reference/fixture"

// This body is called by the public Tablespace definition, so its exact selector
// includes all shared-authority, private visibility and constructor witnesses.
func databaseAlterSharedTablespaceACL(c *databaseCreateCase) {
	defer databaseAlterBuiltinACLCleanup(c)()
	c.create("da_source", "OWNER da_owner", false)
	c.create("da_acl_observer", "", false)
	oid := c.scalar("SELECT oid::text FROM pg_database WHERE datname='da_source'")
	peer, raw, rec := c.pool("da_acl_observer", "")
	defer databaseAlterClose(c, peer, raw)
	conn, err := peer.Conn(c.p.Ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	defer conn.Close()
	databaseAlterRun(c, conn, rec, fixture.Exec("SET ROLE da_owner", 0))
	query := "SELECT has_tablespace_privilege('da_owner','pg_default','CREATE') AS allowed"
	allowed := func(value bool) fixture.Step {
		return fixture.Select(query, []string{"allowed"}, []string{"BOOL"}, [][]any{{value}})
	}
	writer := func(value bool) { c.run(allowed(value)) }
	reader := func(value bool) { databaseAlterRun(c, conn, rec, allowed(value)) }
	alter := "ALTER DATABASE da_source SET TABLESPACE pg_default"
	denied := fixture.ErrorDiagnostic(alter, databaseCreateDiagnostic("42501", "permission denied for tablespace pg_default"))
	probe := func(name string, value bool) {
		db, raw, rec := c.pool(name, "da_owner")
		defer databaseAlterClose(c, db, raw)
		databaseAlterRun(c, db, rec, allowed(value))
		step := denied
		if value {
			step = fixture.Exec(alter, 0)
		}
		databaseAlterRun(c, db, rec, fixture.Prepared(step))
	}
	databaseAlterHeld(c, conn, alter, func(held fixture.HeldStatement) {
		writer(false)
		reader(false)
		databaseAlterRun(c, held, rec, denied)
		c.run(fixture.Exec("BEGIN", 0))
		c.run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_default TO da_owner", 0))
		writer(true)
		reader(false)
		databaseAlterRun(c, held, rec, denied)
		c.run(fixture.Exec("ROLLBACK", 0))
		writer(false)
		reader(false)
		c.run(fixture.Exec("BEGIN", 0))
		c.run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_default TO da_owner", 0))
		c.run(fixture.Exec("SAVEPOINT da_acl_mark", 0))
		c.run(fixture.Exec("REVOKE CREATE ON TABLESPACE pg_default FROM da_owner", 0))
		writer(false)
		reader(false)
		c.run(fixture.Exec("ROLLBACK TO SAVEPOINT da_acl_mark", 0))
		writer(true)
		reader(false)
		c.run(fixture.Exec("COMMIT", 0))
		writer(true)
		reader(true)
		databaseAlterRun(c, held, rec, fixture.Exec(alter, 0))
		c.create("da_acl_after_grant", "", false)
		probe("da_acl_after_grant", true)
		c.create("da_acl_clone", "TEMPLATE da_source", true)
		probe("da_acl_clone", true)
		c.run(fixture.Exec("BEGIN", 0))
		c.run(fixture.Exec("REVOKE CREATE ON TABLESPACE pg_default FROM da_owner", 0))
		writer(false)
		reader(true)
		databaseAlterRun(c, held, rec, fixture.Exec(alter, 0))
		c.run(fixture.Exec("ROLLBACK", 0))
		writer(true)
		reader(true)
		c.run(fixture.Exec("BEGIN", 0))
		c.run(fixture.Exec("REVOKE CREATE ON TABLESPACE pg_default FROM da_owner", 0))
		writer(false)
		reader(true)
		c.run(fixture.Exec("COMMIT", 0))
		writer(false)
		reader(false)
		databaseAlterRun(c, held, rec, denied)
		probe("da_acl_after_grant", false)
		probe("da_acl_clone", false)
	})
	databaseAlterFlags(c, "da_source", false, true, -1)
	if got := c.scalar("SELECT oid::text FROM pg_database WHERE datname='da_source'"); got != oid {
		c.t.Fatalf("tablespace ACL publication changed database identity %s -> %s", oid, got)
	}
}
