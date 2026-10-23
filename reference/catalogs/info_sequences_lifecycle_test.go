package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoSequencesLifecycle(t *testing.T) {
	isqScenario(t, func(c *isqCase) {
		held := c.hold(c.conn, isqQuery)
		c.all(held, "isq_life%")
		c.exec("CREATE SCHEMA isq_moved", "CREATE SEQUENCE isq_life AS INTEGER START 15 MINVALUE -20 MAXVALUE 200 INCREMENT 7 CYCLE")
		c.all(held, "isq_life%", c.custom("public", "isq_life"))
		c.exec("ALTER SEQUENCE isq_life AS BIGINT START WITH 21 INCREMENT BY 3 MINVALUE -30 MAXVALUE 300 NO CYCLE")
		row := c.row("public", "isq_life", "bigint", 64, "21", "-30", "300", "3", "NO")
		c.all(held, "isq_life%", row)
		c.run(c.conn, fixture.Select("SELECT nextval('isq_life')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(15)}}))
		c.all(held, "isq_life%", row)
		c.run(c.conn, fixture.Select("SELECT setval('isq_life',42,false)", []string{"setval"}, []string{"INT8"}, [][]any{{int64(42)}}))
		c.all(held, "isq_life%", row)
		c.exec("ALTER SEQUENCE isq_life CACHE 9")
		c.all(held, "isq_life%", row)
		c.exec("ALTER SEQUENCE isq_life RESTART WITH 90")
		c.all(held, "isq_life%", row)
		c.run(c.conn, fixture.Select("SELECT nextval('isq_life')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(90)}}))
		c.all(held, "isq_life%", row)
		c.exec("ALTER SEQUENCE isq_life RENAME TO isq_life_renamed")
		row = c.row("public", "isq_life_renamed", "bigint", 64, "21", "-30", "300", "3", "NO")
		c.all(held, "isq_life%", row)
		c.exec("ALTER SEQUENCE isq_life_renamed SET SCHEMA isq_moved")
		row = c.row("isq_moved", "isq_life_renamed", "bigint", 64, "21", "-30", "300", "3", "NO")
		c.all(held, "isq_life%", row)
		c.exec("DROP SEQUENCE isq_moved.isq_life_renamed")
		c.all(held, "isq_life%")
		c.exec("CREATE SEQUENCE isq_moved.isq_life_renamed AS SMALLINT INCREMENT -1")
		c.all(held, "isq_life%", c.row("isq_moved", "isq_life_renamed", "smallint", 16, "-1", "-32768", "-1", "-1", "NO"))
	})
}

func TestReferenceInfoSequencesTransactions(t *testing.T) {
	isqScenario(t, func(c *isqCase) {
		peer := c.peer()
		held, observer := c.hold(c.conn, isqQuery), c.hold(peer, isqQuery)
		c.exec("CREATE SEQUENCE isq_tx_base", "BEGIN", "CREATE SEQUENCE isq_tx_new AS INTEGER START 15 MINVALUE -20 MAXVALUE 200 INCREMENT 7 CYCLE")
		base, added := c.def("public", "isq_tx_base"), c.custom("public", "isq_tx_new")
		c.all(held, "isq_tx_%", base, added)
		c.check(observer, "isq_tx_%", base)
		c.exec("SAVEPOINT isq_before_alter", "ALTER SEQUENCE isq_tx_base START WITH 21 INCREMENT BY 3 MINVALUE -30 MAXVALUE 300 NO CYCLE")
		altered := c.row("public", "isq_tx_base", "bigint", 64, "21", "-30", "300", "3", "NO")
		c.all(held, "isq_tx_%", altered, added)
		c.check(observer, "isq_tx_%", base)
		c.exec("SAVEPOINT isq_before_drop", "DROP SEQUENCE isq_tx_new")
		c.all(held, "isq_tx_%", altered)
		c.exec("ROLLBACK TO SAVEPOINT isq_before_drop")
		c.all(held, "isq_tx_%", altered, added)
		c.exec("ROLLBACK TO SAVEPOINT isq_before_alter")
		c.all(held, "isq_tx_%", base, added)
		c.exec("ROLLBACK")
		c.all(held, "isq_tx_%", base)
		c.check(observer, "isq_tx_%", base)
		c.exec("BEGIN", "ALTER SEQUENCE isq_tx_base START WITH 21 INCREMENT BY 3 MINVALUE -30 MAXVALUE 300 NO CYCLE", "COMMIT")
		c.all(held, "isq_tx_%", altered)
		c.check(observer, "isq_tx_%", altered)
		c.exec("BEGIN", "DROP SEQUENCE isq_tx_base")
		c.all(held, "isq_tx_%")
		c.check(observer, "isq_tx_%", altered)
		c.exec("ROLLBACK")
		c.all(held, "isq_tx_%", altered)
		c.exec("BEGIN", "DROP SEQUENCE isq_tx_base", "COMMIT")
		c.all(held, "isq_tx_%")
		c.check(observer, "isq_tx_%")
		if !c.b.PG {
			// Native Rows are eager immutable results, unlike pq streaming cursors.
			c.exec("CREATE ROLE isq_actor", "CREATE SEQUENCE isq_tx_retained", "GRANT USAGE ON SEQUENCE isq_tx_retained TO isq_actor", "SET ROLE isq_actor")
			rows, err := held.QueryContext(c.p.Ctx, isqQuery, "isq_tx_retained")
			if err != nil {
				c.t.Fatal(err)
			}
			c.t.Cleanup(func() { rows.Close() })
			saved := isqMetadata(c.t, rows, false)
			c.run(peer, fixture.Exec("ALTER SEQUENCE isq_tx_retained START 9", 0))
			c.run(peer, fixture.Exec("REVOKE USAGE ON SEQUENCE isq_tx_retained FROM isq_actor", 0))
			isqConsume(c.t, rows, false, [][]any{c.def("public", "isq_tx_retained")})
			isqColumnTypes(c.t, saved, false)
			c.all(held, "isq_tx_retained")
			c.run(peer, fixture.Exec("GRANT USAGE ON SEQUENCE isq_tx_retained TO isq_actor", 0))
			c.all(held, "isq_tx_retained", c.row("public", "isq_tx_retained", "bigint", 64, "9", "1", "9223372036854775807", "1", "NO"))
		}
	})
}
