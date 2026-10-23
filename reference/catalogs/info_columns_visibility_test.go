package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnsVisibility(t *testing.T) {
	icScenario(t, func(c *icCase) {
		c.exec("CREATE ROLE ic_owner", "CREATE ROLE ic_reader", "CREATE ROLE ic_member INHERIT", "CREATE ROLE ic_set NOINHERIT", "CREATE ROLE ic_group INHERIT", "CREATE ROLE ic_super SUPERUSER", "GRANT ic_owner TO ic_member", "GRANT ic_owner TO ic_set WITH INHERIT FALSE, SET TRUE", "GRANT ic_reader TO ic_group",
			"CREATE TABLE ic_scope.vis(a integer,b integer,c integer)", "ALTER TABLE ic_scope.vis DROP COLUMN b", "ALTER TABLE ic_scope.vis OWNER TO ic_owner", "REVOKE ALL ON ic_scope.vis FROM PUBLIC,ic_owner", "GRANT USAGE ON SCHEMA ic_scope TO ic_reader")
		a, cc := c.integer("vis", "a", 1), c.integer("vis", "c", 3)
		where := "table_schema IN ('ic_scope','ic_private') AND table_name='vis'"
		held := c.hold(icProjection(c.b.PG, where, nil).Query)
		check := func(rows ...[]any) {
			c.t.Helper()
			s := icProjection(c.b.PG, where, rows)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(held, s)
		}
		check(a, cc) // bootstrap superuser
		c.exec("SET ROLE ic_owner")
		check(a, cc)
		c.exec("RESET ROLE", "SET ROLE ic_reader")
		check()
		for _, right := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
			c.exec("RESET ROLE", "GRANT "+right+"(c) ON ic_scope.vis TO ic_reader", "SET ROLE ic_reader")
			check(cc)
			if right == "INSERT" {
				d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table vis")
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("SELECT * FROM ic_scope.vis", d)
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					c.run(c.conn, s)
				}
			}
			c.exec("RESET ROLE", "REVOKE "+right+"(c) ON ic_scope.vis FROM ic_reader", "GRANT "+right+" ON ic_scope.vis TO ic_reader", "SET ROLE ic_reader")
			check(a, cc)
			c.exec("RESET ROLE", "REVOKE "+right+" ON ic_scope.vis FROM ic_reader", "SET ROLE ic_reader")
			check()
		}
		for _, right := range []string{"DELETE", "TRUNCATE", "TRIGGER", "MAINTAIN"} {
			c.exec("RESET ROLE", "GRANT "+right+" ON ic_scope.vis TO ic_reader", "SET ROLE ic_reader")
			check()
			c.exec("RESET ROLE", "REVOKE "+right+" ON ic_scope.vis FROM ic_reader")
		}
		c.exec("GRANT SELECT ON ic_scope.vis TO PUBLIC", "SET ROLE ic_reader")
		check(a, cc)
		c.exec("RESET ROLE", "REVOKE SELECT ON ic_scope.vis FROM PUBLIC", "SET ROLE ic_member")
		check(a, cc)
		c.exec("RESET ROLE", "SET ROLE ic_set")
		check()
		c.exec("SET ROLE ic_owner")
		check(a, cc)
		c.exec("RESET ROLE", "GRANT SELECT(c) ON ic_scope.vis TO ic_reader", "SET ROLE ic_group")
		check(cc)
		c.exec("RESET ROLE", "SET ROLE ic_super")
		check(a, cc)
		c.exec("RESET ROLE", "ALTER TABLE ic_scope.vis OWNER TO ic_reader", "REVOKE ALL ON ic_scope.vis FROM ic_reader", "SET ROLE ic_reader")
		check(a, cc)
		// Owner transitions rewrite ACL owner entries; after revoking the
		// reader's owner rights, moving ownership back does not restore the
		// SELECT(c) grant previously folded into its owner entry.
		c.exec("RESET ROLE", "ALTER TABLE ic_scope.vis OWNER TO ic_owner", "SET ROLE ic_reader")
		check()
		// Exercise the actual SELECT privilege independently of the metadata
		// predicate; has_column_privilege is not a supported SQL helper.
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT c FROM ic_scope.vis", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table vis"))
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			c.run(c.conn, s)
		}
		c.exec("RESET ROLE", "GRANT SELECT(c) ON ic_scope.vis TO ic_reader", "SET ROLE ic_reader")
		check(cc)
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT c FROM ic_scope.vis", []string{"c"}, []string{"INT4"}, nil)
			s.Prepared = prepared
			c.run(c.conn, s)
		}
		c.exec("RESET ROLE", "REVOKE SELECT(c) ON ic_scope.vis FROM ic_reader", "CREATE SCHEMA ic_private", "ALTER TABLE ic_scope.vis SET SCHEMA ic_private", "REVOKE ALL ON SCHEMA ic_private FROM PUBLIC,ic_reader", "GRANT INSERT ON ic_private.vis TO ic_reader", "SET ROLE ic_reader")
		a = icPatch(a, map[string]any{"table_schema": "ic_private"})
		cc = icPatch(cc, map[string]any{"table_schema": "ic_private"})
		check(a, cc) // schema USAGE is not a metadata predicate
		diag := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema ic_private")
		diag.Position = "15"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT * FROM ic_private.vis", diag)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			c.run(c.conn, s)
		}
		c.exec("RESET ROLE", "REVOKE INSERT ON ic_private.vis FROM ic_reader", "GRANT USAGE ON SCHEMA ic_private TO ic_reader", "SET ROLE ic_reader")
		check()
		c.exec("RESET ROLE", "GRANT SELECT ON ic_private.vis TO ic_reader", "ALTER TABLE ic_private.vis ENABLE ROW LEVEL SECURITY", "SET ROLE ic_reader")
		check(a, cc)
		c.run(c.conn, fixture.Select("SELECT * FROM ic_private.vis", []string{"a", "c"}, []string{"INT4", "INT4"}, nil))
		c.exec("RESET ROLE")
	})
}
