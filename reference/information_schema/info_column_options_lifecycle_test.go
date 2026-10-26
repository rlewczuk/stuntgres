package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnOptionsLifecycle(t *testing.T) {
	copScenario(t, func(c *copCase) {
		c.seed()
		held := c.hold(c.conn, copQuery)
		want := c.seedRows()
		check := func(schema string) {
			c.check(c.conn, schema, want)
			c.run(c.conn, fixture.Prepared(c.step(copQuery, want, schema)))
			c.check(held, schema, want)
		}
		check("cop_scope")
		if !c.b.PG {
			old, err := c.conn.QueryContext(c.p.Ctx, copQuery, "cop_scope")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			c.exec("ALTER FOREIGN TABLE cop_scope.options ALTER COLUMN id OPTIONS(SET remote 'changed')")
			c.consume(old, c.seedRows())
		} else {
			c.exec("ALTER FOREIGN TABLE cop_scope.options ALTER COLUMN id OPTIONS(SET remote 'changed')")
		}
		want[1][5] = "changed"
		check("cop_scope")
		c.exec("ALTER FOREIGN TABLE cop_scope.options ALTER COLUMN id OPTIONS(ADD added 'new')")
		want = append([][]any{c.row("options", "id", "added", "new")}, want...)
		check("cop_scope")
		c.exec("ALTER FOREIGN TABLE cop_scope.options ALTER COLUMN id OPTIONS(DROP added)")
		want = want[1:]
		check("cop_scope")
		tx, err := c.conn.BeginTx(c.p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		c.run(tx, fixture.Exec("ALTER FOREIGN TABLE cop_scope.options ALTER COLUMN id OPTIONS(SET remote 'staged')", 0))
		c.run(tx, fixture.Exec("CREATE FOREIGN TABLE cop_scope.tx_added(a integer OPTIONS(secret 'transaction')) SERVER cop_server", 0))
		staged := c.seedRows()
		staged[1][5] = "staged"
		staged = append(staged, c.row("tx_added", "a", "secret", "transaction"))
		c.check(tx, "cop_scope", staged)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		check("cop_scope")
		c.exec("ALTER FOREIGN TABLE cop_scope.options ADD COLUMN tail text OPTIONS(secret 'survivor')", "ALTER FOREIGN TABLE cop_scope.options DROP COLUMN label", "ALTER FOREIGN TABLE cop_scope.options ADD COLUMN fresh text OPTIONS(remote 'after-gap')")
		want = [][]any{c.row("options", "fresh", "remote", "after-gap"), c.row("options", "id", "password", "visible=owner"), c.row("options", "id", "remote", "changed"), c.row("options", "tail", "secret", "survivor")}
		check("cop_scope")
		c.exec("ALTER FOREIGN TABLE cop_scope.options RENAME COLUMN tail TO renamed")
		want[3][3] = "renamed"
		check("cop_scope")
		c.exec("ALTER FOREIGN TABLE cop_scope.options RENAME TO renamed_table")
		for _, row := range want {
			row[2] = "renamed_table"
		}
		check("cop_scope")
		c.exec("CREATE SCHEMA cop_moved", "ALTER FOREIGN TABLE cop_scope.renamed_table SET SCHEMA cop_moved")
		for _, row := range want {
			row[1] = "cop_moved"
		}
		check("cop_moved")
		c.check(held, "cop_scope", nil)
		c.exec("DROP FOREIGN TABLE cop_moved.renamed_table")
		want = nil
		check("cop_moved")
	})
}
