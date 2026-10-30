package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGForeignDataWrapperVisibility(t *testing.T) {
	pgfdwScenario(t, func(c *pgfdwCase) {
		c.exec(pgfdwSeed, "CREATE ROLE pgfdw_reader")
		oid := c.oid("pgfdw_wrapper")
		reader, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil { c.t.Fatal(err) }
		defer reader.Close()
		held := c.hold(reader, pgfdwQuery)
		defer held.Statement.Close()
		c.run(reader, fixture.Exec("SET ROLE pgfdw_reader", 0))
		defer reader.ExecContext(context.Background(), "RESET ROLE")
		initial := [][]any{{oid, "pgfdw_wrapper", c.owner, int64(0), int64(0), nil, "{password=private=value,empty=,remote=é=🙂}"}}
		for _, e := range []fixture.Executor{reader, held} { c.raw(e, "pgfdw_wrapper", initial) }
		info := func(visible bool) {
			var want [][]any
			if visible { want = [][]any{{"pgfdw_wrapper"}} }
			c.both(reader, fixture.Select("SELECT foreign_data_wrapper_name FROM information_schema.foreign_data_wrappers WHERE foreign_data_wrapper_name='pgfdw_wrapper'", []string{"foreign_data_wrapper_name"}, []string{"NAME"}, want))
		}
		info(false)
		c.exec("GRANT USAGE ON FOREIGN DATA WRAPPER pgfdw_wrapper TO pgfdw_reader WITH GRANT OPTION")
		info(true)
		// Check actual grantor/grantee relationships, not generated role numbers
		// or an incidental ACL item ordering.
		c.both(reader, fixture.Select("SELECT r.rolname,a.grantor=w.fdwowner AS owner_grantor,a.privilege_type,a.is_grantable FROM pg_catalog.pg_foreign_data_wrapper w CROSS JOIN LATERAL aclexplode(w.fdwacl) a JOIN pg_roles r ON r.oid=a.grantee WHERE w.fdwname='pgfdw_wrapper' AND r.rolname='pgfdw_reader'", []string{"rolname", "owner_grantor", "privilege_type", "is_grantable"}, []string{"NAME", "BOOL", "TEXT", "BOOL"}, [][]any{{"pgfdw_reader", true, "USAGE", true}}))
		c.options(reader, "pgfdw_wrapper", []string{"password=private=value", "empty=", "remote=é=🙂"})
		c.exec("REVOKE USAGE ON FOREIGN DATA WRAPPER pgfdw_wrapper FROM pgfdw_reader")
		info(false)
		c.both(reader, fixture.Select("SELECT fdwacl IS NULL AS implicit,fdwoptions[1] AS secret FROM pg_catalog.pg_foreign_data_wrapper WHERE fdwname='pgfdw_wrapper'", []string{"implicit", "secret"}, []string{"BOOL", "TEXT"}, [][]any{{false, "password=private=value"}}))
		c.both(reader, fixture.Select("SELECT a.grantee=w.fdwowner AS owner_grantee,a.grantor=w.fdwowner AS owner_grantor,a.privilege_type,a.is_grantable FROM pg_catalog.pg_foreign_data_wrapper w CROSS JOIN LATERAL aclexplode(w.fdwacl) a WHERE w.fdwname='pgfdw_wrapper'", []string{"owner_grantee", "owner_grantor", "privilege_type", "is_grantable"}, []string{"BOOL", "BOOL", "TEXT", "BOOL"}, [][]any{{true, true, "USAGE", false}}))
		// A catalog revoke must be rechecked by an already physically prepared
		// handle, even if its predicate selects no row. USAGE cannot override it.
		c.exec("GRANT USAGE ON FOREIGN DATA WRAPPER pgfdw_wrapper TO pgfdw_reader", "REVOKE SELECT ON pg_catalog.pg_foreign_data_wrapper FROM PUBLIC")
		for _, name := range []string{"pgfdw_wrapper", "absent"} {
			step := fixture.ImplicitError(pgfdwQuery, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_foreign_data_wrapper"}, name)
			step.SelectRows, step.ErrorStage = true, "execute"
			c.run(held, step)
			c.run(reader, step)
		}
		c.exec("GRANT SELECT(fdwname) ON pg_catalog.pg_foreign_data_wrapper TO pgfdw_reader")
		c.both(reader, fixture.Select("SELECT fdwname FROM pg_catalog.pg_foreign_data_wrapper WHERE fdwname=$1", []string{"fdwname"}, []string{"NAME"}, [][]any{{"pgfdw_wrapper"}}, "pgfdw_wrapper"))
		step := fixture.ImplicitError("SELECT fdwoptions FROM pg_catalog.pg_foreign_data_wrapper WHERE fdwname='pgfdw_wrapper'", fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_foreign_data_wrapper"})
		step.SelectRows, step.ErrorStage = true, "execute"
		c.run(reader, step)
		c.exec("REVOKE SELECT(fdwname) ON pg_catalog.pg_foreign_data_wrapper FROM pgfdw_reader", "GRANT SELECT ON pg_catalog.pg_foreign_data_wrapper TO PUBLIC")
		c.options(reader, "pgfdw_wrapper", []string{"password=private=value", "empty=", "remote=é=🙂"})
	})
}

func TestReferencePGForeignDataWrapperLifecycle(t *testing.T) {
	pgfdwScenario(t, func(c *pgfdwCase) {
		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil { c.t.Fatal(err) }
		defer peer.Close()
		held := c.hold(peer, pgfdwQuery)
		defer held.Statement.Close()
		c.raw(held, "pgfdw_wrapper", nil)
		c.exec(pgfdwSeed)
		oid := c.oid("pgfdw_wrapper")
		original := [][]any{{oid, "pgfdw_wrapper", c.owner, int64(0), int64(0), nil, "{password=private=value,empty=,remote=é=🙂}"}}
		c.raw(held, "pgfdw_wrapper", original)
		c.exec("CREATE ROLE pgfdw_owner SUPERUSER", "ALTER FOREIGN DATA WRAPPER pgfdw_wrapper OWNER TO pgfdw_owner")
		var owner int64
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='pgfdw_owner'").Scan(&owner); err != nil { c.t.Fatal(err) }
		c.identity("pgfdw_wrapper", oid, owner)
		// Ownership transfer must not materialize an originally NULL ACL.
		c.raw(held, "pgfdw_wrapper", [][]any{{oid, "pgfdw_wrapper", owner, int64(0), int64(0), nil, "{password=private=value,empty=,remote=é=🙂}"}})
		c.exec("ALTER ROLE pgfdw_owner RENAME TO pgfdw_newowner")
		c.identity("pgfdw_wrapper", oid, owner)
		c.both(c.conn, fixture.Select("SELECT r.rolname FROM pg_catalog.pg_foreign_data_wrapper w JOIN pg_roles r ON r.oid=w.fdwowner WHERE w.fdwname='pgfdw_wrapper'", []string{"rolname"}, []string{"NAME"}, [][]any{{"pgfdw_newowner"}}))
		c.exec("ALTER FOREIGN DATA WRAPPER pgfdw_wrapper OPTIONS(SET password 'committed')", "ALTER FOREIGN DATA WRAPPER pgfdw_wrapper RENAME TO pgfdw_renamed")
		c.raw(held, "pgfdw_wrapper", nil)
		current := [][]any{{oid, "pgfdw_renamed", owner, int64(0), int64(0), nil, "{password=committed,empty=,remote=é=🙂}"}}
		c.raw(held, "pgfdw_renamed", current)
		// Each operation completes before the peer query: no sleeps or timing race.
		c.exec("BEGIN", "ALTER FOREIGN DATA WRAPPER pgfdw_renamed OPTIONS(SET password 'private')")
		c.options(c.conn, "pgfdw_renamed", []string{"password=private", "empty=", "remote=é=🙂"})
		c.raw(held, "pgfdw_renamed", current)
		c.exec("DROP FOREIGN DATA WRAPPER pgfdw_renamed")
		c.raw(c.conn, "pgfdw_renamed", nil)
		c.raw(held, "pgfdw_renamed", current)
		c.exec("ROLLBACK")
		c.raw(c.conn, "pgfdw_renamed", current)
		c.exec("BEGIN", "ALTER FOREIGN DATA WRAPPER pgfdw_renamed OPTIONS(SET password 'published')", "COMMIT")
		current[0][6] = "{password=published,empty=,remote=é=🙂}"
		c.raw(held, "pgfdw_renamed", current)
		for _, commit := range []bool{false, true} {
			c.exec("BEGIN", "CREATE FOREIGN DATA WRAPPER pgfdw_tx OPTIONS(remote 'private')")
			txOID := c.oid("pgfdw_tx")
			txRow := [][]any{{txOID, "pgfdw_tx", c.owner, int64(0), int64(0), nil, "{remote=private}"}}
			c.raw(c.conn, "pgfdw_tx", txRow)
			c.raw(held, "pgfdw_tx", nil)
			if commit {
				c.exec("COMMIT")
				c.raw(held, "pgfdw_tx", txRow)
			} else {
				c.exec("ROLLBACK")
				c.raw(held, "pgfdw_tx", nil)
			}
		}
		c.exec("BEGIN", "DROP FOREIGN DATA WRAPPER pgfdw_renamed")
		c.raw(held, "pgfdw_renamed", current)
		c.exec("COMMIT")
		c.raw(held, "pgfdw_renamed", nil)
	})
}
