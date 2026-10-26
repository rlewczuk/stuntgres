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

func TestReferenceInfoTablePrivilegesLifecycle(t *testing.T) {
	t.Run("held_peer_mutations", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			c.seed()
			peer := c.peer()
			c.exec("SET SESSION AUTHORIZATION tp_r")
			held := c.hold(c.conn, tpQuery)
			c.check(held, "tp_t", "tp_r")
			c.run(peer, fixture.Exec("SET ROLE tp_o", 0))
			for _, phase := range []struct{ sql, option string }{
				{"GRANT SELECT ON tp_t TO tp_r", "NO"},
				{"GRANT SELECT ON tp_t TO tp_r WITH GRANT OPTION", "YES"},
				{"REVOKE GRANT OPTION FOR SELECT ON tp_t FROM tp_r", "NO"},
				{"REVOKE SELECT ON tp_t FROM tp_r", ""},
			} {
				c.run(peer, fixture.Exec(phase.sql, 0))
				var want [][]any
				if phase.option != "" {
					want = [][]any{c.r("SELECT", phase.option)}
				}
				c.check(held, "tp_t", "tp_r", want...)
				c.check(c.conn, "tp_t", "tp_r", want...)
			}
			c.exec("RESET SESSION AUTHORIZATION")
			c.run(peer, fixture.Exec("GRANT SELECT ON tp_t TO tp_g", 0))
			c.run(peer, fixture.Exec("RESET ROLE", 0))
			c.exec("SET SESSION AUTHORIZATION tp_m")
			row := c.row("tp_o", "tp_g", "public", "tp_t", "SELECT", "NO")
			c.check(held, "tp_t", "tp_g")
			c.run(peer, fixture.Exec("GRANT tp_g TO tp_m WITH INHERIT TRUE", 0))
			c.check(held, "tp_t", "tp_g", row)
			c.check(c.conn, "tp_t", "tp_g", row)
			c.run(peer, fixture.Exec("REVOKE tp_g FROM tp_m", 0))
			c.check(held, "tp_t", "tp_g")
			c.check(c.conn, "tp_t", "tp_g")
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("rename_move_drop_recreate", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			c.seed()
			c.exec("CREATE SCHEMA tp_moved", "SET ROLE tp_o", "GRANT SELECT ON tp_t TO tp_r", "RESET ROLE")
			c.check(c.conn, "tp_t", "tp_r", c.r("SELECT", "NO"))
			// Role rename SQL is not native admission; the native frontier unit
			// publishes renamed role descriptors and checks the same ACL endpoints.
			if c.b.PG {
				c.exec("ALTER ROLE tp_r RENAME TO tp_renamed", "ALTER ROLE tp_o RENAME TO tp_xowner")
				c.check(c.conn, "tp_t", "tp_r")
				c.check(c.conn, "tp_t", "tp_renamed", c.row("tp_xowner", "tp_renamed", "public", "tp_t", "SELECT", "NO"))
				c.exec("ALTER ROLE tp_xowner RENAME TO tp_o", "ALTER ROLE tp_renamed RENAME TO tp_r")
			}
			c.exec("ALTER TABLE tp_t RENAME TO tp_later", "ALTER TABLE tp_later SET SCHEMA tp_moved")
			c.check(c.conn, "tp_t", "tp_r")
			c.check(c.conn, "tp_later", "tp_r", c.row("tp_o", "tp_r", "tp_moved", "tp_later", "SELECT", "NO"))
			c.exec("DROP TABLE tp_moved.tp_later", "CREATE TABLE tp_moved.tp_later(a integer)", "ALTER TABLE tp_moved.tp_later OWNER TO tp_o")
			c.check(c.conn, "tp_later", "tp_r")
		})
	})
}

func TestReferenceInfoTablePrivilegesRetained(t *testing.T) {
	t.Run("identity_acl_membership_frontier", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			c.seed()
			c.exec("SET ROLE tp_o", "GRANT SELECT ON tp_t TO tp_g", "RESET ROLE", "GRANT tp_g TO tp_m WITH INHERIT TRUE", "SET SESSION AUTHORIZATION tp_m")
			held := c.hold(c.conn, tpQuery)
			old := c.row("tp_o", "tp_g", "public", "tp_t", "SELECT", "NO")
			c.check(held, "tp_t", "tp_g", old)
			peer := c.peer()
			var retained *sql.Rows
			var saved []*sql.ColumnType
			if !c.b.PG {
				var err error
				retained, err = held.QueryContext(c.p.Ctx, tpQuery, "tp_t", "tp_g")
				if err != nil {
					c.t.Fatal(err)
				}
				c.t.Cleanup(func() { retained.Close() })
				saved = tpMetadata(c.t, retained, false)
			}
			for _, q := range []string{"ALTER TABLE tp_t RENAME TO tp_later", "REVOKE tp_g FROM tp_m", "SET ROLE tp_o", "GRANT SELECT ON tp_later TO tp_g WITH GRANT OPTION", "RESET ROLE"} {
				c.run(peer, fixture.Exec(q, 0))
			}
			if retained != nil {
				tpConsume(c.t, retained, false, [][]any{old})
				tpColumnTypes(c.t, saved, false)
			}
			c.check(held, "tp_t", "tp_g")
			c.check(held, "tp_later", "tp_g")
			c.run(peer, fixture.Exec("GRANT tp_g TO tp_m WITH INHERIT TRUE", 0))
			c.check(held, "tp_later", "tp_g", c.row("tp_o", "tp_g", "public", "tp_later", "SELECT", "YES"))
			c.run(peer, fixture.Exec("SET ROLE tp_o", 0))
			c.run(peer, fixture.Exec("REVOKE SELECT ON tp_later FROM tp_g", 0))
			c.check(held, "tp_later", "tp_g")
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("owner_baseline_controls_and_cancel", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			c.exec("CREATE TABLE tp_control(a integer)")
			want := c.baseline(c.owner, "tp_control")
			c.check(c.conn, "tp_control", c.owner, want...)
			if !c.b.PG {
				rows, err := c.conn.QueryContext(c.p.Ctx, tpQuery, "tp_control", c.owner)
				if err != nil {
					c.t.Fatal(err)
				}
				c.t.Cleanup(func() { rows.Close() })
				saved := tpMetadata(c.t, rows, false)
				var dump bytes.Buffer
				if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
					c.t.Fatal(err)
				}
				if dump.Len() == 0 || bytes.Contains(dump.Bytes(), []byte("CREATE VIEW information_schema.table_privileges")) {
					c.t.Fatal("empty native dump")
				}
				if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
					c.t.Fatal(err)
				}
				c.check(c.conn, "tp_control", c.owner)
				if err := stuntgres.Restore(c.p.Ctx, c.p.Native, &dump); err != nil {
					c.t.Fatal(err)
				}
				c.check(c.conn, "tp_control", c.owner, want...)
				tpConsume(c.t, rows, false, want)
				tpColumnTypes(c.t, saved, false)
				held := c.hold(c.conn, tpQuery)
				ctx, cancel := context.WithCancel(c.p.Ctx)
				cancel()
				for _, e := range []fixture.Executor{c.conn, held} {
					r, err := e.QueryContext(ctx, tpQuery, "tp_control", c.owner)
					if r != nil {
						r.Close()
						c.t.Fatal("canceled query returned rows")
					}
					if !errors.Is(err, context.Canceled) {
						c.t.Fatalf("canceled query: %v", err)
					}
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						c.t.Fatal("ordinary cancellation acquired SQLSTATE", err)
					}
				}
				c.check(c.conn, "tp_control", c.owner, want...)
			}
		})
	})
}
