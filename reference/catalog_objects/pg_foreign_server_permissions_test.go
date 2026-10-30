package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGForeignServerPermissions(t *testing.T) {
	pgfsScenario(t, func(c *pgfsCase) {
		c.wrapper()
		c.exec(pgfsSeed, "CREATE ROLE pgfs_reader")
		oid := c.oid("pgfs_server")
		reader, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil { c.t.Fatal(err) }
		defer reader.Close()
		held := c.hold(reader, pgfsQuery)
		defer held.Statement.Close()
		c.run(reader, fixture.Exec("SET ROLE pgfs_reader", 0))
		defer reader.ExecContext(context.Background(), "RESET ROLE")
		initial := [][]any{{oid, "pgfs_server", c.owner, c.wrapperOID(), "", "18", nil, "{password=private=value,empty=,remote=é=🙂}"}}
		for _, e := range []fixture.Executor{reader, held} { c.raw(e, "pgfs_server", initial) }
		info := func(visible bool) {
			var want [][]any
			if visible { want = [][]any{{"pgfs_server"}} }
			c.both(reader, fixture.Select("SELECT foreign_server_name FROM information_schema.foreign_servers WHERE foreign_server_name='pgfs_server'", []string{"foreign_server_name"}, []string{"NAME"}, want))
			var options [][]any
			if visible { options = [][]any{{"empty", ""}, {"password", "private=value"}, {"remote", "é=🙂"}} }
			c.both(reader, fixture.Select("SELECT option_name,option_value FROM information_schema.foreign_server_options WHERE foreign_server_name='pgfs_server' ORDER BY option_name", []string{"option_name", "option_value"}, []string{"NAME", "VARCHAR"}, options))
		}
		info(false)
		c.exec("GRANT USAGE ON FOREIGN SERVER pgfs_server TO pgfs_reader WITH GRANT OPTION")
		info(true)
		// Check actual grantor/grantee relationships, not generated role numbers
		// or an incidental ACL item ordering.
		c.both(reader, fixture.Select("SELECT r.rolname,a.grantor=w.srvowner AS owner_grantor,a.privilege_type,a.is_grantable FROM pg_catalog.pg_foreign_server w CROSS JOIN LATERAL aclexplode(w.srvacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE w.srvname='pgfs_server' AND r.rolname='pgfs_reader'", []string{"rolname", "owner_grantor", "privilege_type", "is_grantable"}, []string{"NAME", "BOOL", "TEXT", "BOOL"}, [][]any{{"pgfs_reader", true, "USAGE", true}}))
		c.options(reader, "pgfs_server", []string{"password=private=value", "empty=", "remote=é=🙂"})
		c.exec("REVOKE USAGE ON FOREIGN SERVER pgfs_server FROM pgfs_reader")
		info(false)
		c.both(reader, fixture.Select("SELECT srvacl IS NULL AS implicit,srvoptions[1] AS secret FROM pg_catalog.pg_foreign_server WHERE srvname='pgfs_server'", []string{"implicit", "secret"}, []string{"BOOL", "TEXT"}, [][]any{{false, "password=private=value"}}))
		c.both(reader, fixture.Select("SELECT a.grantee=w.srvowner AS owner_grantee,a.grantor=w.srvowner AS owner_grantor,a.privilege_type,a.is_grantable FROM pg_catalog.pg_foreign_server w CROSS JOIN LATERAL aclexplode(w.srvacl) a WHERE w.srvname='pgfs_server'", []string{"owner_grantee", "owner_grantor", "privilege_type", "is_grantable"}, []string{"BOOL", "BOOL", "TEXT", "BOOL"}, [][]any{{true, true, "USAGE", false}}))
		c.exec("GRANT USAGE ON FOREIGN SERVER pgfs_server TO pgfs_reader")
		for _, prepared := range []bool{false, true} {
			c.error(reader, "ALTER SERVER pgfs_server VERSION 'denied'", "42501", "must be owner of foreign server pgfs_server", prepared)
			c.error(reader, "DROP SERVER pgfs_server", "42501", "must be owner of foreign server pgfs_server", prepared)
			c.error(reader, "CREATE SERVER pgfs_bad FOREIGN DATA WRAPPER pgfs_wrapper", "42501", "permission denied for foreign-data wrapper pgfs_wrapper", prepared)
		}
		mutation := "ALTER SERVER pgfs_server VERSION 'denied'"
		heldMutation := c.hold(c.conn, mutation)
		// The reader's real held utility handle is admitted by Prepare and denied at Execute.
		heldReaderMutation := c.hold(reader, mutation)
		defer heldReaderMutation.Statement.Close()
		c.error(heldReaderMutation, mutation, "42501", "must be owner of foreign server pgfs_server", false)
		c.run(c.conn, fixture.Exec("SET ROLE pgfs_reader", 0))
		c.error(heldMutation, mutation, "42501", "must be owner of foreign server pgfs_server", false)
		c.run(c.conn, fixture.Exec("RESET ROLE", 0))
		c.exec("REVOKE USAGE ON FOREIGN SERVER pgfs_server FROM pgfs_reader", "GRANT USAGE ON FOREIGN DATA WRAPPER pgfs_wrapper TO pgfs_reader")
		info(false)
		c.run(reader, fixture.Prepared(fixture.Exec("CREATE SERVER pgfs_bad FOREIGN DATA WRAPPER pgfs_wrapper", 0)))
		c.run(reader, fixture.Exec("DROP SERVER pgfs_bad", 0))
		c.error(reader, "ALTER SERVER pgfs_server VERSION 'denied'", "42501", "must be owner of foreign server pgfs_server", false)
		c.exec("CREATE ROLE pgfs_member", "GRANT pgfs_member TO pgfs_reader", "GRANT USAGE ON FOREIGN SERVER pgfs_server TO pgfs_member")
		info(true)
		c.exec("REVOKE USAGE ON FOREIGN SERVER pgfs_server FROM pgfs_member", "GRANT USAGE ON FOREIGN SERVER pgfs_server TO PUBLIC")
		info(true)
		c.exec("REVOKE USAGE ON FOREIGN SERVER pgfs_server FROM PUBLIC", "GRANT USAGE ON FOREIGN SERVER pgfs_server TO pgfs_reader WITH GRANT OPTION", "REVOKE GRANT OPTION FOR USAGE ON FOREIGN SERVER pgfs_server FROM pgfs_reader")
		c.both(reader, fixture.Select("SELECT a.grantor=s.srvowner AS owner_grantor,a.privilege_type,a.is_grantable FROM pg_catalog.pg_foreign_server s CROSS JOIN LATERAL aclexplode(s.srvacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE s.srvname='pgfs_server' AND r.rolname='pgfs_reader'", []string{"owner_grantor", "privilege_type", "is_grantable"}, []string{"BOOL", "TEXT", "BOOL"}, [][]any{{true, "USAGE", false}}))
		// A catalog revoke must be rechecked by an already physically prepared
		// handle, even if its predicate selects no row. USAGE cannot override it.
		c.exec("GRANT USAGE ON FOREIGN SERVER pgfs_server TO pgfs_reader", "REVOKE SELECT ON pg_catalog.pg_foreign_server FROM PUBLIC")
		for _, name := range []string{"pgfs_server", "absent"} {
			step := fixture.ImplicitError(pgfsQuery, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_foreign_server"}, name)
			step.SelectRows, step.ErrorStage = true, "execute"
			c.run(held, step)
			c.both(reader, step)
		}
		c.exec("GRANT SELECT(srvname) ON pg_catalog.pg_foreign_server TO pgfs_reader")
		c.both(reader, fixture.Select("SELECT srvname FROM pg_catalog.pg_foreign_server WHERE srvname=$1", []string{"srvname"}, []string{"NAME"}, [][]any{{"pgfs_server"}}, "pgfs_server"))
		step := fixture.ImplicitError("SELECT srvoptions FROM pg_catalog.pg_foreign_server WHERE srvname='pgfs_server'", fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_foreign_server"})
		step.SelectRows, step.ErrorStage = true, "execute"
		c.both(reader, step)
		c.exec("REVOKE SELECT(srvname) ON pg_catalog.pg_foreign_server FROM pgfs_reader", "GRANT SELECT ON pg_catalog.pg_foreign_server TO PUBLIC")
		c.options(reader, "pgfs_server", []string{"password=private=value", "empty=", "remote=é=🙂"})
		c.shape(held, "pgfs_server", true)
		c.run(held, fixture.Select("SELECT * FROM pg_catalog.pg_foreign_server WHERE srvname=$1", pgfsColumns, pgfsTypes, nil, "absent"))
		c.options(held, "pgfs_server", []string{"password=private=value", "empty=", "remote=é=🙂"})
		c.exec("REVOKE USAGE ON FOREIGN SERVER pgfs_server FROM pgfs_reader", "REVOKE ALL ON FOREIGN SERVER pgfs_server FROM CURRENT_USER")
		c.both(reader, fixture.Select("SELECT srvacl IS NULL AS implicit,srvacl::text AS acl FROM pg_catalog.pg_foreign_server WHERE srvname='pgfs_server'", []string{"implicit", "acl"}, []string{"BOOL", "TEXT"}, [][]any{{false, "{}"}}))
		c.exec("GRANT USAGE ON FOREIGN SERVER pgfs_server TO CURRENT_USER")
		var ownerName string
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT current_user").Scan(&ownerName); err != nil { c.t.Fatal(err) }
		c.both(reader, fixture.Select("SELECT srvacl[1]::text AS owner_acl,array_length(srvacl,1) AS length FROM pg_catalog.pg_foreign_server WHERE srvname='pgfs_server'", []string{"owner_acl", "length"}, []string{"TEXT", "INT4"}, [][]any{{ownerName+"=U/"+ownerName, int64(1)}}))
		c.exec("CREATE ROLE pgfs_newowner", "GRANT USAGE ON FOREIGN DATA WRAPPER pgfs_wrapper TO pgfs_newowner", "GRANT USAGE ON FOREIGN SERVER pgfs_server TO pgfs_reader WITH GRANT OPTION", "ALTER SERVER pgfs_server OWNER TO pgfs_newowner")
		var successor int64
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='pgfs_newowner'").Scan(&successor); err != nil { c.t.Fatal(err) }
		c.identity("pgfs_server", oid, successor, c.wrapperOID())
		c.both(reader, fixture.Select("SELECT r.rolname,a.grantor=s.srvowner AS owner_grantor,a.privilege_type,a.is_grantable FROM pg_catalog.pg_foreign_server s CROSS JOIN LATERAL aclexplode(s.srvacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE s.srvname='pgfs_server' ORDER BY r.rolname", []string{"rolname", "owner_grantor", "privilege_type", "is_grantable"}, []string{"NAME", "BOOL", "TEXT", "BOOL"}, [][]any{{"pgfs_newowner", true, "USAGE", false}, {"pgfs_reader", true, "USAGE", true}}))
		c.options(held, "pgfs_server", []string{"password=private=value", "empty=", "remote=é=🙂"})
	})
}

