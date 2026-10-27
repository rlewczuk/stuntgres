package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func (c *trCase) denied(e fixture.Executor, q string, prepared bool, schema bool) {
	c.t.Helper()
	diagnostic := trDiagnostic("42501", "permission denied for view transforms")
	stage := "execute"
	if schema {
		diagnostic.Message = "permission denied for schema information_schema"
		diagnostic.Position = "15"
		if prepared {
			stage = "prepare"
		}
	}
	s := fixture.ErrorDiagnostic(q, diagnostic)
	s.SelectRows = true
	s.ErrorStage = stage
	if prepared {
		s = fixture.Prepared(s)
	}
	c.run(e, s)
}

func (c *trCase) visibility(smoke bool) {
	defer func() {
		for _, q := range []string{"SET ROLE NONE", "REVOKE SELECT ON TABLE information_schema.transforms FROM tr_reader"} {
			if _, err := c.conn.ExecContext(c.p.Ctx, q); err != nil {
				c.t.Error(err)
			}
		}
	}()
	c.exec("CREATE ROLE tr_reader")
	held := c.hold(trQuery)
	c.run(c.conn, fixture.Select("SELECT has_schema_privilege('tr_reader','information_schema','USAGE') AS admitted,has_table_privilege('tr_reader','information_schema.transforms','SELECT') AS readable", []string{"admitted", "readable"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
	actor := func(allowed bool) {
		c.exec("SET ROLE tr_reader")
		if allowed {
			c.run(c.conn, c.step(trQuery, nil))
			c.run(c.conn, fixture.Prepared(c.step(trQuery, nil)))
			c.run(held, c.step(trQuery, nil))
		} else {
			queries := []string{trQuery}
			if !smoke {
				queries = append(queries, trQuery+" WHERE false", trQuery+" WHERE NULL::boolean")
			}
			for _, q := range queries {
				c.denied(c.conn, q, false, false)
				c.denied(c.conn, q, true, false)
			}
			c.denied(held, trQuery, false, false)
		}
		c.exec("SET ROLE NONE")
	}
	actor(false)
	c.exec("GRANT SELECT ON TABLE information_schema.transforms TO tr_reader")
	actor(true)
	// Empty successful results keep immutable metadata across ACL/identity changes.
	retained, err := held.Statement.QueryContext(c.p.Ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	defer retained.Close()
	saved, err := retained.ColumnTypes()
	if err != nil {
		c.t.Fatal(err)
	}
	trMetadata(c.t, saved, c.b.PG)
	if c.b.PG {
		// pq streams: finish its protocol result before issuing another command.
		c.consume(retained, 0)
	}
	c.exec("REVOKE SELECT ON TABLE information_schema.transforms FROM tr_reader")
	actor(false)
	if !c.b.PG {
		// Native eager rows remain readable after the authority changes.
		c.consume(retained, 0)
	}
	trMetadata(c.t, saved, c.b.PG)
	if smoke {
		c.inventory(c.conn)
		return
	}
	c.exec("GRANT SELECT ON TABLE information_schema.transforms TO tr_reader")
	// Revoke unrelated actual metadata-object privileges, not builtin language rights.
	c.exec("CREATE DOMAIN public.tr_hidden AS integer", "CREATE FUNCTION public.tr_hidden(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'", "REVOKE USAGE ON DOMAIN public.tr_hidden FROM PUBLIC", "REVOKE EXECUTE ON FUNCTION public.tr_hidden(integer) FROM PUBLIC")
	actor(true)
	c.exec("DROP FUNCTION public.tr_hidden(integer)", "DROP DOMAIN public.tr_hidden")
	// The oracle shares information_schema; register restoration before mutating it.
	defer func() {
		for _, q := range []string{"SET ROLE NONE", "GRANT USAGE ON SCHEMA information_schema TO PUBLIC", "REVOKE USAGE ON SCHEMA information_schema FROM tr_reader", "REVOKE SELECT ON TABLE information_schema.transforms FROM tr_reader"} {
			if _, err := c.conn.ExecContext(c.p.Ctx, q); err != nil {
				c.t.Error(err)
			}
		}
	}()
	c.exec("REVOKE USAGE ON SCHEMA information_schema FROM PUBLIC", "SET ROLE tr_reader")
	c.denied(c.conn, trQuery, false, true)
	c.denied(c.conn, trQuery, true, true)
	c.denied(held, trQuery, false, true)
	c.exec("SET ROLE NONE", "GRANT USAGE ON SCHEMA information_schema TO tr_reader")
	actor(true)
	c.exec("GRANT USAGE ON SCHEMA information_schema TO PUBLIC", "REVOKE USAGE ON SCHEMA information_schema FROM tr_reader", "REVOKE SELECT ON TABLE information_schema.transforms FROM tr_reader")
	actor(false)
	c.inventory(c.conn)
}

func TestReferenceInfoTransformsVisibility(t *testing.T) {
	trScenario(t, func(c *trCase) { c.visibility(false) })
}

func TestReferenceInfoTransformsFeatureSmoke(t *testing.T) {
	trScenario(t, func(c *trCase) {
		held := c.hold(trQuery)
		c.run(held, c.step(trQuery, nil))
		c.routes(trNullQuery, trNullRow)
		c.visibility(true)
		c.inventory(c.conn)
	})
}
