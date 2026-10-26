package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoUsagePrivilegesLifecycle(t *testing.T) {
	t.Run("role_drop_recreate", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec("SET ROLE up_o", "REVOKE USAGE ON DOMAIN public.up_t FROM PUBLIC", "GRANT USAGE ON DOMAIN public.up_t TO up_r", "RESET ROLE", "GRANT up_r TO up_m WITH INHERIT TRUE")
			held := c.hold(c.conn, upQuery)
			own := c.origin("up_o", "up_o", "YES")
			reader := c.origin("up_o", "up_r", "NO")
			read := func(want ...[]any) {
				c.check(c.conn, "public", "up_t", want...)
				c.check(held, "public", "up_t", want...)
			}
			member := func(want ...[]any) {
				c.actor("up_m", func() { read(want...) })
			}
			read(own, reader)
			member(reader)
			c.exec("SET ROLE up_o", "REVOKE USAGE ON DOMAIN public.up_t FROM up_r", "RESET ROLE")
			read(own)
			member()
			c.exec("DROP ROLE up_r")
			read(own)
			member()
			c.exec("CREATE ROLE up_r")
			read(own)
			member()
			membership := fixture.Select("SELECT pg_has_role('up_m','up_r','MEMBER') AS member", []string{"member"}, []string{"BOOL"}, [][]any{{false}})
			c.run(c.conn, membership)
			c.run(c.conn, fixture.Prepared(membership))
			// Recreating a name restores neither its old membership nor its grant.
			c.exec("GRANT up_r TO up_m WITH INHERIT TRUE")
			read(own)
			member()
			c.exec("SET ROLE up_o", "GRANT USAGE ON DOMAIN public.up_t TO up_r", "RESET ROLE")
			read(own, reader)
			member(reader)
			c.exec("REVOKE up_r FROM up_m")
			read(own, reader)
			member()
			c.exec("SET ROLE up_o", "REVOKE USAGE ON DOMAIN public.up_t FROM up_r", "RESET ROLE", "DROP ROLE up_r")
			read(own)
			member()
		})
	})
	t.Run("held_peer_publications", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec("SET ROLE up_o", "REVOKE USAGE ON DOMAIN public.up_t FROM PUBLIC", "RESET ROLE")
			peer := c.peer()
			c.exec("SET SESSION AUTHORIZATION up_m")
			held := c.hold(c.conn, upQuery)
			write := func(q string) { c.run(peer, fixture.Exec(q, 0)) }
			read := func(want ...[]any) {
				c.check(c.conn, "public", "up_t", want...)
				c.check(held, "public", "up_t", want...)
			}
			read()
			write("SET ROLE up_o")
			write("GRANT USAGE ON TYPE public.up_t TO up_r")
			write("RESET ROLE")
			read()
			write("GRANT up_r TO up_m WITH INHERIT TRUE")
			read(c.origin("up_o", "up_r", "NO"))
			write("SET ROLE up_o")
			write("GRANT USAGE ON TYPE public.up_t TO up_r WITH GRANT OPTION")
			write("RESET ROLE")
			read(c.origin("up_o", "up_r", "YES"))
			write("REVOKE up_r FROM up_m")
			read()
			write("GRANT up_r TO up_m WITH INHERIT TRUE")
			write("SET ROLE up_o")
			write("REVOKE USAGE ON TYPE public.up_t FROM up_r")
			write("RESET ROLE")
			read()
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("rename_move_owner_recreate", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec("CREATE SCHEMA up_moved AUTHORIZATION up_o", "SET ROLE up_o", "GRANT USAGE ON TYPE public.up_t TO up_r", "RESET ROLE")
			identity := func(name string) int64 {
				var oid int64
				if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid::bigint FROM pg_type WHERE oid=$1::regtype", name).Scan(&oid); err != nil {
					c.t.Fatal(err)
				}
				return oid
			}
			old := identity("public.up_t")
			held := c.hold(c.conn, upQuery)
			c.exec("ALTER TYPE public.up_t RENAME TO up_later", "ALTER TYPE public.up_later SET SCHEMA up_moved")
			if identity("up_moved.up_later") != old {
				c.t.Fatal("rename changed type identity")
			}
			c.check(held, "public", "up_t")
			want := append(c.baseline("up_o", "up_moved", "up_later"), c.row("up_o", "up_r", "up_moved", "up_later", "NO"))
			c.check(held, "up_moved", "up_later", want...)
			c.exec("ALTER TYPE up_moved.up_later OWNER TO up_d")
			want = append(c.baseline("up_d", "up_moved", "up_later"), c.row("up_d", "up_r", "up_moved", "up_later", "NO"))
			c.check(c.conn, "up_moved", "up_later", want...)
			c.check(held, "up_moved", "up_later", want...)
			c.exec("DROP TYPE up_moved.up_later", "CREATE DOMAIN up_moved.up_later AS integer", "ALTER TYPE up_moved.up_later OWNER TO up_o")
			if identity("up_moved.up_later") == old {
				c.t.Fatal("recreated type reused identity")
			}
			c.check(held, "up_moved", "up_later", c.baseline("up_o", "up_moved", "up_later")...)
		})
	})
	t.Run("transaction_frontiers", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec("SET ROLE up_o", "REVOKE USAGE ON DOMAIN public.up_t FROM PUBLIC", "RESET ROLE")
			peer := c.peer()
			held := c.hold(c.conn, upQuery)
			baseline := [][]any{c.origin("up_o", "up_o", "YES")}
			granted := append(append([][]any(nil), baseline...), c.origin("up_o", "up_r", "NO"))
			upgraded := append(append([][]any(nil), baseline...), c.origin("up_o", "up_r", "YES"))
			read := func(e fixture.Executor, want [][]any) { c.check(e, "public", "up_t", want...) }
			c.exec("BEGIN", "SET LOCAL ROLE up_o", "GRANT USAGE ON TYPE public.up_t TO up_r")
			read(held, granted)
			read(peer, baseline)
			c.exec("SAVEPOINT up_save", "GRANT USAGE ON TYPE public.up_t TO up_r WITH GRANT OPTION")
			read(held, upgraded)
			read(peer, baseline)
			c.exec("ROLLBACK TO SAVEPOINT up_save")
			read(held, granted)
			c.exec("COMMIT")
			read(held, granted)
			read(peer, granted)
			c.exec("BEGIN", "SET LOCAL ROLE up_o", "REVOKE USAGE ON TYPE public.up_t FROM up_r")
			read(held, baseline)
			read(peer, granted)
			c.exec("ROLLBACK")
			read(held, granted)
			c.exec("BEGIN", "GRANT up_r TO up_m WITH INHERIT TRUE")
			c.exec("SET LOCAL ROLE up_m")
			c.check(held, "public", "up_t", c.origin("up_o", "up_r", "NO"))
			c.exec("RESET ROLE")
			c.run(peer, fixture.Exec("SET SESSION AUTHORIZATION up_m", 0))
			c.check(peer, "public", "up_t")
			c.exec("ROLLBACK")
			c.check(peer, "public", "up_t")
			c.exec("BEGIN", "GRANT up_r TO up_m WITH INHERIT TRUE", "COMMIT")
			c.check(peer, "public", "up_t", c.origin("up_o", "up_r", "NO"))
			c.run(peer, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	})
	t.Run("retained_native_frontier", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec("SET ROLE up_o", "REVOKE USAGE ON DOMAIN public.up_t FROM PUBLIC", "RESET ROLE")
			c.exec("SET ROLE up_o", "GRANT USAGE ON TYPE public.up_t TO up_r", "RESET ROLE", "GRANT up_r TO up_m WITH INHERIT TRUE", "SET SESSION AUTHORIZATION up_m")
			held := c.hold(c.conn, upQuery)
			c.check(held, "public", "up_t", c.origin("up_o", "up_r", "NO"))
			// PostgreSQL streams; only native promises detached eager rows.
			if !c.b.PG {
				rows, err := held.QueryContext(c.p.Ctx, upQuery, "public", "up_t")
				if err != nil {
					c.t.Fatal(err)
				}
				defer rows.Close()
				saved := upMetadata(c.t, rows, false)
				peer := c.peer()
				for _, q := range []string{"SET ROLE up_o", "GRANT USAGE ON DOMAIN public.up_t TO up_r WITH GRANT OPTION", "RESET ROLE"} {
					c.run(peer, fixture.Exec(q, 0))
				}
				c.check(held, "public", "up_t", c.origin("up_o", "up_r", "YES"))
				c.run(peer, fixture.Exec("REVOKE up_r FROM up_m", 0))
				s := c.step(upQuery, [][]any{c.origin("up_o", "up_r", "NO")}, "public", "up_t")
				// Consume the already admitted result, not a new execution.
				if !rows.Next() {
					c.t.Fatal("retained row missing", rows.Err())
				}
				values, targets := make([]any, 8), make([]any, 8)
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
				upColumnTypes(c.t, saved, false)
				c.check(held, "public", "up_t")
			}
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
}
