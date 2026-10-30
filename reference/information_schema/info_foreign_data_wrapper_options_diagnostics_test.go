package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoForeignDataWrapperOptionsLifecycleAndDiagnostics(t *testing.T) {
	fdwoScenario(t, func(c *fdwoCase) {
		c.seed()
		held := c.hold(c.conn, fdwoQuery)
		initial := c.seedRows()
		for _, tc := range []struct{ query, state, message string }{
			{"ALTER FOREIGN DATA WRAPPER fdwo_wrapper OPTIONS(SET remote 'wrong', ADD password 'duplicate')", "42710", `option "password" provided more than once`},
			{"ALTER FOREIGN DATA WRAPPER fdwo_wrapper OPTIONS(SET absent 'wrong')", "42704", `option "absent" not found`},
			{"ALTER FOREIGN DATA WRAPPER fdwo_wrapper OPTIONS(DROP absent)", "42704", `option "absent" not found`},
			{"ALTER FOREIGN DATA WRAPPER fdwo_wrapper OPTIONS(ADD \"x=y\" 'wrong')", "22023", `invalid option name "x=y": must not contain "="`},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
			s := fixture.ErrorDiagnostic(tc.query, d)
			s.ErrorStage = "execute"
			c.run(c.conn, s)
			c.check(held, "fdwo_wrapper", initial)
			c.run(c.conn, fixture.Prepared(s))
			c.check(held, "fdwo_wrapper", initial)
			mutation := c.hold(c.conn, tc.query)
			c.run(mutation, s)
			if err := mutation.Statement.Close(); err != nil { t.Fatal(err) }
			c.check(held, "fdwo_wrapper", initial)
		}
		for _, tc := range []struct{ operation, query, message, action string }{
			{"insert", "INSERT INTO information_schema.foreign_data_wrapper_options VALUES('catalog','wrapper','key','value')", `cannot insert into view "foreign_data_wrapper_options"`, "inserting into the view"},
			{"update", "UPDATE information_schema.foreign_data_wrapper_options SET option_value='wrong'", `cannot update view "foreign_data_wrapper_options"`, "updating the view"},
			{"delete", "DELETE FROM information_schema.foreign_data_wrapper_options", `cannot delete from view "foreign_data_wrapper_options"`, "deleting from the view"},
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
			c.check(held, "fdwo_wrapper", initial)
		}
		tx, err := c.conn.BeginTx(c.p.Ctx, nil)
		if err != nil { t.Fatal(err) }
		defer tx.Rollback()
		c.run(tx, fixture.Exec("ALTER FOREIGN DATA WRAPPER fdwo_wrapper OPTIONS(SET remote 'transaction')", 0))
		transaction := c.seedRows()
		transaction[2][3] = "transaction"
		c.check(tx, "fdwo_wrapper", transaction)
		if err := tx.Rollback(); err != nil { t.Fatal(err) }
		c.check(held, "fdwo_wrapper", initial)
		tx, err = c.conn.BeginTx(c.p.Ctx, nil)
		if err != nil { t.Fatal(err) }
		defer tx.Rollback()
		c.run(tx, fixture.Exec("ALTER FOREIGN DATA WRAPPER fdwo_wrapper OPTIONS(SET remote 'committed')", 0))
		if err := tx.Commit(); err != nil { t.Fatal(err) }
		want := c.seedRows()
		want[2][3] = "committed"
		c.check(held, "fdwo_wrapper", want)
		c.exec("ALTER FOREIGN DATA WRAPPER fdwo_wrapper OPTIONS(ADD secret 'full-secret',DROP empty)")
		want = [][]any{c.row("fdwo_wrapper", "password", "private=value"), c.row("fdwo_wrapper", "remote", "committed"), c.row("fdwo_wrapper", "secret", "full-secret")}
		c.check(held, "fdwo_wrapper", want)
		c.exec("CREATE ROLE fdwo_newowner SUPERUSER", "ALTER FOREIGN DATA WRAPPER fdwo_wrapper OWNER TO fdwo_newowner", "ALTER FOREIGN DATA WRAPPER fdwo_wrapper RENAME TO fdwo_renamed")
		c.check(held, "fdwo_wrapper", nil)
		for _, row := range want { row[1] = "fdwo_renamed" }
		c.check(held, "fdwo_renamed", want)
		c.exec("DROP FOREIGN DATA WRAPPER fdwo_renamed")
		c.check(held, "fdwo_renamed", nil)
	})
}
