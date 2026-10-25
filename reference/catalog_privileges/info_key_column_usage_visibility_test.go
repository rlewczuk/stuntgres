package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoKeyColumnUsageVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"kcu_owner", "kcu_reader", "kcu_inherit", "kcu_set", "kcu_grant_member"})
	fixture.SchemaReferenceCleanup(t, "kcu_private")
	kcuScenario(t, func(c *kcuCase) {
		t := c.t
		c.exec("CREATE ROLE kcu_owner", "CREATE ROLE kcu_reader", "CREATE ROLE kcu_inherit INHERIT", "CREATE ROLE kcu_set NOINHERIT", "CREATE ROLE kcu_grant_member INHERIT", "GRANT kcu_owner TO kcu_inherit", "GRANT kcu_owner TO kcu_set WITH INHERIT FALSE, SET TRUE", "GRANT kcu_reader TO kcu_grant_member",
			"CREATE TABLE kcu_vis_parent(a integer,b integer,CONSTRAINT kcu_vis_u UNIQUE(a,b))",
			"CREATE TABLE kcu_vis_child(x integer,y integer,CONSTRAINT kcu_vis_fk FOREIGN KEY(x,y) REFERENCES kcu_vis_parent(b,a))",
			"ALTER TABLE kcu_vis_parent OWNER TO kcu_owner", "ALTER TABLE kcu_vis_child OWNER TO kcu_owner", "REVOKE ALL ON kcu_vis_parent,kcu_vis_child FROM PUBLIC", "REVOKE ALL ON kcu_vis_parent,kcu_vis_child FROM kcu_owner")
		c.exec("GRANT USAGE ON SCHEMA public TO kcu_reader")
		all := [][]any{c.pub("kcu_vis_u", "kcu_vis_parent", "a", 1, nil), c.pub("kcu_vis_u", "kcu_vis_parent", "b", 2, nil), c.pub("kcu_vis_fk", "kcu_vis_child", "x", 1, int64(2)), c.pub("kcu_vis_fk", "kcu_vis_child", "y", 2, int64(1))}
		child := all[2:]
		partial := child[1:]
		held := c.hold(c.conn, kcuQuery)
		check := func(rows [][]any) { c.check(c.conn, "kcu_vis_%", rows...); c.check(held, "kcu_vis_%", rows...) }
		c.exec("SET ROLE kcu_owner")
		check(all)
		c.exec("RESET ROLE", "SET ROLE kcu_reader")
		check(nil)
		denied := fixture.ErrorDiagnostic("SELECT * FROM kcu_vis_child", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table kcu_vis_child"))
		denied.SelectRows = true
		denied.ErrorStage = "execute"
		c.run(c.conn, denied)
		for _, right := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
			c.exec("RESET ROLE", "GRANT "+right+"(y) ON kcu_vis_child TO kcu_reader", "SET ROLE kcu_reader")
			check(partial)
			c.exec("RESET ROLE", "REVOKE "+right+"(y) ON kcu_vis_child FROM kcu_reader", "GRANT "+right+" ON kcu_vis_child TO kcu_reader", "SET ROLE kcu_reader")
			check(child)
			c.exec("RESET ROLE", "REVOKE "+right+" ON kcu_vis_child FROM kcu_reader", "SET ROLE kcu_reader")
			check(nil)
		}
		for _, right := range []string{"DELETE", "TRUNCATE", "TRIGGER", "MAINTAIN"} {
			c.exec("RESET ROLE", "GRANT "+right+" ON kcu_vis_child TO kcu_reader", "SET ROLE kcu_reader")
			check(nil)
			c.exec("RESET ROLE", "REVOKE "+right+" ON kcu_vis_child FROM kcu_reader")
		}
		c.exec("GRANT SELECT ON kcu_vis_child TO PUBLIC", "SET ROLE kcu_reader")
		check(child)
		c.exec("RESET ROLE", "REVOKE SELECT ON kcu_vis_child FROM PUBLIC", "SET ROLE kcu_reader")
		check(nil)
		c.exec("RESET ROLE", "SET ROLE kcu_inherit")
		check(all)
		c.exec("RESET ROLE", "SET ROLE kcu_set")
		check(nil)
		c.exec("SET ROLE kcu_owner")
		check(all)
		c.exec("RESET ROLE", "SET ROLE kcu_set")
		check(nil)
		c.exec("RESET ROLE", "GRANT SELECT(y) ON kcu_vis_child TO kcu_reader", "SET ROLE kcu_grant_member")
		check(partial)
		c.exec("RESET ROLE", "REVOKE SELECT(y) ON kcu_vis_child FROM kcu_reader", "CREATE SCHEMA kcu_private", "ALTER TABLE kcu_vis_child SET SCHEMA kcu_private", "REVOKE USAGE ON SCHEMA public FROM kcu_reader", "REVOKE ALL ON SCHEMA kcu_private FROM PUBLIC,kcu_reader", "GRANT SELECT ON kcu_private.kcu_vis_child TO kcu_reader", "SET ROLE kcu_reader")
		privateRows := [][]any{c.row("kcu_private", "kcu_vis_fk", "kcu_vis_child", "x", 1, int64(2)), c.row("kcu_private", "kcu_vis_fk", "kcu_vis_child", "y", 2, int64(1))}
		check(privateRows)
		c.exec("RESET ROLE")
		peer, err := c.b.DB.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		c.exec("CREATE TEMP TABLE kcu_temp_own(a integer CONSTRAINT kcu_temp_own_p PRIMARY KEY)")
		c.run(peer, fixture.Exec("CREATE TEMP TABLE kcu_temp_peer(a integer CONSTRAINT kcu_temp_peer_p PRIMARY KEY)", 0))
		defer peer.ExecContext(c.p.Ctx, "DROP TABLE IF EXISTS kcu_temp_peer")
		defer c.conn.ExecContext(c.p.Ctx, "DROP TABLE IF EXISTS kcu_temp_own")
		var ownSchema, peerSchema string
		if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE r.oid='kcu_temp_own'::regclass::oid").Scan(&ownSchema); err != nil {
			t.Fatal(err)
		}
		if err := peer.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class r JOIN pg_namespace n ON n.oid=r.relnamespace WHERE r.oid='kcu_temp_peer'::regclass::oid").Scan(&peerSchema); err != nil {
			t.Fatal(err)
		}
		if ownSchema == peerSchema {
			t.Fatal("TEMP namespaces must differ")
		}
		c.check(c.conn, "kcu_temp_%", c.row(ownSchema, "kcu_temp_own_p", "kcu_temp_own", "a", 1, nil))
		c.check(peer, "kcu_temp_%", c.row(peerSchema, "kcu_temp_peer_p", "kcu_temp_peer", "a", 1, nil))
	})
}
