package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoSQLImplementationInfoRows(t *testing.T) {
	siiScenario(t, false, func(c *siiCase) {
		for _, q := range []string{siiQuery, "SELECT * FROM " + siiTable} {
			c.check(c.conn, q, c.want)
			held := c.hold(q)
			c.run(held, c.projection(q, c.want))
		}
		q := siiQuery + " WHERE implementation_info_id=$1"
		held := c.hold(q)
		for _, id := range []any{"17", "18", "85", "not-an-item", nil, "17"} {
			c.check(held, q, siiItem(c.want, id), id)
			c.run(c.conn, c.projection(q, siiItem(c.want, id), id))
		}
		if !c.b.PG {
			c.run(c.conn, fixture.Select("SELECT version()", []string{"version"}, []string{"TEXT"}, [][]any{{"PostgreSQL 18.0 compatible; embedded stuntgres"}}))
		}
	})
}

func TestReferenceInfoSQLImplementationInfoStability(t *testing.T) {
	siiScenario(t, false, func(c *siiCase) {
		held := c.hold(siiQuery)
		c.check(held, siiQuery, c.want)
		for _, q := range []string{
			"SET search_path=public",
			"SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL SERIALIZABLE",
			"CREATE TABLE public.sii_unrelated(id integer)",
			"CREATE DOMAIN public.sii_unrelated_domain AS varchar",
			`CREATE COLLATION public.sii_rank_strictly_longer FROM pg_catalog."C"`,
			"DROP COLLATION public.sii_rank_strictly_longer",
			"DROP DOMAIN public.sii_unrelated_domain",
			"DROP TABLE public.sii_unrelated",
			"SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL READ COMMITTED",
			"RESET search_path",
		} {
			c.exec(q)
			c.check(held, siiQuery, c.want)
			c.run(c.conn, c.projection(siiQuery, c.want))
		}
	})
}

func TestReferenceInfoSQLImplementationInfoVisibility(t *testing.T) {
	siiScenario(t, true, func(c *siiCase) {
		siiPrivateSeed(c)
		held := c.hold(siiQuery)
		for _, actor := range []string{"sii_owner", "sii_reader"} {
			c.exec("SET SESSION AUTHORIZATION "+actor, "SET search_path=pg_catalog,public")
			c.check(held, siiQuery, c.want)
			c.run(c.conn, c.projection(siiQuery, c.want))
			if actor == "sii_owner" {
				c.run(c.conn, fixture.Select("SELECT * FROM public.sii_private", []string{"id"}, []string{"INT4"}, nil))
			} else {
				c.privateDenied()
			}
			c.exec("RESET SESSION AUTHORIZATION")
		}
		c.exec("GRANT sii_owner TO sii_reader", "SET SESSION AUTHORIZATION sii_reader", "SET ROLE sii_owner", "SET search_path=public")
		c.check(held, siiQuery, c.want)
		c.exec("RESET ROLE", "RESET SESSION AUTHORIZATION", "REVOKE sii_owner FROM sii_reader", "SET SESSION AUTHORIZATION sii_reader")
		c.privateDenied()
		c.check(held, siiQuery, c.want)
	})
}

func TestReferenceInfoSQLImplementationInfoFeatureSmoke(t *testing.T) {
	siiScenario(t, true, func(c *siiCase) {
		siiPrivateSeed(c)
		c.check(c.conn, siiQuery, c.want)
		q := siiQuery + " WHERE implementation_info_id=$1"
		held := c.hold(q)
		for _, id := range []any{"17", "18", "85", "2", "23", nil, "missing"} {
			c.run(held, c.projection(q, siiItem(c.want, id), id))
		}
		c.exec("SET SESSION AUTHORIZATION sii_reader")
		c.privateDenied()
		c.run(held, c.projection(q, siiItem(c.want, "17"), "17"))
		c.run(c.conn, fixture.Prepared(c.projection(siiQuery, c.want)))
		c.check(c.conn, siiQuery+" WHERE FALSE", nil)
		c.exec("RESET SESSION AUTHORIZATION")
		siiCatalogShape(c)
	})
}
