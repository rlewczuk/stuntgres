package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func (c *pgfdwCase) attributes() {
	c.t.Helper()
	want := [][]any{
		{int64(1), "oid", int64(26), int64(-1), true, int64(0), int64(0)},
		{int64(2), "fdwname", int64(19), int64(-1), true, int64(0), int64(950)},
		{int64(3), "fdwowner", int64(26), int64(-1), true, int64(0), int64(0)},
		{int64(4), "fdwhandler", int64(26), int64(-1), true, int64(0), int64(0)},
		{int64(5), "fdwvalidator", int64(26), int64(-1), true, int64(0), int64(0)},
		{int64(6), "fdwacl", int64(1034), int64(-1), false, int64(1), int64(0)},
		{int64(7), "fdwoptions", int64(1009), int64(-1), false, int64(1), int64(950)},
	}
	c.both(c.conn, fixture.Select("SELECT attnum,attname,atttypid::bigint AS type,atttypmod,attnotnull,attndims,attcollation::bigint AS collation FROM pg_catalog.pg_attribute WHERE attrelid=2328 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "type", "atttypmod", "attnotnull", "attndims", "collation"}, []string{"INT2", "NAME", "INT8", "INT4", "BOOL", "INT2", "INT8"}, want))
	c.both(c.conn, fixture.Select("SELECT relname,relhasindex FROM pg_catalog.pg_class WHERE oid=2328", []string{"relname", "relhasindex"}, []string{"NAME", "BOOL"}, [][]any{{"pg_foreign_data_wrapper", true}}))
}

func TestReferencePGForeignDataWrapperShape(t *testing.T) {
	pgfdwScenario(t, func(c *pgfdwCase) {
		held := c.hold(c.conn, pgfdwQuery)
		for _, e := range []fixture.Executor{c.conn, held} {
			c.shape(e, "pgfdw_wrapper", false)
			c.raw(e, "pgfdw_wrapper", nil)
		}
		c.exec("CREATE FOREIGN DATA WRAPPER pgfdw_wrapper")
		oid := c.oid("pgfdw_wrapper")
		for _, e := range []fixture.Executor{c.conn, held} {
			c.shape(e, "pgfdw_wrapper", true)
			c.raw(e, "pgfdw_wrapper", [][]any{{oid, "pgfdw_wrapper", c.owner, int64(0), int64(0), nil, nil}})
		}
		c.exec("ALTER FOREIGN DATA WRAPPER pgfdw_wrapper OPTIONS(password 'private=value',empty '',remote 'é=🙂')")
		for _, e := range []fixture.Executor{c.conn, held} {
			c.shape(e, "pgfdw_wrapper", true)
			c.raw(e, "pgfdw_wrapper", [][]any{{oid, "pgfdw_wrapper", c.owner, int64(0), int64(0), nil, "{password=private=value,empty=,remote=é=🙂}"}})
		}
		c.attributes()
	})
}

func TestReferencePGForeignDataWrapperMetadata(t *testing.T) {
	pgfdwScenario(t, func(c *pgfdwCase) {
		c.run(c.conn, fixture.Prepared(fixture.Exec("CREATE FOREIGN DATA WRAPPER pgfdw_empty", 0)))
		oid := c.oid("pgfdw_empty")
		c.identity("pgfdw_empty", oid, c.owner)
		c.raw(c.conn, "pgfdw_empty", [][]any{{oid, "pgfdw_empty", c.owner, int64(0), int64(0), nil, nil}})
		c.exec(pgfdwSeed, "CREATE SERVER pgfdw_server FOREIGN DATA WRAPPER pgfdw_wrapper", "CREATE USER MAPPING FOR PUBLIC SERVER pgfdw_server OPTIONS(secret 'not-a-wrapper')", "CREATE FOREIGN TABLE public.pgfdw_table(id integer) SERVER pgfdw_server OPTIONS(remote 'table-only')")
		oid = c.oid("pgfdw_wrapper")
		c.identity("pgfdw_wrapper", oid, c.owner)
		held := c.hold(c.conn, pgfdwQuery)
		c.raw(held, "pgfdw_wrapper", [][]any{{oid, "pgfdw_wrapper", c.owner, int64(0), int64(0), nil, "{password=private=value,empty=,remote=é=🙂}"}})
		c.both(c.conn, fixture.Select("SELECT w.oid=s.srvfdw AS actual_wrapper FROM pg_catalog.pg_foreign_data_wrapper w JOIN pg_catalog.pg_foreign_server s ON s.srvfdw=w.oid WHERE s.srvname='pgfdw_server'", []string{"actual_wrapper"}, []string{"BOOL"}, [][]any{{true}}))
		c.options(c.conn, "pgfdw_wrapper", []string{"password=private=value", "empty=", "remote=é=🙂"})
		c.run(c.conn, fixture.Prepared(fixture.Exec("ALTER FOREIGN DATA WRAPPER pgfdw_wrapper OPTIONS(SET password 'changed',DROP empty,ADD tail 'last')", 0)))
		c.options(c.conn, "pgfdw_wrapper", []string{"password=changed", "remote=é=🙂", "tail=last"})
		c.raw(held, "pgfdw_wrapper", [][]any{{oid, "pgfdw_wrapper", c.owner, int64(0), int64(0), nil, "{password=changed,remote=é=🙂,tail=last}"}})
		c.exec("ALTER FOREIGN DATA WRAPPER pgfdw_wrapper OPTIONS(DROP password,DROP remote,DROP tail)")
		c.raw(held, "pgfdw_wrapper", [][]any{{oid, "pgfdw_wrapper", c.owner, int64(0), int64(0), nil, nil}})
		// A real SQL validator is executable through the existing routine path;
		// metadata records its actual procedure identity without invoking it.
		c.exec("CREATE FUNCTION public.pgfdw_validator(text[],oid) RETURNS integer LANGUAGE SQL AS 'SELECT 7'", "ALTER FOREIGN DATA WRAPPER pgfdw_wrapper VALIDATOR public.pgfdw_validator")
		c.both(c.conn, fixture.Select("SELECT w.fdwvalidator=p.oid AS actual_validator,w.fdwvalidator>0 AS nonzero FROM pg_catalog.pg_foreign_data_wrapper w JOIN pg_catalog.pg_proc p ON p.oid=w.fdwvalidator WHERE w.fdwname='pgfdw_wrapper' AND p.proname='pgfdw_validator'", []string{"actual_validator", "nonzero"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	})
}

func TestReferencePGForeignDataWrapperSmoke(t *testing.T) {
	pgfdwScenario(t, func(c *pgfdwCase) {
		c.exec(pgfdwSeed)
		oid := c.oid("pgfdw_wrapper")
		held := c.hold(c.conn, pgfdwQuery)
		before := [][]any{{oid, "pgfdw_wrapper", c.owner, int64(0), int64(0), nil, "{password=private=value,empty=,remote=é=🙂}"}}
		for _, e := range []fixture.Executor{c.conn, held} {
			c.raw(e, "pgfdw_wrapper", before)
			c.shape(e, "pgfdw_wrapper", true)
		}
		c.identity("pgfdw_wrapper", oid, c.owner)
		c.attributes()
		c.exec("CREATE ROLE pgfdw_reader", "SET ROLE pgfdw_reader")
		c.raw(held, "pgfdw_wrapper", before)
		c.both(c.conn, fixture.Select("SELECT foreign_data_wrapper_name FROM information_schema.foreign_data_wrappers WHERE foreign_data_wrapper_name='pgfdw_wrapper'", []string{"foreign_data_wrapper_name"}, []string{"NAME"}, nil))
		c.exec("RESET ROLE", "ALTER FOREIGN DATA WRAPPER pgfdw_wrapper OPTIONS(SET password 'changed',DROP empty,ADD tail 'last')")
		want := [][]any{{oid, "pgfdw_wrapper", c.owner, int64(0), int64(0), nil, "{password=changed,remote=é=🙂,tail=last}"}}
		c.raw(held, "pgfdw_wrapper", want)
		c.options(c.conn, "pgfdw_wrapper", []string{"password=changed", "remote=é=🙂", "tail=last"})
		c.error(c.conn, "ALTER FOREIGN DATA WRAPPER pgfdw_wrapper OPTIONS(DROP absent)", "42704", `option "absent" not found`, false)
		c.raw(held, "pgfdw_wrapper", want)
		c.exec("DROP FOREIGN DATA WRAPPER pgfdw_wrapper")
		c.raw(held, "pgfdw_wrapper", nil)
		c.shape(held, "pgfdw_wrapper", false)
	})
}
