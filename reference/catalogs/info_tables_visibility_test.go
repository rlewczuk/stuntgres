package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoTablesVisibility(t *testing.T) {
	for _, right := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "MAINTAIN", "SELECT(a)", "INSERT(a)", "UPDATE(a)", "REFERENCES(a)", "SCHEMA", "NONE", "DROPPED"} {
		t.Run(right, func(t *testing.T) {
			itScenario(t, func(c *itCase) {
				c.exec("CREATE TABLE it_scope.allowed(a integer,b integer)", "CREATE TABLE it_scope.denied(a integer)", "CREATE ROLE it_reader")
				visible := right != "MAINTAIN" && right != "SCHEMA" && right != "NONE" && right != "DROPPED"
				switch right {
				case "SCHEMA":
					c.exec("GRANT USAGE ON SCHEMA it_scope TO it_reader")
				case "NONE":
				case "DROPPED":
					c.exec("GRANT SELECT(b) ON it_scope.allowed TO it_reader", "ALTER TABLE it_scope.allowed DROP COLUMN b")
				default:
					c.exec("GRANT " + right + " ON it_scope.allowed TO it_reader")
				}
				held := c.hold("SELECT * FROM information_schema.tables WHERE table_schema='it_scope'")
				c.exec("SET SESSION AUTHORIZATION it_reader")
				var want [][]any
				if visible {
					want = [][]any{c.row("allowed", "BASE TABLE", "YES")}
				}
				s := itStep(c.b.PG, held.SQL, want)
				c.run(c.conn, s)
				c.run(c.conn, fixture.Prepared(s))
				c.run(held, s)
				if right == "SELECT" {
					d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema it_scope")
					d.Position = "15"
					for _, prepared := range []bool{false, true} {
						e := fixture.ErrorDiagnostic("SELECT * FROM it_scope.allowed", d)
						e.SelectRows, e.Prepared, e.ErrorStage = true, prepared, "execute"
						if prepared {
							e.ErrorStage = "prepare"
						}
						c.run(c.conn, e)
					}
					// The failed ordinary lookup must not redact or hide the metadata identity.
					c.run(held, s)
				}
				c.exec("RESET SESSION AUTHORIZATION")
				if visible {
					c.exec("REVOKE "+right+" ON it_scope.allowed FROM it_reader", "SET SESSION AUTHORIZATION it_reader")
					c.run(held, itStep(c.b.PG, held.SQL, nil))
					c.exec("RESET SESSION AUTHORIZATION")
				}
			})
		})
	}
	t.Run("authority_transitions", func(t *testing.T) {
		itScenario(t, func(c *itCase) {
			c.exec("CREATE ROLE it_owner", "CREATE ROLE it_reader", "CREATE ROLE it_member", "CREATE ROLE it_set", "CREATE ROLE it_noinherit NOINHERIT", "CREATE ROLE it_other", "CREATE ROLE it_newowner", "CREATE ROLE it_super SUPERUSER", "CREATE TABLE it_scope.owned(a integer)", "ALTER TABLE it_scope.owned OWNER TO it_owner", "CREATE TABLE it_scope.granted(a integer)", "CREATE TABLE it_scope.public_t(a integer)", "CREATE TABLE it_scope.denied(a integer)", "GRANT SELECT ON it_scope.granted TO it_reader", "GRANT SELECT ON it_scope.public_t TO PUBLIC", "GRANT it_owner,it_reader TO it_member", "GRANT it_owner TO it_set WITH INHERIT FALSE, SET TRUE", "GRANT it_owner TO it_noinherit")
			// Enabled ownership discloses identity even without any ordinary ACL right.
			c.exec("REVOKE ALL ON it_scope.owned FROM it_owner")
			q := "SELECT * FROM information_schema.tables WHERE table_schema='it_scope'"
			held := c.hold(q)
			check := func(role string, names ...string) {
				c.exec("SET SESSION AUTHORIZATION " + role)
				var want [][]any
				for _, name := range names {
					want = append(want, c.row(name, "BASE TABLE", "YES"))
				}
				s := itStep(c.b.PG, q, want)
				c.run(c.conn, s)
				c.run(c.conn, fixture.Prepared(s))
				c.run(held, s)
				c.exec("RESET SESSION AUTHORIZATION")
			}
			check("it_owner", "owned", "public_t")
			check("it_member", "owned", "granted", "public_t")
			check("it_reader", "granted", "public_t")
			check("it_other", "public_t")
			check("it_set", "public_t")
			check("it_noinherit", "public_t")
			check("it_super", "owned", "granted", "public_t", "denied")
			for _, role := range []string{"it_set", "it_noinherit"} {
				c.exec("SET SESSION AUTHORIZATION "+role, "SET ROLE it_owner")
				c.run(held, itStep(c.b.PG, q, [][]any{c.row("owned", "BASE TABLE", "YES"), c.row("public_t", "BASE TABLE", "YES")}))
				c.exec("RESET ROLE", "RESET SESSION AUTHORIZATION")
			}
			c.exec("REVOKE it_owner FROM it_member")
			check("it_member", "granted", "public_t")
			c.exec("GRANT it_owner TO it_member")
			check("it_member", "owned", "granted", "public_t")
			c.exec("ALTER TABLE it_scope.owned OWNER TO it_newowner")
			check("it_owner", "public_t")
			check("it_newowner", "owned", "public_t")
			for _, predefined := range []string{"pg_read_all_data", "pg_write_all_data"} {
				c.exec("GRANT " + predefined + " TO it_other")
				check("it_other", "owned", "granted", "public_t", "denied")
				c.exec("REVOKE " + predefined + " FROM it_other")
			}
			check("it_other", "public_t")
		})
	})
	t.Run("search_path_not_visibility", func(t *testing.T) {
		itScenario(t, func(c *itCase) {
			c.exec("CREATE SCHEMA it_private", "CREATE TABLE it_scope.same(a integer)", "CREATE TABLE it_private.same(a integer)", "CREATE TABLE it_private.hidden(a integer)", "CREATE ROLE it_reader", "GRANT SELECT ON it_scope.same,it_private.same TO it_reader", "SET SESSION AUTHORIZATION it_reader")
			q := "SELECT * FROM information_schema.tables WHERE table_schema IN ('it_scope','it_private')"
			want := [][]any{c.row("same", "BASE TABLE", "YES"), itRow(c.database, "it_private", "same", "BASE TABLE", "YES")}
			held := c.hold(q)
			for _, path := range []string{"it_scope,it_private", "it_private,it_scope", "pg_catalog"} {
				c.exec("SET search_path=" + path)
				s := itStep(c.b.PG, q, want)
				c.run(c.conn, s)
				c.run(c.conn, fixture.Prepared(s))
				c.run(held, s)
			}
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
}
