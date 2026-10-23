package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoColumnColumnUsageLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "colcu_moved")
	colcuScenario(t, func(c *colcuCase) {
		c.exec("CREATE TABLE colcu_life(a integer,b integer,g integer GENERATED ALWAYS AS(a) STORED)")
		held := c.hold(c.conn, colcuQuery)
		check := func(schema, table, base, dep string) {
			var rows [][]any
			if base != "" {
				rows = [][]any{c.row(schema, table, base, dep)}
			}
			c.check(c.conn, "colcu_life%", rows...)
			c.check(held, "colcu_life%", rows...)
		}
		check("public", "colcu_life", "a", "g")
		var retainedRows func()
		if !c.b.PG {
			r, err := c.conn.QueryContext(c.p.Ctx, colcuQuery, "colcu_life%")
			if err != nil {
				c.t.Fatal(err)
			}
			defer r.Close()
			types := colcuMetadata(c.t, r, false)
			retainedRows = func() {
				colcuAssertRows(c.t, r, [][]any{c.pub("colcu_life", "a", "g")})
				colcuColumnTypes(c.t, types, false)
			}
		}
		c.exec("ALTER TABLE colcu_life RENAME COLUMN a TO aa")
		check("public", "colcu_life", "aa", "g")
		c.exec("ALTER TABLE colcu_life RENAME COLUMN g TO gg")
		check("public", "colcu_life", "aa", "gg")
		c.exec("ALTER TABLE colcu_life RENAME TO colcu_life_new", "CREATE SCHEMA colcu_moved", "ALTER TABLE colcu_life_new SET SCHEMA colcu_moved")
		check("colcu_moved", "colcu_life_new", "aa", "gg")
		c.exec("ALTER TABLE colcu_moved.colcu_life_new ALTER gg SET EXPRESSION AS(b+1)")
		check("colcu_moved", "colcu_life_new", "b", "gg")
		c.exec("ALTER TABLE colcu_moved.colcu_life_new ALTER gg DROP EXPRESSION")
		check("colcu_moved", "colcu_life_new", "", "")
		if retainedRows != nil {
			retainedRows()
		}
		c.exec("CREATE TABLE colcu_drop(a integer,b integer,g integer GENERATED ALWAYS AS(a) STORED,h integer GENERATED ALWAYS AS(b) STORED)")
		rows := [][]any{c.pub("colcu_drop", "a", "g"), c.pub("colcu_drop", "b", "h")}
		d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column a of table colcu_drop because other objects depend on it")
		d.Detail = "column g of table colcu_drop depends on column a of table colcu_drop"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("ALTER TABLE colcu_drop DROP COLUMN a RESTRICT", d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			c.run(c.conn, s)
			c.check(c.conn, "colcu_drop", rows...)
			c.check(held, "colcu_drop", rows...)
			c.run(c.conn, fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='colcu_drop'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"a"}, {"b"}, {"g"}, {"h"}}))
			c.run(c.conn, fixture.Select("SELECT adnum FROM pg_attrdef WHERE adrelid='colcu_drop'::regclass::oid ORDER BY adnum", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(3)}, {int64(4)}}))
		}
		c.exec("ALTER TABLE colcu_drop DROP COLUMN a CASCADE")
		c.check(held, "colcu_drop", c.pub("colcu_drop", "b", "h"))
		c.exec("DROP TABLE colcu_drop")
		c.check(held, "colcu_drop")
		c.exec("CREATE TABLE colcu_drop(a integer,b integer,g integer GENERATED ALWAYS AS(b) STORED)")
		c.check(held, "colcu_drop", c.pub("colcu_drop", "b", "g"))
		c.exec("CREATE TABLE colcu_copy(LIKE colcu_drop INCLUDING GENERATED)", "CREATE TABLE colcu_child() INHERITS(colcu_drop)")
		c.check(c.conn, "colcu_copy", c.pub("colcu_copy", "b", "g"))
		c.check(c.conn, "colcu_child", c.pub("colcu_child", "b", "g"))
		c.exec("DROP TABLE colcu_child", "DROP TABLE colcu_copy", "DROP TABLE colcu_drop", "DROP TABLE colcu_moved.colcu_life_new")
		c.check(held, "colcu_%")
	})
}

func TestReferenceInfoColumnColumnUsageTransactions(t *testing.T) {
	colcuScenario(t, func(c *colcuCase) {
		c.exec("CREATE TABLE colcu_tx(a integer,b integer,g integer GENERATED ALWAYS AS(a) STORED)")
		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer peer.Close()
		peerHeld := c.hold(peer, colcuQuery)
		defer peerHeld.Statement.Close()
		checkPeer := func(base string) {
			rows := [][]any{c.pub("colcu_tx", base, "g")}
			// These read only catalog metadata, never the exclusively locked
			// user relation. Each autocommit read gets the committed frontier.
			c.check(peer, "colcu_tx", rows...)
			c.check(peerHeld, "colcu_tx", rows...)
		}
		held := c.hold(c.conn, colcuQuery)
		check := func(base string) {
			var rows [][]any
			if base != "" {
				rows = [][]any{c.pub("colcu_tx", base, "g")}
			}
			c.check(c.conn, "colcu_tx", rows...)
			c.check(held, "colcu_tx", rows...)
		}
		check("a")
		checkPeer("a")
		c.exec("BEGIN", "ALTER TABLE colcu_tx ALTER g SET EXPRESSION AS(b+1)")
		check("b")
		checkPeer("a")
		c.exec("ROLLBACK")
		check("a")
		checkPeer("a")
		c.exec("BEGIN", "SAVEPOINT colcu_before_drop", "ALTER TABLE colcu_tx ALTER g DROP EXPRESSION")
		check("")
		checkPeer("a")
		c.exec("ROLLBACK TO colcu_before_drop")
		check("a")
		checkPeer("a")
		c.exec("ALTER TABLE colcu_tx ALTER g SET EXPRESSION AS(b+1)")
		check("b")
		checkPeer("a")
		c.exec("COMMIT")
		check("b")
		checkPeer("b")
		c.exec("BEGIN", "ALTER TABLE colcu_tx DROP COLUMN b CASCADE")
		check("")
		checkPeer("b")
		c.exec("ROLLBACK")
		check("b")
		checkPeer("b")
	})
}
