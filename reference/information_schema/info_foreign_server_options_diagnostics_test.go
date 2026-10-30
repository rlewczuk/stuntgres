package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoForeignServerOptionsLifecycleAndDiagnostics(t *testing.T) {
	fsopScenario(t, func(c *fsopCase) {
		c.seed()
		held := c.hold(c.conn, fsopQuery)
		initial := c.seedRows()
		for _, tc := range []struct{ query, state, message string }{
			{"ALTER SERVER fsop_server OPTIONS(SET host 'wrong', ADD password 'duplicate')", "42710", `option "password" provided more than once`},
			{"ALTER SERVER fsop_server OPTIONS(SET absent 'wrong')", "42704", `option "absent" not found`},
			{"ALTER SERVER fsop_server OPTIONS(DROP absent)", "42704", `option "absent" not found`},
			{"ALTER SERVER fsop_server OPTIONS(ADD \"x=y\" 'wrong')", "22023", `invalid option name "x=y": must not contain "="`},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
			s := fixture.ErrorDiagnostic(tc.query, d)
			s.ErrorStage = "execute"
			c.run(c.conn, s)
			c.check(held, "fsop_server", initial)
			c.run(c.conn, fixture.Prepared(s))
			c.check(held, "fsop_server", initial)
			mutation := c.hold(c.conn, tc.query)
			c.run(mutation, s)
			if err := mutation.Statement.Close(); err != nil {
				t.Fatal(err)
			}
			c.check(held, "fsop_server", initial)
			c.check(c.conn, "fsop_server", initial)
			c.run(c.conn, fixture.Prepared(c.step(fsopQuery, initial, "fsop_server")))
		}
		for _, tc := range []struct{ operation, query, message, action string }{
			{"insert", "INSERT INTO information_schema.foreign_server_options VALUES('catalog','wrapper','key','value')", `cannot insert into view "foreign_server_options"`, "inserting into the view"},
			{"update", "UPDATE information_schema.foreign_server_options SET option_value='wrong'", `cannot update view "foreign_server_options"`, "updating the view"},
			{"delete", "DELETE FROM information_schema.foreign_server_options", `cannot delete from view "foreign_server_options"`, "deleting from the view"},
		} {
			d := fixture.ControlsReferenceDiagnostic("55000", tc.message)
			d.Detail = "Views that return set-returning functions are not automatically updatable."
			upper := strings.ToUpper(tc.operation)
			d.Hint = "To enable " + tc.action + ", provide an INSTEAD OF " + upper + " trigger or an unconditional ON " + upper + " DO INSTEAD rule."
			s := fixture.ErrorDiagnostic(tc.query, d)
			s.ErrorStage = "execute"
			c.run(c.conn, s)
			s.ErrorStage = "prepare"
			c.run(c.conn, fixture.Prepared(s))
			c.check(held, "fsop_server", initial)
		}
		c.exec("CREATE ROLE fsop_denied", "SET SESSION AUTHORIZATION fsop_denied")
		unauthorized := fixture.ErrorDiagnostic("ALTER SERVER fsop_server OPTIONS(SET host 'forbidden')", fixture.ControlsReferenceDiagnostic("42501", "must be owner of foreign server fsop_server"))
		unauthorized.ErrorStage = "execute"
		c.run(c.conn, unauthorized)
		c.run(c.conn, fixture.Prepared(unauthorized))
		mutation := c.hold(c.conn, unauthorized.Query)
		c.run(mutation, unauthorized)
		if err := mutation.Statement.Close(); err != nil {
			t.Fatal(err)
		}
		c.exec("RESET SESSION AUTHORIZATION")
		c.check(held, "fsop_server", initial)
		c.check(c.conn, "fsop_server", initial)
		tx, err := c.conn.BeginTx(c.p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		c.run(tx, fixture.Exec("ALTER SERVER fsop_server OPTIONS(SET host 'transaction')", 0))
		transaction := c.seedRows()
		transaction[1][3] = "transaction"
		c.check(tx, "fsop_server", transaction)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		c.check(held, "fsop_server", initial)
		tx, err = c.conn.BeginTx(c.p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		c.run(tx, fixture.Exec("ALTER SERVER fsop_server OPTIONS(SET host 'committed')", 0))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		want := c.seedRows()
		want[1][3] = "committed"
		c.check(held, "fsop_server", want)
		c.exec("ALTER SERVER fsop_server OPTIONS(ADD secret 'full-secret',DROP empty)")
		want = [][]any{c.row("fsop_server", "host", "committed"), c.row("fsop_server", "password", "private=value"), c.row("fsop_server", "secret", "full-secret")}
		c.check(held, "fsop_server", want)
		c.exec("CREATE ROLE fsop_newowner", "GRANT USAGE ON FOREIGN DATA WRAPPER fsop_wrapper TO fsop_newowner", "ALTER SERVER fsop_server OWNER TO fsop_newowner", "ALTER SERVER fsop_server RENAME TO fsop_renamed")
		c.check(held, "fsop_server", nil)
		for _, row := range want {
			row[1] = "fsop_renamed"
		}
		c.check(held, "fsop_renamed", want)
		c.exec("DROP SERVER fsop_renamed")
		c.check(held, "fsop_renamed", nil)
		c.exec("CREATE SERVER fsop_server FOREIGN DATA WRAPPER fsop_wrapper OPTIONS(only 'last')")
		c.check(held, "fsop_server", [][]any{c.row("fsop_server", "only", "last")})
		c.exec("ALTER SERVER fsop_server OPTIONS(DROP only)")
		c.check(held, "fsop_server", nil)
		c.exec("ALTER SERVER fsop_server OPTIONS(ADD host 'cascade')")
		c.check(held, "fsop_server", [][]any{c.row("fsop_server", "host", "cascade")})
		c.exec("DROP FOREIGN DATA WRAPPER fsop_wrapper CASCADE")
		c.check(held, "fsop_server", nil)
	})
}
