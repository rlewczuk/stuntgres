package reference_test

import (
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func (c *iftCase) missingColumn() {
	c.t.Helper()
	for _, prefix := range []string{"", "/* é🙂 */ "} {
		d := fixture.ControlsReferenceDiagnostic("42703", `column "absent" does not exist`)
		d.Position = fixture.Position(utf8.RuneCountInString(prefix) + 8)
		s := fixture.ErrorDiagnostic(prefix+"SELECT absent FROM information_schema.foreign_tables", d)
		s.SelectRows = true
		s.ErrorStage = "execute"
		c.run(c.conn, s)
		s.ErrorStage = "prepare"
		c.run(c.conn, fixture.Prepared(s))
	}
}

func (c *iftCase) optionSentinel() {
	c.t.Helper()
	s := fixture.Select("SELECT option_name,option_value FROM information_schema.foreign_table_options WHERE foreign_table_schema='ift_scope' AND foreign_table_name='ift_table' ORDER BY option_name", []string{"option_name", "option_value"}, []string{"NAME", "VARCHAR"}, [][]any{{"empty", ""}, {"password", "visible=value"}, {"quoted", "a'b\\c  "}, {"remote", "é=🙂"}})
	c.run(c.conn, s)
	c.run(c.conn, fixture.Prepared(s))
}

func TestReferenceInfoForeignTablesDiagnostics(t *testing.T) {
	iftScenario(t, func(c *iftCase) {
		c.missingColumn()
		c.seed()
		c.missingColumn()
		c.exec("CREATE ROLE ift_denied", "SET SESSION AUTHORIZATION ift_denied")
		c.routes(iftQuery, nil, "ift_table")
		c.missingColumn()
		c.exec("RESET SESSION AUTHORIZATION", "CREATE TABLE ift_scope.ift_local(a integer)")
		held := c.hold(c.conn, iftQuery)
		for _, tc := range []struct{ query, state, message string }{
			{"ALTER FOREIGN TABLE ift_scope.ift_table OPTIONS(SET remote 'wrong', ADD password 'duplicate')", "42710", `option "password" provided more than once`},
			{"ALTER FOREIGN TABLE ift_scope.ift_table OPTIONS(SET remote 'wrong', SET absent 'wrong')", "42704", `option "absent" not found`},
			{"ALTER FOREIGN TABLE ift_scope.ift_table OPTIONS(SET remote 'wrong', DROP absent)", "42704", `option "absent" not found`},
			{"ALTER FOREIGN TABLE ift_scope.ift_table OPTIONS(SET remote 'wrong', ADD \"x=y\" 'wrong')", "22023", `invalid option name "x=y": must not contain "="`},
			{"CREATE FOREIGN TABLE ift_scope.ift_table(a integer) SERVER ift_server", "42P07", `relation "ift_table" already exists`},
			{"ALTER FOREIGN TABLE ift_scope.ift_local OPTIONS(ADD x 'wrong')", "42809", `"ift_local" is not a foreign table`},
			{"DROP FOREIGN TABLE ift_scope.ift_local", "42809", `"ift_local" is not a foreign table`},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
			if tc.query == "DROP FOREIGN TABLE ift_scope.ift_local" {
				d.Hint = "Use DROP TABLE to remove a table."
			}
			s := fixture.ErrorDiagnostic(tc.query, d)
			s.ErrorStage = "execute"
			c.run(c.conn, s)
			c.check(held, "ift_table", c.seedRows())
			c.optionSentinel()
			c.run(c.conn, fixture.Prepared(s))
			c.check(held, "ift_table", c.seedRows())
			c.optionSentinel()
			mutation := c.hold(c.conn, tc.query)
			c.run(mutation, s)
			if err := mutation.Statement.Close(); err != nil {
				t.Fatal(err)
			}
			c.check(held, "ift_table", c.seedRows())
			c.optionSentinel()
		}
	})
}
