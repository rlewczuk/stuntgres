package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCollationsRows(t *testing.T) {
	icoScenario(t, false, func(c *icoCase) {
		for _, targets := range []string{"*", strings.Join(icoColumns, ",")} {
			q := "SELECT " + targets + " FROM information_schema.collations WHERE " + icoKnown + " ORDER BY collation_name"
			s := c.projection(q, c.seeds())
			c.routes(s)
			// database/sql.Conn exposes only context methods; plain API routes use
			// the pool while retaining the same database and identity.
			s.NonContext = true
			c.run(c.b.DB, s)
			c.run(c.b.DB, fixture.Prepared(s))
		}
		// Native's complete fresh inventory is seven real registry objects;
		// PostgreSQL may additionally import environment-specific collations.
		if !c.b.PG {
			c.routes(c.projection(icoQuery+" ORDER BY collation_name", c.seeds()))
		}
		for _, q := range []string{icoQuery+" WHERE FALSE", icoQuery+" WHERE collation_name='icoll_nonexistent'", icoQuery+" WHERE collation_name IS NULL"} {
			c.routes(c.projection(q, nil))
		}
		q := icoQuery + " WHERE collation_name=$1 AND collation_schema='pg_catalog'"
		held := c.hold(q)
		for _, arg := range []any{"C", "icoll_nonexistent", nil, "C"} {
			var want [][]any
			if arg == "C" { want = c.row("pg_catalog", "C") }
			s := c.projection(q, want, arg)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(held, s)
		}
		c.exec("CREATE TABLE public.icoll_null_witness(id integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO public.icoll_null_witness VALUES(1)", 1))
		nullQuery := "SELECT i.collation_catalog,i.collation_schema,i.collation_name,i.pad_attribute FROM public.icoll_null_witness w LEFT JOIN information_schema.collations i ON i.collation_name='icoll_nonexistent'"
		for _, sample := range []struct{ query string; want [][]any }{
			{icoQuery+" WHERE "+icoKnown+" ORDER BY collation_name", c.seeds()},
			{icoQuery+" WHERE FALSE", nil},
			{nullQuery, [][]any{{nil,nil,nil,nil}}},
		} {
			c.routes(c.projection(sample.query, sample.want))
			h := c.hold(sample.query)
			for _, route := range []string{"direct", "prepared", "held"} {
				if route == "held" {
					rows, err := h.Statement.QueryContext(c.p.Ctx)
					if err != nil { t.Fatal(err) }
					c.consume(rows, sample.want)
				} else {
					rows, closeQuery, err := fixture.TransactionQuery(c.p.Ctx, c.conn, sample.query, route == "prepared")
					if err != nil { t.Fatal(err) }
					c.consume(rows, sample.want)
					if err := closeQuery(); err != nil { t.Fatal(err) }
				}
			}
		}
	})
}
