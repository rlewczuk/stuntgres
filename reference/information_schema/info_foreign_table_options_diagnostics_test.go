package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoForeignTableOptionsDiagnostics(t *testing.T) {
	ftoScenario(t, func(c *ftoCase) {
		c.seed()
		held := c.hold(c.conn, ftoQuery)
		initial := c.seedRows()
		for _, tc := range []struct{ query, state, message string }{
			{"ALTER FOREIGN TABLE fto_scope.fto_table OPTIONS(SET remote 'wrong', ADD password 'duplicate')", "42710", `option "password" provided more than once`},
			{"ALTER FOREIGN TABLE fto_scope.fto_table OPTIONS(SET absent 'wrong')", "42704", `option "absent" not found`},
			{"ALTER FOREIGN TABLE fto_scope.fto_table OPTIONS(DROP absent)", "42704", `option "absent" not found`},
			{"ALTER FOREIGN TABLE fto_scope.fto_table OPTIONS(ADD \"x=y\" 'wrong')", "22023", `invalid option name "x=y": must not contain "="`},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
			s := fixture.ErrorDiagnostic(tc.query, d)
			s.ErrorStage = "execute"
			c.run(c.conn, s)
			c.check(held, "fto_table", initial)
			c.run(c.conn, fixture.Prepared(s))
			c.check(held, "fto_table", initial)
			mutation := c.hold(c.conn, tc.query)
			c.run(mutation, s)
			if err := mutation.Statement.Close(); err != nil {
				t.Fatal(err)
			}
			c.check(held, "fto_table", initial)
		}
		for _, tc := range []struct{ operation, query, message, action string }{
			{"insert", "INSERT INTO information_schema.foreign_table_options VALUES('catalog','schema','table','key','value')", `cannot insert into view "foreign_table_options"`, "inserting into the view"},
			{"update", "UPDATE information_schema.foreign_table_options SET option_value='wrong'", `cannot update view "foreign_table_options"`, "updating the view"},
			{"delete", "DELETE FROM information_schema.foreign_table_options", `cannot delete from view "foreign_table_options"`, "deleting from the view"},
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
			c.check(held, "fto_table", initial)
		}
		d := fixture.ControlsReferenceDiagnostic("42703", `column "absent" does not exist`)
		d.Position = fixture.Position(8)
		missing := fixture.ErrorDiagnostic("SELECT absent FROM information_schema.foreign_table_options", d)
		missing.SelectRows = true
		missing.ErrorStage = "execute"
		c.run(c.conn, missing)
		missing.ErrorStage = "prepare"
		c.run(c.conn, fixture.Prepared(missing))
	})
}
