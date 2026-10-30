package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoForeignDataWrappersLifecycle(t *testing.T) {
	fdwvScenario(t, func(t *testing.T, c *fdwvCase) {
		held := c.hold(c.conn, fdwvQuery)
		c.check(held, "fdwview_wrapper", nil)
		c.run(c.conn, fixture.Prepared(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdwview_wrapper NO HANDLER NO VALIDATOR OPTIONS(password 'private=value')", 0)))
		want := c.seedRows()
		c.check(held, "fdwview_wrapper", want)
		for _, q := range []string{
			"ALTER FOREIGN DATA WRAPPER fdwview_wrapper OPTIONS(ADD remote 'é=🙂')",
			"ALTER FOREIGN DATA WRAPPER fdwview_wrapper OPTIONS(SET remote 'changed')",
			"ALTER FOREIGN DATA WRAPPER fdwview_wrapper OPTIONS(DROP remote)",
			"ALTER FOREIGN DATA WRAPPER fdwview_wrapper NO HANDLER",
			"ALTER FOREIGN DATA WRAPPER fdwview_wrapper NO VALIDATOR",
			"CREATE SERVER fdwview_server FOREIGN DATA WRAPPER fdwview_wrapper",
			"CREATE USER MAPPING FOR PUBLIC SERVER fdwview_server OPTIONS(password 'mapping-secret')",
		} {
			c.exec(q)
			c.check(held, "fdwview_wrapper", want)
		}
		c.exec("CREATE ROLE fdwview_owner SUPERUSER", "CREATE ROLE fdwview_newowner SUPERUSER", "ALTER FOREIGN DATA WRAPPER fdwview_wrapper OWNER TO fdwview_owner")
		c.check(held, "fdwview_wrapper", [][]any{c.row("fdwview_wrapper", "fdwview_owner")})
		c.exec("ALTER FOREIGN DATA WRAPPER fdwview_wrapper OWNER TO fdwview_newowner")
		c.check(held, "fdwview_wrapper", [][]any{c.row("fdwview_wrapper", "fdwview_newowner")})
		c.exec("ALTER ROLE fdwview_newowner RENAME TO fdwview_renamedowner")
		c.check(held, "fdwview_wrapper", [][]any{c.row("fdwview_wrapper", "fdwview_renamedowner")})
		c.exec("ALTER FOREIGN DATA WRAPPER fdwview_wrapper RENAME TO fdwview_renamed")
		c.check(held, "fdwview_wrapper", nil)
		c.check(held, "fdwview_renamed", [][]any{c.row("fdwview_renamed", "fdwview_renamedowner")})
		c.exec("DROP FOREIGN DATA WRAPPER fdwview_renamed CASCADE")
		c.check(held, "fdwview_renamed", nil)

		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		for _, commit := range []bool{false, true} {
			tx, err := c.conn.BeginTx(c.p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			c.run(tx, fixture.Exec("CREATE FOREIGN DATA WRAPPER fdwview_tx", 0))
			c.check(tx, "fdwview_tx", [][]any{c.row("fdwview_tx", c.owner)})
			c.check(peer, "fdwview_tx", nil)
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			var rows [][]any
			if commit {
				rows = [][]any{c.row("fdwview_tx", c.owner)}
			}
			c.check(held, "fdwview_tx", rows)
			c.check(peer, "fdwview_tx", rows)
		}
		for _, commit := range []bool{false, true} {
			tx, err := c.conn.BeginTx(c.p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			c.run(tx, fixture.Exec("DROP FOREIGN DATA WRAPPER fdwview_tx", 0))
			c.check(tx, "fdwview_tx", nil)
			c.check(peer, "fdwview_tx", [][]any{c.row("fdwview_tx", c.owner)})
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			var rows [][]any
			if !commit {
				rows = [][]any{c.row("fdwview_tx", c.owner)}
			}
			c.check(held, "fdwview_tx", rows)
			c.check(peer, "fdwview_tx", rows)
		}
	})
}
