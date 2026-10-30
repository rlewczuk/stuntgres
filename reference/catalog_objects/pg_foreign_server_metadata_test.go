package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func (c *pgfsCase) attributes() {
	c.t.Helper()
	want := [][]any{
		{int64(1), "oid", int64(26), int64(-1), true, int64(0), int64(0)},
		{int64(2), "srvname", int64(19), int64(-1), true, int64(0), int64(950)},
		{int64(3), "srvowner", int64(26), int64(-1), true, int64(0), int64(0)},
		{int64(4), "srvfdw", int64(26), int64(-1), true, int64(0), int64(0)},
		{int64(5), "srvtype", int64(25), int64(-1), false, int64(0), int64(950)},
		{int64(6), "srvversion", int64(25), int64(-1), false, int64(0), int64(950)},
		{int64(7), "srvacl", int64(1034), int64(-1), false, int64(1), int64(0)},
		{int64(8), "srvoptions", int64(1009), int64(-1), false, int64(1), int64(950)},
	}
	c.both(c.conn, fixture.Select("SELECT attnum,attname,atttypid::bigint AS type,atttypmod,attnotnull,attndims,attcollation::bigint AS collation FROM pg_catalog.pg_attribute WHERE attrelid=1417 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "type", "atttypmod", "attnotnull", "attndims", "collation"}, []string{"INT2", "NAME", "INT8", "INT4", "BOOL", "INT2", "INT8"}, want))
	c.both(c.conn, fixture.Select("SELECT relname,relhasindex FROM pg_catalog.pg_class WHERE oid=1417", []string{"relname", "relhasindex"}, []string{"NAME", "BOOL"}, [][]any{{"pg_foreign_server", true}}))
}

func TestReferencePGForeignServerShape(t *testing.T) {
	pgfsScenario(t, func(c *pgfsCase) {
		held := c.hold(c.conn, pgfsQuery)
		for _, e := range []fixture.Executor{c.conn, c.hold(c.conn, pgfsQuery), held} {
			c.shape(e, "pgfs_server", false)
			c.raw(e, "pgfs_server", nil)
		}
		c.wrapper()
		wrapper := c.wrapperOID()
		for i, name := range []string{"pgfs_server", "pgfs_empty"} {
			step := fixture.Exec("CREATE SERVER "+name+" FOREIGN DATA WRAPPER pgfs_wrapper", 0)
			if i == 1 { step = fixture.Prepared(step) }
			c.run(c.conn, step)
			oid := c.oid(name)
			for _, e := range []fixture.Executor{c.conn, c.hold(c.conn, pgfsQuery), held} {
				c.shape(e, name, true)
				c.raw(e, name, [][]any{c.row(name, oid, c.owner, wrapper, nil, nil, nil, nil)})
			}
		}
		c.exec("ALTER SERVER pgfs_server OPTIONS(password 'private=value',empty '',remote 'é=🙂')")
		for _, e := range []fixture.Executor{c.conn, held} { c.shape(e, "pgfs_server", true) }
		c.attributes()
	})
}

func TestReferencePGForeignServerMetadata(t *testing.T) {
	pgfsScenario(t, func(c *pgfsCase) {
		c.wrapper()
		c.exec(pgfsSeed, "ALTER FOREIGN DATA WRAPPER pgfs_wrapper OPTIONS(password 'wrapper-only')", "CREATE USER MAPPING FOR PUBLIC SERVER pgfs_server OPTIONS(password 'mapping-only')", "CREATE FOREIGN TABLE public.pgfs_table(id integer OPTIONS(remote 'column-only')) SERVER pgfs_server OPTIONS(remote 'table-only')", "CREATE FOREIGN DATA WRAPPER pgfs_other_wrapper", "CREATE SERVER pgfs_other TYPE 'other' FOREIGN DATA WRAPPER pgfs_other_wrapper OPTIONS(remote 'other-server')")
		oid, wrapper := c.oid("pgfs_server"), c.wrapperOID()
		c.identity("pgfs_server", oid, c.owner, wrapper)
		held := c.hold(c.conn, pgfsQuery)
		check := func(version, options any) {
			for _, e := range []fixture.Executor{c.conn, c.hold(c.conn, pgfsQuery), held} {
				c.raw(e, "pgfs_server", [][]any{c.row("pgfs_server", oid, c.owner, wrapper, "", version, nil, options)})
			}
		}
		check("18", "{password=private=value,empty=,remote=é=🙂}")
		c.options(held, "pgfs_server", []string{"password=private=value", "empty=", "remote=é=🙂"})
		c.run(c.conn, fixture.Prepared(fixture.Exec("ALTER SERVER pgfs_server VERSION NULL OPTIONS(SET password 'changed',DROP empty,ADD tail 'last')", 0)))
		check(nil, "{password=changed,remote=é=🙂,tail=last}")
		c.identity("pgfs_server", oid, c.owner, wrapper)
		c.exec("ALTER SERVER pgfs_server VERSION '' OPTIONS(SET password 'first',SET password 'second',DROP tail,ADD tail 'again')")
		check("", "{password=second,remote=é=🙂,tail=again}")
		c.exec(`ALTER SERVER pgfs_server OPTIONS(SET password 'quote''comma,brace{}',SET remote E'back\\slash',SET tail 'é=🙂')`)
		c.options(held, "pgfs_server", []string{"password=quote'comma,brace{}", `remote=back\slash`, "tail=é=🙂"})
		check("", `{"password=quote'comma,brace{}","remote=back\\slash",tail=é=🙂}`)
		c.exec("ALTER SERVER pgfs_server OPTIONS(DROP password,DROP remote,DROP tail)")
		check("", nil)
		c.options(held, "pgfs_server", nil)
	})
}

func TestReferencePGForeignServerSmoke(t *testing.T) {
	pgfsScenario(t, func(c *pgfsCase) {
		held := c.hold(c.conn, pgfsQuery)
		c.shape(held, "pgfs_server", false)
		c.wrapper()
		c.exec(pgfsSeed)
		oid, wrapper := c.oid("pgfs_server"), c.wrapperOID()
		before := [][]any{c.row("pgfs_server", oid, c.owner, wrapper, "", "18", nil, "{password=private=value,empty=,remote=é=🙂}")}
		for _, e := range []fixture.Executor{c.conn, held} {
			c.raw(e, "pgfs_server", before)
			c.shape(e, "pgfs_server", true)
		}
		c.identity("pgfs_server", oid, c.owner, wrapper)
		c.attributes()
		c.exec("CREATE ROLE pgfs_reader", "SET ROLE pgfs_reader")
		c.raw(held, "pgfs_server", before)
		c.both(c.conn, fixture.Select("SELECT foreign_server_name FROM information_schema.foreign_servers WHERE foreign_server_name='pgfs_server'", []string{"foreign_server_name"}, []string{"NAME"}, nil))
		c.exec("RESET ROLE", "ALTER SERVER pgfs_server VERSION NULL OPTIONS(SET password 'changed',DROP empty,ADD tail 'last')")
		want := [][]any{c.row("pgfs_server", oid, c.owner, wrapper, "", nil, nil, "{password=changed,remote=é=🙂,tail=last}")}
		c.raw(held, "pgfs_server", want)
		c.identity("pgfs_server", oid, c.owner, wrapper)
		c.error(c.conn, "ALTER SERVER pgfs_server OPTIONS(DROP absent)", "42704", `option "absent" not found`, false)
		c.raw(held, "pgfs_server", want)
		c.exec("CREATE FOREIGN TABLE public.pgfs_probe(id integer) SERVER pgfs_server")
		step := fixture.ImplicitError("SELECT * FROM public.pgfs_probe", fixture.Diagnostic{Code: "55000", Message: `foreign-data wrapper "pgfs_wrapper" has no handler`})
		step.SelectRows, step.ErrorStage = true, "execute"
		c.both(c.conn, step)
		c.exec("DROP FOREIGN TABLE public.pgfs_probe", "DROP SERVER pgfs_server")
		c.raw(held, "pgfs_server", nil)
		c.shape(held, "pgfs_server", false)
	})
}
