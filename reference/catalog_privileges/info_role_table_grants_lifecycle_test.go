package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleTableGrantsLifecycle(t *testing.T) {
	t.Run("held_peer_mutations", func(t *testing.T) {
		rtgScenario(t, func(c *rtgCase) {
			c.seed()
			peer := c.peer()
			c.exec("SET SESSION AUTHORIZATION rtg_r")
			held := c.hold(c.conn, rtgQuery)
			c.check(held, "rtg_t", "rtg_r")
			c.run(peer, fixture.Exec("SET ROLE rtg_o", 0))
			for _, phase := range []struct{ sql, option string }{
				{"GRANT SELECT ON rtg_t TO rtg_r", "NO"},
				{"GRANT SELECT ON rtg_t TO rtg_r WITH GRANT OPTION", "YES"},
				{"REVOKE GRANT OPTION FOR SELECT ON rtg_t FROM rtg_r", "NO"},
				{"REVOKE SELECT ON rtg_t FROM rtg_r", ""},
			} {
				c.run(peer, fixture.Exec(phase.sql, 0))
				var want [][]any
				if phase.option != "" {
					want = [][]any{c.r("SELECT", phase.option)}
				}
				c.check(held, "rtg_t", "rtg_r", want...)
				c.check(c.conn, "rtg_t", "rtg_r", want...)
			}
			c.exec("RESET SESSION AUTHORIZATION")
			c.run(peer, fixture.Exec("GRANT SELECT ON rtg_t TO rtg_g", 0))
			c.run(peer, fixture.Exec("RESET ROLE", 0))
			c.exec("SET SESSION AUTHORIZATION rtg_m")
			row := c.row("rtg_o", "rtg_g", "public", "rtg_t", "SELECT", "NO")
			c.check(held, "rtg_t", "rtg_g")
			c.run(peer, fixture.Exec("GRANT rtg_g TO rtg_m WITH INHERIT TRUE", 0))
			c.check(held, "rtg_t", "rtg_g", row)
			c.check(c.conn, "rtg_t", "rtg_g", row)
			c.run(peer, fixture.Exec("REVOKE rtg_g FROM rtg_m", 0))
			c.check(held, "rtg_t", "rtg_g")
			c.check(c.conn, "rtg_t", "rtg_g")
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("rename_move_drop_recreate", func(t *testing.T) {
		rtgScenario(t, func(c *rtgCase) {
			c.seed()
			c.exec("CREATE SCHEMA rtg_moved", "SET ROLE rtg_o", "GRANT SELECT ON rtg_t TO rtg_r", "RESET ROLE")
			c.check(c.conn, "rtg_t", "rtg_r", c.r("SELECT", "NO"))
			c.exec("ALTER TABLE rtg_t RENAME TO rtg_later", "ALTER TABLE rtg_later SET SCHEMA rtg_moved")
			c.check(c.conn, "rtg_t", "rtg_r")
			c.check(c.conn, "rtg_later", "rtg_r", c.row("rtg_o", "rtg_r", "rtg_moved", "rtg_later", "SELECT", "NO"))
			c.exec("DROP TABLE rtg_moved.rtg_later", "CREATE TABLE rtg_moved.rtg_later(a integer)", "ALTER TABLE rtg_moved.rtg_later OWNER TO rtg_o")
			c.check(c.conn, "rtg_later", "rtg_r")
		})
	})
}

func TestReferenceInfoRoleTableGrantsRetained(t *testing.T) {
	t.Run("identity_acl_membership_frontier", func(t *testing.T) {
		rtgScenario(t, func(c *rtgCase) {
			c.seed()
			c.exec("SET ROLE rtg_o", "GRANT SELECT ON rtg_t TO rtg_g", "RESET ROLE", "GRANT rtg_g TO rtg_m WITH INHERIT TRUE", "SET SESSION AUTHORIZATION rtg_m")
			held := c.hold(c.conn, rtgQuery)
			old := c.row("rtg_o", "rtg_g", "public", "rtg_t", "SELECT", "NO")
			c.check(held, "rtg_t", "rtg_g", old)
			peer := c.peer()
			var retained *sql.Rows
			var saved []*sql.ColumnType
			if !c.b.PG {
				var err error
				retained, err = held.QueryContext(c.p.Ctx, rtgQuery, "rtg_t", "rtg_g")
				if err != nil {
					c.t.Fatal(err)
				}
				c.t.Cleanup(func() { retained.Close() })
				saved = rtgMetadata(c.t, retained, false)
			}
			for _, q := range []string{"ALTER TABLE rtg_t RENAME TO rtg_later", "REVOKE rtg_g FROM rtg_m", "SET ROLE rtg_o", "GRANT SELECT ON rtg_later TO rtg_g WITH GRANT OPTION", "RESET ROLE"} {
				c.run(peer, fixture.Exec(q, 0))
			}
			if retained != nil {
				rtgConsume(c.t, retained, false, [][]any{old})
				rtgColumnTypes(c.t, saved, false)
			}
			c.check(held, "rtg_t", "rtg_g")
			c.check(held, "rtg_later", "rtg_g")
			c.run(peer, fixture.Exec("GRANT rtg_g TO rtg_m WITH INHERIT TRUE", 0))
			c.check(held, "rtg_later", "rtg_g", c.row("rtg_o", "rtg_g", "public", "rtg_later", "SELECT", "YES"))
			c.run(peer, fixture.Exec("SET ROLE rtg_o", 0))
			c.run(peer, fixture.Exec("REVOKE SELECT ON rtg_later FROM rtg_g", 0))
			c.check(held, "rtg_later", "rtg_g")
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("owner_baseline_controls_and_cancel", func(t *testing.T) {
		rtgScenario(t, func(c *rtgCase) {
			c.exec("CREATE TABLE rtg_control(a integer)")
			want := c.baseline(c.owner, "rtg_control")
			c.check(c.conn, "rtg_control", c.owner, want...)
			if !c.b.PG {
				rows, err := c.conn.QueryContext(c.p.Ctx, rtgQuery, "rtg_control", c.owner)
				if err != nil {
					c.t.Fatal(err)
				}
				c.t.Cleanup(func() { rows.Close() })
				saved := rtgMetadata(c.t, rows, false)
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
				c.check(c.conn, "rtg_control", c.owner)
				if err := stuntgres.Restore(c.p.Ctx, c.p.Native, &dump); err != nil {
					c.t.Fatal(err)
				}
				c.check(c.conn, "rtg_control", c.owner, want...)
				rtgConsume(c.t, rows, false, want)
				rtgColumnTypes(c.t, saved, false)
				held := c.hold(c.conn, rtgQuery)
				ctx, cancel := context.WithCancel(c.p.Ctx)
				cancel()
				for _, e := range []fixture.Executor{c.conn, held} {
					r, err := e.QueryContext(ctx, rtgQuery, "rtg_control", c.owner)
					if r != nil {
						r.Close()
						c.t.Fatal("canceled query returned rows")
					}
					if !errors.Is(err, context.Canceled) {
						c.t.Fatalf("canceled query: %v", err)
					}
				}
				c.check(c.conn, "rtg_control", c.owner, want...)
			}
		})
	})
}
