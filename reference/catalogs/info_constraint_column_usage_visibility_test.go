package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoConstraintColumnUsageVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ccu_owner", "ccu_target_owner", "ccu_child_owner", "ccu_reader", "ccu_inherit", "ccu_set"})
	fixture.SchemaReferenceCleanup(t, "ccu_private")
	ccuScenario(t, func(c *ccuCase) {
		c.exec("CREATE ROLE ccu_owner", "CREATE ROLE ccu_target_owner", "CREATE ROLE ccu_child_owner", "CREATE ROLE ccu_reader", "CREATE ROLE ccu_inherit INHERIT", "CREATE ROLE ccu_set NOINHERIT",
			"GRANT ccu_target_owner TO ccu_inherit", "GRANT ccu_target_owner TO ccu_set WITH INHERIT FALSE, SET TRUE",
			"CREATE TABLE ccu_vis_parent(a integer,b integer,CONSTRAINT ccu_vis_u UNIQUE(a,b))",
			"CREATE TABLE ccu_vis_child(x integer,y integer,CONSTRAINT ccu_vis_fk FOREIGN KEY(x,y) REFERENCES ccu_vis_parent(b,a))",
			"CREATE TABLE ccu_vis_owned(a integer,CONSTRAINT ccu_vis_owned_u UNIQUE(a))",
			"ALTER TABLE ccu_vis_owned OWNER TO ccu_owner", "ALTER TABLE ccu_vis_parent OWNER TO ccu_target_owner", "ALTER TABLE ccu_vis_child OWNER TO ccu_child_owner",
			"REVOKE ALL ON ccu_vis_parent,ccu_vis_child,ccu_vis_owned FROM PUBLIC", "REVOKE ALL ON ccu_vis_parent FROM ccu_target_owner", "REVOKE ALL ON ccu_vis_owned FROM ccu_owner", "GRANT USAGE ON SCHEMA public TO ccu_reader")
		target := [][]any{c.pub("ccu_vis_parent", "a", "ccu_vis_u"), c.pub("ccu_vis_parent", "b", "ccu_vis_u"), c.pub("ccu_vis_parent", "a", "ccu_vis_fk"), c.pub("ccu_vis_parent", "b", "ccu_vis_fk")}
		owned := c.pub("ccu_vis_owned", "a", "ccu_vis_owned_u")
		all := append(append([][]any(nil), target...), owned)
		held := c.hold(c.conn, ccuQuery)
		check := func(rows [][]any) { c.check(c.conn, "ccu_vis_%", rows...); c.check(held, "ccu_vis_%", rows...) }
		check(all)
		c.exec("SET ROLE ccu_owner")
		check([][]any{owned})
		c.exec("RESET ROLE", "SET ROLE ccu_target_owner")
		check(target)
		c.exec("RESET ROLE", "SET ROLE ccu_child_owner")
		check(nil)
		c.exec("RESET ROLE", "SET ROLE ccu_reader")
		check(nil)
		denied := fixture.ErrorDiagnostic("SELECT * FROM ccu_vis_parent", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table ccu_vis_parent"))
		denied.SelectRows = true
		denied.ErrorStage = "execute"
		c.run(c.conn, denied)
		c.run(c.conn, fixture.Prepared(denied))
		for _, right := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
			c.exec("RESET ROLE", "GRANT "+right+"(a) ON ccu_vis_parent TO ccu_reader", "SET ROLE ccu_reader")
			check(nil)
			c.exec("RESET ROLE", "REVOKE "+right+"(a) ON ccu_vis_parent FROM ccu_reader", "GRANT "+right+" ON ccu_vis_parent,ccu_vis_child TO ccu_reader", "SET ROLE ccu_reader")
			check(nil)
			c.exec("RESET ROLE", "REVOKE "+right+" ON ccu_vis_parent,ccu_vis_child FROM ccu_reader", "SET ROLE ccu_reader")
			check(nil)
		}
		c.exec("RESET ROLE", "GRANT SELECT ON ccu_vis_parent,ccu_vis_child TO PUBLIC", "SET ROLE ccu_reader")
		check(nil)
		c.exec("RESET ROLE", "REVOKE SELECT ON ccu_vis_parent,ccu_vis_child FROM PUBLIC", "SET ROLE ccu_inherit")
		check(target)
		c.exec("RESET ROLE", "REVOKE ccu_target_owner FROM ccu_inherit", "SET ROLE ccu_inherit")
		check(nil)
		c.exec("RESET ROLE", "GRANT ccu_target_owner TO ccu_inherit", "SET ROLE ccu_inherit")
		check(target)
		c.exec("RESET ROLE", "SET ROLE ccu_set")
		check(nil)
		c.exec("SET ROLE ccu_target_owner")
		check(target)
		c.exec("RESET ROLE", "SET ROLE ccu_set")
		check(nil)
		c.exec("RESET ROLE", "ALTER TABLE ccu_vis_parent OWNER TO ccu_child_owner", "SET ROLE ccu_target_owner")
		check(nil)
		c.exec("RESET ROLE", "SET ROLE ccu_child_owner")
		check(target)
		c.exec("RESET ROLE", "ALTER TABLE ccu_vis_parent OWNER TO ccu_target_owner", "CREATE SCHEMA ccu_private", "ALTER TABLE ccu_vis_parent SET SCHEMA ccu_private", "REVOKE ALL ON SCHEMA ccu_private FROM PUBLIC,ccu_inherit", "ALTER TABLE ccu_private.ccu_vis_parent ENABLE ROW LEVEL SECURITY", "SET ROLE ccu_inherit", "SET search_path TO pg_catalog")
		privateRows := [][]any{c.row("ccu_private", "ccu_vis_parent", "a", "ccu_private", "ccu_vis_u"), c.row("ccu_private", "ccu_vis_parent", "b", "ccu_private", "ccu_vis_u"), c.row("ccu_private", "ccu_vis_parent", "a", "public", "ccu_vis_fk"), c.row("ccu_private", "ccu_vis_parent", "b", "public", "ccu_vis_fk")}
		check(privateRows)
		c.exec("RESET ROLE", "RESET search_path")
		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer peer.Close()
		c.exec("CREATE TEMP TABLE ccu_temp_own(a integer CONSTRAINT ccu_temp_own_u UNIQUE)")
		c.run(peer, fixture.Exec("CREATE TEMP TABLE ccu_temp_peer(a integer CONSTRAINT ccu_temp_peer_u UNIQUE)", 0))
		defer peer.ExecContext(c.p.Ctx, "DROP TABLE IF EXISTS ccu_temp_peer")
		defer c.conn.ExecContext(c.p.Ctx, "DROP TABLE IF EXISTS ccu_temp_own")
		var ownSchema, peerSchema string
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE r.oid='ccu_temp_own'::regclass::oid").Scan(&ownSchema); err != nil {
			c.t.Fatal(err)
		}
		if err := peer.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE r.oid='ccu_temp_peer'::regclass::oid").Scan(&peerSchema); err != nil {
			c.t.Fatal(err)
		}
		if ownSchema == peerSchema {
			c.t.Fatal("TEMP namespaces must differ")
		}
		temps := [][]any{c.row(ownSchema, "ccu_temp_own", "a", ownSchema, "ccu_temp_own_u"), c.row(peerSchema, "ccu_temp_peer", "a", peerSchema, "ccu_temp_peer_u")}
		c.check(c.conn, "ccu_temp_%", temps...)
		c.check(peer, "ccu_temp_%", temps...)
	})
}
