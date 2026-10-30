package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoForeignTablesLifecycle(t *testing.T) {
	iftScenario(t, func(c *iftCase) {
		held := c.hold(c.conn, iftQuery)
		check := func(table string, want [][]any) {
			c.routes(iftQuery, want, table)
			c.check(held, table, want)
		}
		check("ift_table", nil)
		c.seed()
		initial := c.seedRows()
		check("ift_table", initial)
		if !c.b.PG {
			old, err := held.Statement.QueryContext(c.p.Ctx, "ift_table")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			c.exec("ALTER FOREIGN TABLE ift_scope.ift_table RENAME TO ift_renamed")
			c.consume(old, initial)
		} else {
			c.exec("ALTER FOREIGN TABLE ift_scope.ift_table RENAME TO ift_renamed")
		}
		check("ift_table", nil)
		want := [][]any{c.row("ift_scope", "ift_renamed", "ift_server")}
		check("ift_renamed", want)
		c.exec("CREATE SCHEMA ift_moved", "ALTER FOREIGN TABLE ift_scope.ift_renamed SET SCHEMA ift_moved", "ALTER SERVER ift_server RENAME TO ift_renamed_server")
		want = [][]any{c.row("ift_moved", "ift_renamed", "ift_renamed_server")}
		check("ift_renamed", want)
		c.exec("ALTER FOREIGN TABLE ift_moved.ift_renamed OPTIONS(SET remote 'changed',ADD token 'secret',DROP empty)")
		check("ift_renamed", want)
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
			c.run(tx, fixture.Exec("CREATE FOREIGN TABLE ift_scope.ift_private(a integer) SERVER ift_renamed_server", 0))
			private := [][]any{c.row("ift_scope", "ift_private", "ift_renamed_server")}
			c.check(tx, "ift_private", private)
			c.check(other, "ift_private", nil)
			c.run(tx, fixture.Exec("SAVEPOINT ift_save", 0))
			c.run(tx, fixture.Exec("ALTER FOREIGN TABLE ift_scope.ift_private RENAME TO ift_temporary", 0))
			c.check(tx, "ift_private", nil)
			c.check(tx, "ift_temporary", [][]any{c.row("ift_scope", "ift_temporary", "ift_renamed_server")})
			c.run(tx, fixture.Exec("DROP FOREIGN TABLE ift_scope.ift_temporary", 0))
			c.check(tx, "ift_temporary", nil)
			c.run(tx, fixture.Exec("ROLLBACK TO SAVEPOINT ift_save", 0))
			c.check(tx, "ift_private", private)
			if commit {
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				check("ift_private", private)
			} else {
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				check("ift_private", nil)
			}
			check("ift_renamed", want)
		}
		c.exec("CREATE ROLE ift_owner", "CREATE ROLE ift_newowner", "CREATE ROLE ift_inherit", "GRANT ift_newowner TO ift_inherit WITH INHERIT TRUE, SET TRUE", "GRANT USAGE ON FOREIGN SERVER ift_renamed_server TO ift_owner,ift_newowner", "GRANT CREATE ON SCHEMA ift_moved TO ift_owner,ift_newowner", "ALTER FOREIGN TABLE ift_moved.ift_renamed OWNER TO ift_owner", "SET SESSION AUTHORIZATION ift_owner")
		c.check(held, "ift_renamed", want)
		c.exec("RESET SESSION AUTHORIZATION", "ALTER FOREIGN TABLE ift_moved.ift_renamed OWNER TO ift_newowner", "SET SESSION AUTHORIZATION ift_owner")
		c.check(held, "ift_renamed", nil)
		c.exec("RESET SESSION AUTHORIZATION")
		for _, role := range []string{"ift_newowner", "ift_inherit"} {
			c.exec("SET SESSION AUTHORIZATION " + role)
			c.check(held, "ift_renamed", want)
			c.exec("RESET SESSION AUTHORIZATION")
		}
		c.exec("DROP FOREIGN TABLE ift_moved.ift_renamed")
		check("ift_renamed", nil)
		check("ift_private", [][]any{c.row("ift_scope", "ift_private", "ift_renamed_server")})
		c.exec("CREATE SERVER ift_second_server FOREIGN DATA WRAPPER ift_wrapper", "CREATE FOREIGN TABLE ift_scope.ift_survivor(a integer) SERVER ift_second_server", "DROP SERVER ift_renamed_server CASCADE")
		check("ift_private", nil)
		check("ift_survivor", [][]any{c.row("ift_scope", "ift_survivor", "ift_second_server")})
		c.exec("DROP FOREIGN DATA WRAPPER ift_wrapper CASCADE")
		check("ift_survivor", nil)
	})
}
