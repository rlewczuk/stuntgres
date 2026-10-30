package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoForeignTableOptionsLifecycle(t *testing.T) {
	ftoScenario(t, func(c *ftoCase) {
		c.seed()
		held := c.hold(c.conn, ftoQuery)
		initial := c.seedRows()
		check := func(table string, want [][]any) {
			c.routes(ftoQuery, want, table)
			c.check(held, table, want)
		}
		check("fto_table", initial)
		if !c.b.PG {
			old, err := held.Statement.QueryContext(c.p.Ctx, "fto_table")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			c.exec("ALTER FOREIGN TABLE fto_scope.fto_table OPTIONS(SET remote 'changed')")
			c.consume(old, initial)
		} else {
			c.exec("ALTER FOREIGN TABLE fto_scope.fto_table OPTIONS(SET remote 'changed')")
		}
		changed := c.seedRows()
		changed[3][4] = "changed"
		check("fto_table", changed)
		c.exec("ALTER FOREIGN TABLE fto_scope.fto_table OPTIONS(ADD token 'full-token',DROP empty)")
		want := append(changed[1:], c.row("fto_table", "token", "full-token"))
		check("fto_table", want)
		other, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		for _, commit := range []bool{false, true} {
			tx, err := c.conn.BeginTx(c.p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			c.run(tx, fixture.Exec("ALTER FOREIGN TABLE fto_scope.fto_table OPTIONS(SET remote 'private')", 0))
			private := [][]any{c.row("fto_table", "password", "visible=value"), c.row("fto_table", "quoted", "a'b\\c  "), c.row("fto_table", "remote", "private"), c.row("fto_table", "token", "full-token")}
			c.check(tx, "fto_table", private)
			c.check(other, "fto_table", want)
			if commit {
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				want = private
			} else if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			check("fto_table", want)
		}
		c.exec("ALTER FOREIGN TABLE fto_scope.fto_table RENAME TO fto_renamed")
		check("fto_table", nil)
		for _, row := range want {
			row[2] = "fto_renamed"
		}
		check("fto_renamed", want)
		c.exec("CREATE SCHEMA fto_moved", "ALTER FOREIGN TABLE fto_scope.fto_renamed SET SCHEMA fto_moved")
		for _, row := range want {
			row[1] = "fto_moved"
		}
		check("fto_renamed", want)
		c.exec("CREATE ROLE fto_owner", "CREATE ROLE fto_newowner", "GRANT USAGE ON FOREIGN SERVER fto_server TO fto_owner,fto_newowner", "GRANT CREATE ON SCHEMA fto_moved TO fto_owner,fto_newowner", "ALTER FOREIGN TABLE fto_moved.fto_renamed OWNER TO fto_owner", "SET SESSION AUTHORIZATION fto_owner")
		c.check(held, "fto_renamed", want)
		c.exec("RESET SESSION AUTHORIZATION", "ALTER FOREIGN TABLE fto_moved.fto_renamed OWNER TO fto_newowner", "SET SESSION AUTHORIZATION fto_owner")
		c.check(held, "fto_renamed", nil)
		c.exec("RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION fto_newowner")
		c.check(held, "fto_renamed", want)
		c.exec("RESET SESSION AUTHORIZATION", "DROP FOREIGN TABLE fto_moved.fto_renamed")
		check("fto_renamed", nil)
	})
}
