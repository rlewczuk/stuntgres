package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGForeignServerLifecycle(t *testing.T) {
	pgfsScenario(t, func(c *pgfsCase) {
		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil { c.t.Fatal(err) }
		defer peer.Close()
		held := c.hold(peer, pgfsQuery)
		defer held.Statement.Close()
		c.raw(held, "pgfs_server", nil)
		c.wrapper()
		c.exec(pgfsSeed)
		oid, wrapper := c.oid("pgfs_server"), c.wrapperOID()
		original := [][]any{c.row("pgfs_server", oid, c.owner, wrapper, "", "18", nil, "{password=private=value,empty=,remote=é=🙂}")}
		c.raw(held, "pgfs_server", original)
		c.exec("CREATE ROLE pgfs_newowner", "GRANT USAGE ON FOREIGN DATA WRAPPER pgfs_wrapper TO pgfs_newowner", "ALTER SERVER pgfs_server OWNER TO pgfs_newowner")
		var owner int64
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='pgfs_newowner'").Scan(&owner); err != nil { c.t.Fatal(err) }
		c.identity("pgfs_server", oid, owner, wrapper)
		c.raw(held, "pgfs_server", [][]any{c.row("pgfs_server", oid, owner, wrapper, "", "18", nil, "{password=private=value,empty=,remote=é=🙂}")})
		c.exec("ALTER SERVER pgfs_server VERSION NULL OPTIONS(SET password 'committed')", "ALTER FOREIGN DATA WRAPPER pgfs_wrapper RENAME TO pgfs_renamed_wrapper", "ALTER SERVER pgfs_server RENAME TO pgfs_renamed")
		c.raw(held, "pgfs_server", nil)
		current := [][]any{c.row("pgfs_renamed", oid, owner, wrapper, "", nil, nil, "{password=committed,empty=,remote=é=🙂}")}
		c.raw(held, "pgfs_renamed", current)
		c.identity("pgfs_renamed", oid, owner, wrapper)
		c.both(c.conn, fixture.Select("SELECT w.fdwname FROM pg_catalog.pg_foreign_server s JOIN pg_catalog.pg_foreign_data_wrapper w ON w.oid=s.srvfdw WHERE s.srvname='pgfs_renamed'", []string{"fdwname"}, []string{"NAME"}, [][]any{{"pgfs_renamed_wrapper"}}))
		c.exec("BEGIN", "ALTER SERVER pgfs_renamed VERSION 'private' OPTIONS(SET password 'private')", "GRANT USAGE ON FOREIGN SERVER pgfs_renamed TO PUBLIC")
		c.options(c.conn, "pgfs_renamed", []string{"password=private", "empty=", "remote=é=🙂"})
		c.raw(held, "pgfs_renamed", current)
		c.exec("DROP SERVER pgfs_renamed")
		c.raw(c.conn, "pgfs_renamed", nil)
		c.raw(held, "pgfs_renamed", current)
		c.exec("ROLLBACK")
		c.raw(c.conn, "pgfs_renamed", current)
		c.exec("BEGIN", "ALTER SERVER pgfs_renamed OPTIONS(SET password 'published')", "COMMIT")
		current[0][7] = "{password=published,empty=,remote=é=🙂}"
		c.raw(held, "pgfs_renamed", current)
		for _, commit := range []bool{false, true} {
			c.exec("BEGIN", "CREATE SERVER pgfs_tx FOREIGN DATA WRAPPER pgfs_renamed_wrapper")
			txOID := c.oid("pgfs_tx")
			txRow := [][]any{c.row("pgfs_tx", txOID, c.owner, wrapper, nil, nil, nil, nil)}
			c.raw(c.conn, "pgfs_tx", txRow)
			c.raw(held, "pgfs_tx", nil)
			if commit { c.exec("COMMIT"); c.raw(held, "pgfs_tx", txRow) } else { c.exec("ROLLBACK"); c.raw(held, "pgfs_tx", nil) }
		}
		var retained *sql.Rows
		if !c.b.PG {
			retained, err = c.conn.QueryContext(c.p.Ctx, pgfsQuery, "pgfs_renamed")
			if err != nil { c.t.Fatal(err) }
			defer retained.Close()
		}
		c.exec("BEGIN", "DROP SERVER pgfs_renamed")
		c.raw(held, "pgfs_renamed", current)
		c.exec("COMMIT")
		c.raw(held, "pgfs_renamed", nil)
		c.exec("CREATE SERVER pgfs_renamed FOREIGN DATA WRAPPER pgfs_renamed_wrapper")
		replacement := c.oid("pgfs_renamed")
		if replacement == oid { c.t.Fatal("DROP/recreate reused server identity") }
		c.raw(held, "pgfs_renamed", [][]any{c.row("pgfs_renamed", replacement, c.owner, wrapper, nil, nil, nil, nil)})
		if retained != nil {
			if !retained.Next() { c.t.Fatal("lost predecessor row", retained.Err()) }
			values, destinations := make([]any, 8), make([]any, 8)
			for i := range values { destinations[i] = &values[i] }
			if err := retained.Scan(destinations...); err != nil { c.t.Fatal(err) }
			if !reflect.DeepEqual(values, current[0]) || retained.Next() || retained.Err() != nil {
				c.t.Fatal("retained row retargeted to replacement", values, retained.Err())
			}
			if err := retained.Close(); err != nil { c.t.Fatal(err) }
		}
		c.exec("CREATE FOREIGN TABLE public.pgfs_table(id integer) SERVER pgfs_renamed", "CREATE USER MAPPING FOR PUBLIC SERVER pgfs_renamed OPTIONS(password 'mapping')")
		// A later missing root must reject the entire multi-name DROP.
		for _, prepared := range []bool{false, true} {
			c.error(c.conn, "DROP SERVER pgfs_tx, pgfs_bad", "42704", `server "pgfs_bad" does not exist`, prepared)
			c.raw(held, "pgfs_tx", [][]any{c.row("pgfs_tx", c.oid("pgfs_tx"), c.owner, wrapper, nil, nil, nil, nil)})
		}
		c.exec("DROP SERVER pgfs_renamed CASCADE")
		c.raw(held, "pgfs_renamed", nil)
		c.both(c.conn, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='pgfs_table'", []string{"relname"}, []string{"NAME"}, nil))
		c.both(c.conn, fixture.Select("SELECT srvname FROM pg_catalog.pg_foreign_server WHERE oid=$1::oid", []string{"srvname"}, []string{"NAME"}, nil, replacement))
		c.both(c.conn, fixture.Select("SELECT umserver::bigint AS server FROM pg_catalog.pg_user_mapping WHERE umserver=$1::oid", []string{"server"}, []string{"INT8"}, nil, replacement))
		c.exec(`CREATE SERVER "PGFS Quoted" TYPE '' VERSION '' FOREIGN DATA WRAPPER pgfs_renamed_wrapper`)
		quoted := c.oid("PGFS Quoted")
		c.raw(held, "PGFS Quoted", [][]any{c.row("PGFS Quoted", quoted, c.owner, wrapper, "", "", nil, nil)})
		c.raw(held, "pgfs quoted", nil)
		c.exec(`DROP SERVER "PGFS Quoted"`)
	})
}
