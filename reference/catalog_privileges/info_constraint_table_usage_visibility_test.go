package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoConstraintTableUsageVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ctu_owner", "ctu_target_owner", "ctu_child_owner", "ctu_reader", "ctu_inherit", "ctu_set"})
	fixture.SchemaReferenceCleanup(t, "ctu_private")
	ctuScenario(t, func(c *ctuCase) {
		c.exec("CREATE ROLE ctu_owner", "CREATE ROLE ctu_target_owner", "CREATE ROLE ctu_child_owner", "CREATE ROLE ctu_reader", "CREATE ROLE ctu_inherit INHERIT", "CREATE ROLE ctu_set NOINHERIT",
			"GRANT ctu_target_owner TO ctu_inherit", "GRANT ctu_target_owner TO ctu_set WITH INHERIT FALSE, SET TRUE",
			"CREATE TABLE ctu_vis_parent(a integer,b integer,CONSTRAINT ctu_vis_u UNIQUE(a,b))",
			"CREATE TABLE ctu_vis_child(x integer,y integer,CONSTRAINT ctu_vis_fk FOREIGN KEY(x,y) REFERENCES ctu_vis_parent(b,a))",
			"CREATE TABLE ctu_vis_owned(a integer,CONSTRAINT ctu_vis_owned_u UNIQUE(a))",
			"ALTER TABLE ctu_vis_owned OWNER TO ctu_owner", "ALTER TABLE ctu_vis_parent OWNER TO ctu_target_owner", "ALTER TABLE ctu_vis_child OWNER TO ctu_child_owner",
			"REVOKE ALL ON ctu_vis_parent,ctu_vis_child,ctu_vis_owned FROM PUBLIC", "REVOKE ALL ON ctu_vis_parent FROM ctu_target_owner", "REVOKE ALL ON ctu_vis_owned FROM ctu_owner", "GRANT USAGE ON SCHEMA public TO ctu_reader")
		target := [][]any{c.pub("ctu_vis_parent", "ctu_vis_u"), c.pub("ctu_vis_parent", "ctu_vis_fk")}
		owned := c.pub("ctu_vis_owned", "ctu_vis_owned_u")
		all := append(append([][]any(nil), target...), owned)
		held := c.hold(c.conn, ctuQuery)
		check := func(rows [][]any) { c.routes(held, "ctu_vis_%", rows...) }
		check(all)
		actor := func(role string, rows [][]any) {
			c.exec("RESET ROLE", "SET ROLE "+role)
			c.run(c.conn, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{role}}))
			check(rows)
		}
		actor("ctu_owner", [][]any{owned})
		actor("ctu_target_owner", target)
		actor("ctu_child_owner", nil)
		actor("ctu_reader", nil)
		denied := fixture.ErrorDiagnostic("SELECT * FROM public.ctu_vis_parent", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table ctu_vis_parent"))
		denied.SelectRows, denied.ErrorStage = true, "execute"
		c.run(c.conn, denied)
		c.run(c.conn, fixture.Prepared(denied))
		check(nil)
		for _, right := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
			c.exec("RESET ROLE", "GRANT "+right+"(a) ON ctu_vis_parent TO ctu_reader")
			actor("ctu_reader", nil)
			c.exec("RESET ROLE", "REVOKE "+right+"(a) ON ctu_vis_parent FROM ctu_reader", "GRANT "+right+" ON ctu_vis_parent,ctu_vis_child TO ctu_reader")
			actor("ctu_reader", nil)
			c.exec("RESET ROLE", "REVOKE "+right+" ON ctu_vis_parent,ctu_vis_child FROM ctu_reader")
			actor("ctu_reader", nil)
		}
		c.exec("RESET ROLE", "GRANT SELECT ON ctu_vis_parent,ctu_vis_child TO PUBLIC")
		actor("ctu_reader", nil)
		c.exec("RESET ROLE", "REVOKE SELECT ON ctu_vis_parent,ctu_vis_child FROM PUBLIC")
		actor("ctu_inherit", target)
		c.exec("RESET ROLE", "REVOKE ctu_target_owner FROM ctu_inherit")
		actor("ctu_inherit", nil)
		c.exec("RESET ROLE", "GRANT ctu_target_owner TO ctu_inherit")
		actor("ctu_inherit", target)
		actor("ctu_set", nil)
		c.exec("SET ROLE ctu_target_owner")
		c.run(c.conn, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"ctu_target_owner"}}))
		check(target)
		actor("ctu_set", nil)
		c.exec("RESET ROLE", "ALTER TABLE ctu_vis_parent OWNER TO ctu_child_owner")
		actor("ctu_target_owner", nil)
		actor("ctu_child_owner", target)
		c.exec("RESET ROLE", "ALTER TABLE ctu_vis_parent OWNER TO ctu_target_owner", "CREATE SCHEMA ctu_private", "ALTER TABLE ctu_vis_parent SET SCHEMA ctu_private", "REVOKE ALL ON SCHEMA ctu_private FROM PUBLIC,ctu_inherit", "ALTER TABLE ctu_private.ctu_vis_parent ENABLE ROW LEVEL SECURITY", "SET search_path TO pg_catalog")
		privateRows := [][]any{c.row("ctu_private", "ctu_vis_parent", "ctu_private", "ctu_vis_u"), c.row("ctu_private", "ctu_vis_parent", "public", "ctu_vis_fk")}
		actor("ctu_inherit", privateRows)
		c.exec("RESET ROLE", "RESET search_path")
	})
}

func TestReferenceInfoConstraintTableUsageTemporary(t *testing.T) {
	ctuScenario(t, func(c *ctuCase) {
		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			c.t.Fatal(err)
		}
		defer peer.Close()
		c.exec("CREATE TEMP TABLE ctu_temp_own(a integer CONSTRAINT ctu_temp_own_u UNIQUE)")
		defer c.conn.ExecContext(c.p.Ctx, "DROP TABLE IF EXISTS ctu_temp_own")
		c.run(peer, fixture.Exec("CREATE TEMP TABLE ctu_temp_peer(a integer CONSTRAINT ctu_temp_peer_u UNIQUE)", 0))
		defer peer.ExecContext(c.p.Ctx, "DROP TABLE IF EXISTS ctu_temp_peer")
		var ownSchema, peerSchema string
		query := "SELECT n.nspname FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE r.oid=$1::regclass::oid"
		if err := c.conn.QueryRowContext(c.p.Ctx, query, "ctu_temp_own").Scan(&ownSchema); err != nil {
			c.t.Fatal(err)
		}
		if err := peer.QueryRowContext(c.p.Ctx, query, "ctu_temp_peer").Scan(&peerSchema); err != nil {
			c.t.Fatal(err)
		}
		if ownSchema == peerSchema {
			c.t.Fatal("TEMP namespaces must differ")
		}
		want := [][]any{c.row(ownSchema, "ctu_temp_own", ownSchema, "ctu_temp_own_u"), c.row(peerSchema, "ctu_temp_peer", peerSchema, "ctu_temp_peer_u")}
		c.routes(c.hold(c.conn, ctuQuery), "ctu_temp_%", want...)
		c.check(peer, "ctu_temp_%", want...)
		c.check(c.hold(peer, ctuQuery), "ctu_temp_%", want...)
	})
}
