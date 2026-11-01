package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoUdtPrivilegesLifecycle(t *testing.T) {
	t.Run("reset_reinstalls_source", uvResetSource)
	t.Run("held_peer_publications", func(t *testing.T) {
		uvScenario(t, func(c *uvCase) {
			c.seed()
			peer := c.peer()
			c.exec("SET SESSION AUTHORIZATION uv_m")
			held := c.hold(c.conn, uvQuery)
			write := func(q string) { c.run(peer, fixture.Exec(q, 0)) }
			read := func(want ...[]any) {
				c.check(c.conn, "public", "uv_t", want...)
				c.check(held, "public", "uv_t", want...)
			}
			read(c.public())
			write("SET ROLE uv_o")
			write("GRANT USAGE ON TYPE public.uv_t TO uv_r")
			write("RESET ROLE")
			read(c.public())
			write("GRANT uv_r TO uv_m WITH INHERIT TRUE")
			read(c.public(), c.origin("uv_o", "uv_r", "NO"))
			write("SET ROLE uv_o")
			write("GRANT USAGE ON TYPE public.uv_t TO uv_r WITH GRANT OPTION")
			write("RESET ROLE")
			read(c.public(), c.origin("uv_o", "uv_r", "YES"))
			write("REVOKE uv_r FROM uv_m")
			read(c.public())
			write("GRANT uv_r TO uv_m WITH INHERIT TRUE")
			write("SET ROLE uv_o")
			write("REVOKE USAGE ON TYPE public.uv_t FROM uv_r")
			write("RESET ROLE")
			read(c.public())
			write("SET ROLE uv_o")
			write("REVOKE USAGE ON TYPE public.uv_t FROM PUBLIC")
			write("RESET ROLE")
			read()
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("rename_move_owner_recreate", func(t *testing.T) {
		uvScenario(t, func(c *uvCase) {
			c.seed()
			c.exec("CREATE SCHEMA uv_moved AUTHORIZATION uv_o", "SET ROLE uv_o", "GRANT USAGE ON TYPE public.uv_t TO uv_r", "RESET ROLE")
			identity := func(name string) int64 {
				var oid int64
				if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid::bigint FROM pg_type WHERE oid=$1::regtype", name).Scan(&oid); err != nil {
					c.t.Fatal(err)
				}
				return oid
			}
			old := identity("public.uv_t")
			held := c.hold(c.conn, uvQuery)
			c.exec("ALTER TYPE public.uv_t RENAME TO uv_later", "ALTER TYPE public.uv_later SET SCHEMA uv_moved")
			if identity("uv_moved.uv_later") != old {
				c.t.Fatal("rename changed type identity")
			}
			c.check(held, "public", "uv_t")
			want := append(c.baseline("uv_o", "uv_moved", "uv_later"), c.row("uv_o", "uv_r", "uv_moved", "uv_later", "NO"))
			c.check(held, "uv_moved", "uv_later", want...)
			c.exec("ALTER TYPE uv_moved.uv_later OWNER TO uv_d")
			want = append(c.baseline("uv_d", "uv_moved", "uv_later"), c.row("uv_d", "uv_r", "uv_moved", "uv_later", "NO"))
			c.check(c.conn, "uv_moved", "uv_later", want...)
			c.check(held, "uv_moved", "uv_later", want...)
			c.exec("DROP TYPE uv_moved.uv_later", "CREATE TYPE uv_moved.uv_later AS(a integer)", "ALTER TYPE uv_moved.uv_later OWNER TO uv_o")
			if identity("uv_moved.uv_later") == old {
				c.t.Fatal("recreated type reused identity")
			}
			c.check(held, "uv_moved", "uv_later", c.baseline("uv_o", "uv_moved", "uv_later")...)
		})
	})
	t.Run("transaction_frontiers", func(t *testing.T) {
		uvScenario(t, func(c *uvCase) {
			c.seed()
			peer := c.peer()
			held := c.hold(c.conn, uvQuery)
			baseline := c.baseline("uv_o", "public", "uv_t")
			granted := append(append([][]any(nil), baseline...), c.origin("uv_o", "uv_r", "NO"))
			upgraded := append(append([][]any(nil), baseline...), c.origin("uv_o", "uv_r", "YES"))
			read := func(e fixture.Executor, want [][]any) { c.check(e, "public", "uv_t", want...) }
			c.exec("BEGIN", "SET LOCAL ROLE uv_o", "GRANT USAGE ON TYPE public.uv_t TO uv_r")
			read(held, granted)
			read(peer, baseline)
			c.exec("SAVEPOINT uv_save", "GRANT USAGE ON TYPE public.uv_t TO uv_r WITH GRANT OPTION")
			read(held, upgraded)
			read(peer, baseline)
			c.exec("ROLLBACK TO SAVEPOINT uv_save")
			read(held, granted)
			c.exec("COMMIT")
			read(held, granted)
			read(peer, granted)
			c.exec("BEGIN", "SET LOCAL ROLE uv_o", "REVOKE USAGE ON TYPE public.uv_t FROM uv_r")
			read(held, baseline)
			read(peer, granted)
			c.exec("ROLLBACK")
			read(held, granted)
			c.exec("BEGIN", "GRANT uv_r TO uv_m WITH INHERIT TRUE")
			c.exec("SET LOCAL ROLE uv_m")
			c.check(held, "public", "uv_t", c.public(), c.origin("uv_o", "uv_r", "NO"))
			c.exec("RESET ROLE")
			c.run(peer, fixture.Exec("SET SESSION AUTHORIZATION uv_m", 0))
			c.check(peer, "public", "uv_t", c.public())
			c.exec("ROLLBACK")
			c.check(peer, "public", "uv_t", c.public())
			c.exec("BEGIN", "GRANT uv_r TO uv_m WITH INHERIT TRUE", "COMMIT")
			c.check(peer, "public", "uv_t", c.public(), c.origin("uv_o", "uv_r", "NO"))
			c.run(peer, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	})
	t.Run("retained_native_frontier", func(t *testing.T) {
		uvScenario(t, func(c *uvCase) {
			c.seed()
			c.exec("SET ROLE uv_o", "REVOKE USAGE ON TYPE public.uv_t FROM PUBLIC", "GRANT USAGE ON TYPE public.uv_t TO uv_r", "RESET ROLE", "GRANT uv_r TO uv_m WITH INHERIT TRUE", "SET SESSION AUTHORIZATION uv_m")
			held := c.hold(c.conn, uvQuery)
			c.check(held, "public", "uv_t", c.origin("uv_o", "uv_r", "NO"))
			// PostgreSQL streams; only native promises detached eager rows.
			if !c.b.PG {
				rows, err := held.QueryContext(c.p.Ctx, uvQuery, "public", "uv_t")
				if err != nil {
					c.t.Fatal(err)
				}
				defer rows.Close()
				saved := ugMetadata(c.t, rows, false)
				peer := c.peer()
				c.run(peer, fixture.Exec("REVOKE uv_r FROM uv_m", 0))
				s := c.step(uvQuery, [][]any{c.origin("uv_o", "uv_r", "NO")}, "public", "uv_t")
				// Consume the already admitted result, not a new execution.
				if !rows.Next() {
					c.t.Fatal("retained row missing", rows.Err())
				}
				values, targets := make([]any, 7), make([]any, 7)
				for i := range targets {
					targets[i] = &values[i]
				}
				if err := rows.Scan(targets...); err != nil {
					c.t.Fatal(err)
				}
				for i := range values {
					if fixture.Normalize(values[i]) != s.Rows[0][i] {
						c.t.Fatalf("retained column %d=%v want %v", i, values[i], s.Rows[0][i])
					}
				}
				if rows.Next() {
					c.t.Fatal("extra retained row")
				}
				if err := rows.Err(); err != nil {
					c.t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					c.t.Fatal(err)
				}
				ugColumnTypes(c.t, saved, false)
				c.check(held, "public", "uv_t")
			}
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
}
