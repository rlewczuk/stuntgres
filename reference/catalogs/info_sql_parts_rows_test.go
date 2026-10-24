package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoSQLPartsRows(t *testing.T) {
	spScenario(t, false, func(c *spCase) {
		for _, q := range []string{spQuery, "SELECT * FROM " + spTable} {
			c.check(q, spRows())
			held := c.hold(q)
			c.run(held, c.projection(q, spRows()))
		}
		var ordered [][]any
		for _, id := range []string{"1", "10", "11", "13", "14", "15", "16", "2", "3", "4", "9"} {
			ordered = append(ordered, spItem(id)...)
		}
		s := c.projection(spQuery+" ORDER BY feature_id", ordered)
		s.Bag = false
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q := spQuery + " WHERE feature_id=$1"
		held := c.hold(q)
		for _, id := range []any{"1", "11", "16", "999", "", nil, "2"} {
			c.check(q, spItem(id), id)
			c.run(held, c.projection(q, spItem(id), id))
		}
		full := c.hold(spQuery)
		for _, ddl := range []string{
			"CREATE TABLE public.sp_unrelated(id integer)",
			"CREATE DOMAIN public.sp_unrelated_domain AS varchar",
			"DROP DOMAIN public.sp_unrelated_domain",
			"DROP TABLE public.sp_unrelated",
		} {
			c.exec(ddl)
			c.check(spQuery, spRows())
			c.run(full, c.projection(spQuery, spRows()))
		}
	})
}

func TestReferenceInfoSQLPartsFeatureSmoke(t *testing.T) {
	spScenario(t, true, func(c *spCase) {
		q := spQuery + " WHERE feature_id=$1"
		held := c.hold(q)
		for _, id := range []any{"11", nil, "999", "1"} {
			c.run(held, c.projection(q, spItem(id), id))
		}
		c.check(spQuery, spRows())
		c.exec("CREATE ROLE sp_reader")
		spDefaultDenial(c)
		spCatalogShape(c)
	})
}
