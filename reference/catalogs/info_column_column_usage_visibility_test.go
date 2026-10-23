package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoColumnColumnUsageVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"colcu_owner", "colcu_other", "colcu_reader", "colcu_inherit", "colcu_set", "colcu_super", "colcu_super_member"})
	fixture.SchemaReferenceCleanup(t, "colcu_private")
	colcuScenario(t, func(c *colcuCase) {
		c.exec("CREATE ROLE colcu_owner", "CREATE ROLE colcu_other", "CREATE ROLE colcu_reader", "CREATE ROLE colcu_inherit INHERIT", "CREATE ROLE colcu_set NOINHERIT", "CREATE ROLE colcu_super SUPERUSER", "CREATE ROLE colcu_super_member INHERIT",
			"GRANT colcu_owner TO colcu_inherit", "GRANT colcu_owner TO colcu_set WITH INHERIT FALSE, SET TRUE", "GRANT colcu_super TO colcu_super_member",
			"CREATE TABLE colcu_vis(a integer,b integer,g integer GENERATED ALWAYS AS(a+b) STORED)", "ALTER TABLE colcu_vis OWNER TO colcu_owner",
			"REVOKE ALL ON colcu_vis FROM PUBLIC,colcu_owner", "GRANT USAGE ON SCHEMA public TO colcu_reader")
		rows := [][]any{c.pub("colcu_vis", "a", "g"), c.pub("colcu_vis", "b", "g")}
		held := c.hold(c.conn, colcuQuery)
		check := func(want [][]any) { c.check(c.conn, "colcu_vis", want...); c.check(held, "colcu_vis", want...) }
		check(rows)
		for _, role := range []string{"colcu_owner", "colcu_inherit", "colcu_super"} {
			c.exec("SET ROLE " + role)
			check(rows)
			c.exec("RESET ROLE")
		}
		for _, role := range []string{"colcu_reader", "colcu_set", "colcu_super_member"} {
			c.exec("SET ROLE " + role)
			check(nil)
			c.exec("RESET ROLE")
		}
		for _, grant := range []string{"SELECT(a)", "SELECT"} {
			c.exec("GRANT "+grant+" ON colcu_vis TO colcu_reader", "SET ROLE colcu_reader")
			check(nil)
			c.exec("RESET ROLE", "REVOKE "+grant+" ON colcu_vis FROM colcu_reader")
		}
		c.exec("SET ROLE colcu_set", "SET ROLE colcu_owner")
		check(rows)
		c.exec("RESET ROLE", "REVOKE colcu_owner FROM colcu_inherit", "SET ROLE colcu_inherit")
		check(nil)
		c.exec("RESET ROLE", "GRANT colcu_owner TO colcu_inherit", "SET ROLE colcu_inherit")
		check(rows)
		if !c.b.PG {
			old, err := c.conn.QueryContext(c.p.Ctx, colcuQuery, "colcu_vis")
			if err != nil {
				c.t.Fatal(err)
			}
			defer old.Close()
			types := colcuMetadata(c.t, old, false)
			c.exec("RESET ROLE", "REVOKE colcu_owner FROM colcu_inherit", "SET ROLE colcu_inherit")
			check(nil)
			colcuAssertRows(c.t, old, rows)
			colcuColumnTypes(c.t, types, false)
			c.exec("RESET ROLE", "GRANT colcu_owner TO colcu_inherit")
		} else {
			c.exec("RESET ROLE")
		}
		c.exec("ALTER TABLE colcu_vis OWNER TO colcu_other", "SET ROLE colcu_owner")
		check(nil)
		c.exec("RESET ROLE", "SET ROLE colcu_other")
		check(rows)
		c.exec("RESET ROLE", "ALTER TABLE colcu_vis OWNER TO colcu_owner", "CREATE SCHEMA colcu_private", "ALTER TABLE colcu_vis SET SCHEMA colcu_private", "REVOKE ALL ON SCHEMA colcu_private FROM PUBLIC,colcu_inherit", "ALTER TABLE colcu_private.colcu_vis ENABLE ROW LEVEL SECURITY", "SET ROLE colcu_inherit", "SET search_path TO pg_catalog")
		private := [][]any{c.row("colcu_private", "colcu_vis", "a", "g"), c.row("colcu_private", "colcu_vis", "b", "g")}
		check(private)
		c.exec("RESET ROLE", "RESET search_path")
		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer peer.Close()
		c.exec("CREATE TEMP TABLE colcu_temp_own(a integer,g integer GENERATED ALWAYS AS(a) STORED)")
		defer c.conn.ExecContext(c.p.Ctx, "DROP TABLE IF EXISTS colcu_temp_own")
		c.run(peer, fixture.Exec("CREATE TEMP TABLE colcu_temp_peer(a integer,g integer GENERATED ALWAYS AS(a) STORED)", 0))
		defer peer.ExecContext(c.p.Ctx, "DROP TABLE IF EXISTS colcu_temp_peer")
		var ownSchema, peerSchema string
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE r.oid='colcu_temp_own'::regclass::oid").Scan(&ownSchema); err != nil {
			c.t.Fatal(err)
		}
		if err := peer.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE r.oid='colcu_temp_peer'::regclass::oid").Scan(&peerSchema); err != nil {
			c.t.Fatal(err)
		}
		if ownSchema == peerSchema {
			c.t.Fatal("TEMP namespaces coincide")
		}
		temps := [][]any{c.row(ownSchema, "colcu_temp_own", "a", "g"), c.row(peerSchema, "colcu_temp_peer", "a", "g")}
		c.check(c.conn, "colcu_temp_%", temps...)
		c.check(peer, "colcu_temp_%", temps...)
	})
}
