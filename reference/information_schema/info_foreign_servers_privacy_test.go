package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoForeignServersPrivacy(t *testing.T) {
	fsvScenario(t, func(t *testing.T, c *fsvCase) {
		c.seed()
		c.exec("CREATE ROLE fsv_owner", "CREATE ROLE fsv_reader", "CREATE ROLE fsv_denied", "CREATE ROLE fsv_inherit", "GRANT USAGE ON FOREIGN DATA WRAPPER fsv_wrapper TO fsv_owner", "ALTER SERVER fsv_text OWNER TO fsv_owner", "GRANT fsv_owner TO fsv_inherit WITH INHERIT TRUE", "GRANT USAGE ON FOREIGN SERVER fsv_text TO fsv_reader",
			"CREATE USER MAPPING FOR PUBLIC SERVER fsv_text OPTIONS(password 'public-secret')", "CREATE USER MAPPING FOR fsv_owner SERVER fsv_text OPTIONS(password 'owner-secret')", "CREATE USER MAPPING FOR fsv_reader SERVER fsv_text OPTIONS(empty '',password 'reader-secret',tag 'é=🙂')", "CREATE USER MAPPING FOR fsv_denied SERVER fsv_text OPTIONS(password 'denied-secret')", "CREATE FOREIGN TABLE public.fsv_privacy(id integer) SERVER fsv_text OPTIONS(password 'table-secret')")
		c.owner = "fsv_owner"
		want := c.seedRows()
		held := c.hold(c.conn, fsvQuery)
		// The released helper admits all mappings of an admitted server, but the
		// option CASE discloses only the exact mapped identity or PUBLIC to owners.
		roles := []string{"PUBLIC", "fsv_denied", "fsv_owner", "fsv_reader", "fsv_reader", "fsv_reader"}
		names := []string{"password", "password", "password", "empty", "password", "tag"}
		values := []any{"public-secret", "denied-secret", "owner-secret", "", "reader-secret", "é=🙂"}
		check := func(role, disclose string, visible bool) {
			c.exec("SET SESSION AUTHORIZATION " + role)
			var selected, mappings, options [][]any
			if visible {
				selected = want
				for _, mapped := range []string{"PUBLIC", "fsv_denied", "fsv_owner", "fsv_reader"} {
					mappings = append(mappings, []any{mapped, c.database, "fsv_text"})
				}
				for i, mapped := range roles {
					var value any
					if mapped == role || disclose == mapped {
						value = values[i]
					}
					options = append(options, []any{mapped, c.database, "fsv_text", names[i], value})
				}
			}
			c.check(held, "fsv_text", selected)
			c.routes(fsvQuery, selected, "fsv_text")
			for _, s := range []fixture.Step{
				fixture.Select("SELECT * FROM information_schema.user_mappings WHERE foreign_server_name='fsv_text' ORDER BY authorization_identifier", []string{"authorization_identifier", "foreign_server_catalog", "foreign_server_name"}, []string{"NAME", "NAME", "NAME"}, mappings),
				fixture.Select("SELECT * FROM information_schema.user_mapping_options WHERE foreign_server_name='fsv_text' ORDER BY authorization_identifier,option_name", []string{"authorization_identifier", "foreign_server_catalog", "foreign_server_name", "option_name", "option_value"}, []string{"NAME", "NAME", "NAME", "NAME", "VARCHAR"}, options),
			} {
				c.run(c.conn, s)
				c.run(c.conn, fixture.Prepared(s))
			}
			c.exec("RESET SESSION AUTHORIZATION")
		}
		check("fsv_reader", "fsv_reader", true)
		check("fsv_owner", "PUBLIC", true)
		check("fsv_inherit", "PUBLIC", true)
		check("fsv_denied", "", false)
		c.exec("ALTER USER MAPPING FOR fsv_reader SERVER fsv_text OPTIONS(SET password 'changed')", "ALTER FOREIGN TABLE public.fsv_privacy OPTIONS(SET password 'changed-table')")
		values[4] = "changed"
		check("fsv_reader", "fsv_reader", true)
		c.exec("DROP USER MAPPING FOR fsv_denied SERVER fsv_text", "DROP FOREIGN TABLE public.fsv_privacy")
		c.routes(fsvQuery, want, "fsv_text")
		// After removal, use the exact remaining identity bag independently.
		c.run(c.conn, fixture.Select("SELECT authorization_identifier FROM information_schema.user_mappings WHERE foreign_server_name='fsv_text' ORDER BY authorization_identifier", []string{"authorization_identifier"}, []string{"NAME"}, [][]any{{"PUBLIC"}, {"fsv_owner"}, {"fsv_reader"}}))
		remaining := [][]any{
			{"PUBLIC", c.database, "fsv_text", "password", "public-secret"},
			{"fsv_owner", c.database, "fsv_text", "password", "owner-secret"},
			{"fsv_reader", c.database, "fsv_text", "empty", ""},
			{"fsv_reader", c.database, "fsv_text", "password", "changed"},
			{"fsv_reader", c.database, "fsv_text", "tag", "é=🙂"},
		}
		s := fixture.Select("SELECT * FROM information_schema.user_mapping_options WHERE foreign_server_name='fsv_text' ORDER BY authorization_identifier,option_name", []string{"authorization_identifier", "foreign_server_catalog", "foreign_server_name", "option_name", "option_value"}, []string{"NAME", "NAME", "NAME", "NAME", "VARCHAR"}, remaining)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
	})
}
