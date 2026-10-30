package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoForeignTablesFeatureSmoke(t *testing.T) {
	iftScenario(t, func(c *iftCase) {
		held := c.hold(c.conn, iftQuery)
		c.check(held, "ift_table", nil)
		c.exec("CREATE FOREIGN DATA WRAPPER ift_wrapper NO HANDLER NO VALIDATOR", "CREATE SERVER ift_server FOREIGN DATA WRAPPER ift_wrapper", "CREATE SCHEMA ift_scope", "CREATE FOREIGN TABLE ift_scope.ift_table(a integer) SERVER ift_server", "CREATE ROLE ift_reader", "CREATE ROLE ift_denied", "REVOKE ALL ON SCHEMA ift_scope FROM PUBLIC", "REVOKE ALL ON FOREIGN SERVER ift_server FROM PUBLIC", "GRANT SELECT(a) ON ift_scope.ift_table TO ift_reader")
		want := c.seedRows()
		c.check(held, "ift_table", want)
		c.actor(held, "ift_reader", want)
		c.actor(held, "ift_denied", nil)
		c.exec("REVOKE SELECT(a) ON ift_scope.ift_table FROM ift_reader")
		c.actor(held, "ift_reader", nil)
		c.exec("GRANT SELECT(a) ON ift_scope.ift_table TO PUBLIC")
		c.actor(held, "ift_reader", want)
		c.exec("REVOKE SELECT(a) ON ift_scope.ift_table FROM PUBLIC", "GRANT SELECT(a) ON ift_scope.ift_table TO ift_reader", "ALTER FOREIGN TABLE ift_scope.ift_table RENAME TO ift_renamed", "ALTER SERVER ift_server RENAME TO ift_renamed_server")
		want = [][]any{c.row("ift_scope", "ift_renamed", "ift_renamed_server")}
		c.check(held, "ift_table", nil)
		c.check(held, "ift_renamed", want)
		c.exec("ALTER FOREIGN TABLE ift_scope.ift_renamed OPTIONS(ADD password 'sentinel')")
		c.check(held, "ift_renamed", want)
		c.missingColumn()
		s := fixture.ErrorDiagnostic("ALTER FOREIGN TABLE ift_scope.ift_renamed OPTIONS(SET password 'wrong',ADD password 'duplicate')", fixture.ControlsReferenceDiagnostic("42710", `option "password" provided more than once`))
		s.ErrorStage = "execute"
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		c.check(held, "ift_renamed", want)
		c.run(c.conn, fixture.Select("SELECT option_value FROM information_schema.foreign_table_options WHERE foreign_table_name='ift_renamed' AND option_name='password'", []string{"option_value"}, []string{"VARCHAR"}, [][]any{{"sentinel"}}))
		if !c.b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			old, err := held.Statement.QueryContext(c.p.Ctx, "ift_renamed")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
				t.Fatal(err)
			}
			// Replacement retires catalog-owned domain signatures, not returned rows.
			c.routes(iftQuery, nil, "ift_renamed")
			held = c.hold(c.conn, iftQuery)
			c.check(held, "ift_renamed", nil)
			c.consume(old, want)
			if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			c.routes(iftQuery, want, "ift_renamed")
			held = c.hold(c.conn, iftQuery)
			c.check(held, "ift_renamed", want)
			for _, identity := range []struct {
				role string
				rows [][]any
			}{{"ift_reader", want}, {"ift_denied", nil}} {
				c.exec("SET SESSION AUTHORIZATION " + identity.role)
				c.check(held, "ift_renamed", identity.rows)
				c.exec("RESET SESSION AUTHORIZATION")
			}
		}
		c.exec("DROP FOREIGN TABLE ift_scope.ift_renamed")
		c.check(held, "ift_renamed", nil)
	})
}
