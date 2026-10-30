package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoForeignServersLifecycle(t *testing.T) {
	fsvScenario(t, func(t *testing.T, c *fsvCase) {
		held := c.hold(c.conn, fsvQuery)
		c.check(held, "fsv_text", nil)
		c.wrapper()
		c.run(c.conn, fixture.Prepared(fixture.Exec("CREATE SERVER fsv_text TYPE 'é=🙂' VERSION 'before' FOREIGN DATA WRAPPER fsv_wrapper OPTIONS(password 'server-secret')", 0)))
		want := c.seedRows()
		c.check(held, "fsv_text", want)
		c.exec("ALTER SERVER fsv_text VERSION 'after'")
		c.check(held, "fsv_text", [][]any{c.row("fsv_text", "fsv_wrapper", c.owner, "é=🙂", "after")})
		c.exec("ALTER SERVER fsv_text VERSION NULL")
		want = [][]any{c.row("fsv_text", "fsv_wrapper", c.owner, "é=🙂", nil)}
		c.check(held, "fsv_text", want)
		for _, q := range []string{
			"ALTER SERVER fsv_text OPTIONS(ADD remote 'remote')", "ALTER SERVER fsv_text OPTIONS(SET remote 'changed')", "ALTER SERVER fsv_text OPTIONS(DROP remote)",
			"CREATE USER MAPPING FOR PUBLIC SERVER fsv_text OPTIONS(password 'mapping-secret')", "ALTER USER MAPPING FOR PUBLIC SERVER fsv_text OPTIONS(SET password 'changed')", "DROP USER MAPPING FOR PUBLIC SERVER fsv_text",
			"CREATE FOREIGN TABLE public.fsv_table(id integer) SERVER fsv_text OPTIONS(remote 'table')", "ALTER FOREIGN TABLE public.fsv_table OPTIONS(SET remote 'changed')", "DROP FOREIGN TABLE public.fsv_table",
		} {
			c.exec(q)
			c.check(held, "fsv_text", want)
		}
		c.exec("CREATE ROLE fsv_newowner", "GRANT USAGE ON FOREIGN DATA WRAPPER fsv_wrapper TO fsv_newowner", "ALTER SERVER fsv_text OWNER TO fsv_newowner")
		c.check(held, "fsv_text", [][]any{c.row("fsv_text", "fsv_wrapper", "fsv_newowner", "é=🙂", nil)})
		c.exec("ALTER ROLE fsv_newowner RENAME TO fsv_renamedowner", "ALTER FOREIGN DATA WRAPPER fsv_wrapper RENAME TO fsv_renamed_wrapper", "ALTER SERVER fsv_text RENAME TO fsv_renamed")
		c.check(held, "fsv_text", nil)
		c.check(held, "fsv_renamed", [][]any{c.row("fsv_renamed", "fsv_renamed_wrapper", "fsv_renamedowner", "é=🙂", nil)})
		c.exec("DROP SERVER fsv_renamed")
		c.check(held, "fsv_renamed", nil)
		c.exec("CREATE SERVER fsv_renamed FOREIGN DATA WRAPPER fsv_renamed_wrapper")
		c.check(held, "fsv_renamed", [][]any{c.row("fsv_renamed", "fsv_renamed_wrapper", c.owner, nil, nil)})
		c.exec("DROP FOREIGN DATA WRAPPER fsv_renamed_wrapper CASCADE")
		c.check(held, "fsv_renamed", nil)
		c.wrapper()
		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		for _, action := range []string{"create", "alter", "drop"} {
			before := [][]any(nil)
			if action != "create" {
				before = [][]any{c.row("fsv_tx", "fsv_wrapper", c.owner, nil, nil)}
			}
			after := [][]any{c.row("fsv_tx", "fsv_wrapper", c.owner, nil, nil)}
			query := "CREATE SERVER fsv_tx FOREIGN DATA WRAPPER fsv_wrapper"
			if action == "alter" {
				query = "ALTER SERVER fsv_tx VERSION 'private'"
				after = [][]any{c.row("fsv_tx", "fsv_wrapper", c.owner, nil, "private")}
			}
			if action == "drop" {
				query = "DROP SERVER fsv_tx"
				after = nil
			}
			for _, commit := range []bool{false, true} {
				tx, err := c.conn.BeginTx(c.p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				c.run(tx, fixture.Exec(query, 0))
				c.check(tx, "fsv_tx", after)
				c.check(peer, "fsv_tx", before)
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				expected := before
				if commit {
					expected = after
				}
				c.check(peer, "fsv_tx", expected)
				c.check(held, "fsv_tx", expected)
			}
			if action == "alter" {
				c.exec("ALTER SERVER fsv_tx VERSION NULL")
			}
		}
	})
}
