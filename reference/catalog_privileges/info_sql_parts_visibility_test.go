package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func spDenied(c *spCase, q string, held fixture.HeldStatement, transactional bool) {
	c.t.Helper()
	for _, route := range []string{"direct", "prepared", "held"} {
		if transactional {
			c.exec("SAVEPOINT sp_denial")
		}
		s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table sql_parts"))
		s.SelectRows, s.ErrorStage = true, "execute"
		var executor fixture.Executor = c.conn
		if route == "prepared" {
			s.Prepared = true
		}
		if route == "held" {
			executor = held
		}
		c.run(executor, s)
		if transactional {
			c.exec("ROLLBACK TO SAVEPOINT sp_denial", "RELEASE SAVEPOINT sp_denial")
		}
	}
}

// Default denial is shared SQL acceptance, not a PostgreSQL-only ACL phase.
func spDefaultDenial(c *spCase) {
	c.t.Helper()
	full := c.hold(spQuery)
	emptyQuery := spQuery + " WHERE FALSE"
	empty := c.hold(emptyQuery)
	c.run(full, c.projection(spQuery, spRows()))
	c.run(empty, c.projection(emptyQuery, nil))
	c.exec("SET SESSION AUTHORIZATION sp_reader")
	c.run(c.conn, fixture.Select(`SELECT relname FROM pg_catalog.pg_class WHERE oid='information_schema.sql_parts'::regclass`, []string{"relname"}, []string{"NAME"}, [][]any{{"sql_parts"}}))
	spDenied(c, spQuery, full, false)
	spDenied(c, emptyQuery, empty, false)
	c.exec("RESET SESSION AUTHORIZATION")
	c.run(full, c.projection(spQuery, spRows()))
	c.run(empty, c.projection(emptyQuery, nil))
}

func TestReferenceInfoSQLPartsVisibility(t *testing.T) {
	spScenario(t, true, func(c *spCase) {
		c.exec("CREATE ROLE sp_reader", "CREATE ROLE sp_super SUPERUSER")
		c.check(spQuery, spRows())
		c.exec("SET SESSION AUTHORIZATION postgres")
		c.check(spQuery, spRows())
		c.exec("RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION sp_super")
		c.check(spQuery, spRows())
		c.exec("RESET SESSION AUTHORIZATION")
		spDefaultDenial(c)
		c.exec("GRANT USAGE,CREATE ON SCHEMA public TO sp_reader", "SET SESSION AUTHORIZATION sp_reader", "CREATE TABLE public.sp_reader_owned(id integer)", "CREATE DOMAIN public.sp_reader_domain AS varchar", "RESET SESSION AUTHORIZATION")
		spDefaultDenial(c)
		c.check(spQuery, spRows())

		// Transaction-local ACLs are checked by direct and held consumers.
		{
			c.exec("BEGIN")
			full := c.hold(spQuery)
			emptyQuery := spQuery + " WHERE FALSE"
			empty := c.hold(emptyQuery)
			c.exec("GRANT SELECT ON information_schema.sql_parts TO sp_reader", "SET LOCAL ROLE sp_reader")
			c.check(spQuery, spRows())
			c.check(emptyQuery, nil)
			c.run(full, c.projection(spQuery, spRows()))
			c.run(empty, c.projection(emptyQuery, nil))
			c.exec("RESET ROLE", "REVOKE SELECT ON information_schema.sql_parts FROM sp_reader", "SET LOCAL ROLE sp_reader")
			spDenied(c, spQuery, full, true)
			spDenied(c, emptyQuery, empty, true)
			c.exec("ROLLBACK")
			c.run(full, c.projection(spQuery, spRows()))
			c.run(empty, c.projection(emptyQuery, nil))
		}
		spDefaultDenial(c)
	})
}
