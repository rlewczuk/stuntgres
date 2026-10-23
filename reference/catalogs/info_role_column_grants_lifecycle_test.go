package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleColumnGrantsLifecycle(t *testing.T) {
	t.Run("held_peer_mutations", func(t *testing.T) {
		rcgScenario(t, func(c *rcgCase) {
			c.seed()
			peer := c.peer()
			c.exec("SET SESSION AUTHORIZATION rcg_r")
			held := c.hold(c.conn, rcgQuery)
			c.check(held, "rcg_t", "rcg_r")
			c.run(peer, fixture.Exec("SET ROLE rcg_o", 0))
			for _, phase := range []struct{ sql, option string }{
				{"GRANT SELECT(a) ON rcg_t TO rcg_r", "NO"},
				{"GRANT SELECT(a) ON rcg_t TO rcg_r WITH GRANT OPTION", "YES"},
				{"REVOKE GRANT OPTION FOR SELECT(a) ON rcg_t FROM rcg_r", "NO"},
				{"REVOKE SELECT(a) ON rcg_t FROM rcg_r", ""},
				{"GRANT SELECT(a) ON rcg_t TO rcg_r", "NO"},
			} {
				c.run(peer, fixture.Exec(phase.sql, 0))
				var want [][]any
				if phase.option != "" {
					want = [][]any{c.r("a", "SELECT", phase.option)}
				}
				c.check(held, "rcg_t", "rcg_r", want...)
				c.check(c.conn, "rcg_t", "rcg_r", want...)
			}
			c.exec("RESET SESSION AUTHORIZATION")
			c.run(peer, fixture.Exec("RESET ROLE", 0))
		})
	})
	t.Run("stable_attribute_and_relation_identity", func(t *testing.T) {
		rcgScenario(t, func(c *rcgCase) {
			c.seed()
			c.exec("CREATE SCHEMA rcg_moved", "SET ROLE rcg_o", "GRANT SELECT(a) ON rcg_t TO rcg_r", "GRANT UPDATE ON rcg_t TO rcg_r", "RESET ROLE", "ALTER TABLE rcg_t RENAME COLUMN a TO renamed")
			c.check(c.conn, "rcg_t", "rcg_r", c.r("renamed", "SELECT", "NO"), c.r("renamed", "UPDATE", "NO"), c.r("b", "UPDATE", "NO"))
			c.exec("ALTER TABLE rcg_t DROP COLUMN renamed", "ALTER TABLE rcg_t ADD COLUMN a integer")
			c.check(c.conn, "rcg_t", "rcg_r", c.r("b", "UPDATE", "NO"), c.r("a", "UPDATE", "NO"))
			c.exec("ALTER TABLE rcg_t RENAME TO rcg_later", "ALTER TABLE rcg_later SET SCHEMA rcg_moved")
			c.check(c.conn, "rcg_t", "rcg_r")
			c.check(c.conn, "rcg_later", "rcg_r", c.row("rcg_o", "rcg_r", "rcg_moved", "rcg_later", "a", "UPDATE", "NO"), c.row("rcg_o", "rcg_r", "rcg_moved", "rcg_later", "b", "UPDATE", "NO"))
			c.exec("DROP TABLE rcg_moved.rcg_later", "CREATE TABLE rcg_moved.rcg_later(a integer,b integer)", "ALTER TABLE rcg_moved.rcg_later OWNER TO rcg_o")
			c.check(c.conn, "rcg_later", "rcg_r")
		})
	})
	t.Run("four_masks_independent_origins", func(t *testing.T) {
		rcgScenario(t, func(c *rcgCase) {
			c.seed()
			for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
				c.exec("SET ROLE rcg_o", "GRANT "+privilege+" ON rcg_t TO rcg_r", "GRANT "+privilege+"(a) ON rcg_t TO rcg_r WITH GRANT OPTION", "RESET ROLE")
				c.check(c.conn, "rcg_t", "rcg_r", c.r("a", privilege, "NO"), c.r("a", privilege, "YES"), c.r("b", privilege, "NO"))
				c.exec("SET ROLE rcg_o", "REVOKE "+privilege+"(a) ON rcg_t FROM rcg_r", "RESET ROLE")
				c.check(c.conn, "rcg_t", "rcg_r", c.r("a", privilege, "NO"), c.r("b", privilege, "NO"))
				c.exec("SET ROLE rcg_o", "GRANT "+privilege+"(a) ON rcg_t TO rcg_r WITH GRANT OPTION", "REVOKE "+privilege+" ON rcg_t FROM rcg_r", "RESET ROLE")
				// PostgreSQL table revocation also removes corresponding column rights.
				c.check(c.conn, "rcg_t", "rcg_r")
				c.exec("SET ROLE rcg_o", "GRANT "+privilege+"(a) ON rcg_t TO rcg_r", "RESET ROLE")
				c.check(c.conn, "rcg_t", "rcg_r", c.r("a", privilege, "NO"))
				c.exec("SET ROLE rcg_o", "REVOKE "+privilege+"(a) ON rcg_t FROM rcg_r", "RESET ROLE")
				c.check(c.conn, "rcg_t", "rcg_r")
			}
		})
	})
}

func TestReferenceInfoRoleColumnGrantsRetained(t *testing.T) {
	t.Run("identity_and_acl_frontier", func(t *testing.T) {
		rcgScenario(t, func(c *rcgCase) {
			c.seed()
			c.exec("SET ROLE rcg_o", "GRANT SELECT(a) ON rcg_t TO rcg_g", "RESET ROLE", "GRANT rcg_g TO rcg_m WITH INHERIT TRUE", "SET SESSION AUTHORIZATION rcg_m")
			held := c.hold(c.conn, rcgQuery)
			old := c.row("rcg_o", "rcg_g", "public", "rcg_t", "a", "SELECT", "NO")
			c.check(held, "rcg_t", "rcg_g", old)
			peer := c.peer()
			var retained *sql.Rows
			var saved []*sql.ColumnType
			if !c.b.PG {
				var err error
				retained, err = held.QueryContext(c.p.Ctx, rcgQuery, "rcg_t", "rcg_g")
				if err != nil {
					c.t.Fatal(err)
				}
				c.t.Cleanup(func() { retained.Close() })
				saved = rcgMetadata(c.t, retained, false)
			}
			c.run(peer, fixture.Exec("ALTER TABLE rcg_t RENAME COLUMN a TO renamed", 0))
			c.run(peer, fixture.Exec("REVOKE rcg_g FROM rcg_m", 0))
			if retained != nil {
				rcgConsume(c.t, retained, false, [][]any{old})
				rcgColumnTypes(c.t, saved, false)
			}
			c.check(held, "rcg_t", "rcg_g")
			c.run(peer, fixture.Exec("GRANT rcg_g TO rcg_m WITH INHERIT TRUE", 0))
			c.check(held, "rcg_t", "rcg_g", c.row("rcg_o", "rcg_g", "public", "rcg_t", "renamed", "SELECT", "NO"))
			c.run(peer, fixture.Exec("SET ROLE rcg_o", 0))
			c.run(peer, fixture.Exec("REVOKE SELECT(renamed) ON rcg_t FROM rcg_g", 0))
			c.check(held, "rcg_t", "rcg_g")
		})
	})
	t.Run("owner_baseline_controls", func(t *testing.T) {
		rcgScenario(t, func(c *rcgCase) {
			c.exec("CREATE TABLE rcg_control(a integer)")
			want := c.baseline(c.owner, "rcg_control", "a")
			c.check(c.conn, "rcg_control", c.owner, want...)
			if !c.b.PG {
				rows, err := c.conn.QueryContext(c.p.Ctx, rcgQuery, "rcg_control", c.owner)
				if err != nil {
					c.t.Fatal(err)
				}
				c.t.Cleanup(func() { rows.Close() })
				var dump bytes.Buffer
				if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
					c.t.Fatal(err)
				}
				if dump.Len() == 0 {
					c.t.Fatal("empty native dump")
				}
				if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
					c.t.Fatal(err)
				}
				c.check(c.conn, "rcg_control", c.owner)
				if err := stuntgres.Restore(c.p.Ctx, c.p.Native, &dump); err != nil {
					c.t.Fatal(err)
				}
				c.check(c.conn, "rcg_control", c.owner, want...)
				rcgConsume(c.t, rows, false, want)
			}
		})
	})
}
