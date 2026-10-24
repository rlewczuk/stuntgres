package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoRoleUdtGrantsLifecycle(t *testing.T) {
	t.Run("held_peer_publications", func(t *testing.T) {
		ugScenario(t, func(c *ugCase) {
			c.seed()
			peer := c.peer()
			c.exec("SET SESSION AUTHORIZATION ug_m")
			held := c.hold(c.conn, ugQuery)
			write := func(q string) { c.run(peer, fixture.Exec(q, 0)) }
			read := func(want ...[]any) {
				c.check(c.conn, "public", "ug_t", want...)
				c.check(held, "public", "ug_t", want...)
			}
			read()
			write("SET ROLE ug_o")
			write("GRANT USAGE ON TYPE public.ug_t TO ug_r")
			write("RESET ROLE")
			read()
			write("GRANT ug_r TO ug_m WITH INHERIT TRUE")
			read(c.origin("ug_o", "ug_r", "NO"))
			write("SET ROLE ug_o")
			write("GRANT USAGE ON TYPE public.ug_t TO ug_r WITH GRANT OPTION")
			write("RESET ROLE")
			read(c.origin("ug_o", "ug_r", "YES"))
			write("REVOKE ug_r FROM ug_m")
			read()
			write("GRANT ug_r TO ug_m WITH INHERIT TRUE")
			write("SET ROLE ug_o")
			write("REVOKE USAGE ON TYPE public.ug_t FROM ug_r")
			write("RESET ROLE")
			read()
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("rename_move_owner_recreate", func(t *testing.T) {
		ugScenario(t, func(c *ugCase) {
			c.seed()
			c.exec("CREATE SCHEMA ug_moved AUTHORIZATION ug_o", "SET ROLE ug_o", "GRANT USAGE ON TYPE public.ug_t TO ug_r", "RESET ROLE")
			identity := func(name string) int64 {
				var oid int64
				if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid::bigint FROM pg_type WHERE oid=$1::regtype", name).Scan(&oid); err != nil {
					c.t.Fatal(err)
				}
				return oid
			}
			old := identity("public.ug_t")
			held := c.hold(c.conn, ugQuery)
			c.exec("ALTER TYPE public.ug_t RENAME TO ug_later", "ALTER TYPE public.ug_later SET SCHEMA ug_moved")
			if identity("ug_moved.ug_later") != old {
				c.t.Fatal("rename changed type identity")
			}
			c.check(held, "public", "ug_t")
			want := append(c.baseline("ug_o", "ug_moved", "ug_later"), c.row("ug_o", "ug_r", "ug_moved", "ug_later", "NO"))
			c.check(held, "ug_moved", "ug_later", want...)
			c.exec("ALTER TYPE ug_moved.ug_later OWNER TO ug_d")
			want = append(c.baseline("ug_d", "ug_moved", "ug_later"), c.row("ug_d", "ug_r", "ug_moved", "ug_later", "NO"))
			c.check(c.conn, "ug_moved", "ug_later", want...)
			c.check(held, "ug_moved", "ug_later", want...)
			c.exec("DROP TYPE ug_moved.ug_later", "CREATE TYPE ug_moved.ug_later AS(a integer)", "ALTER TYPE ug_moved.ug_later OWNER TO ug_o")
			if identity("ug_moved.ug_later") == old {
				c.t.Fatal("recreated type reused identity")
			}
			c.check(held, "ug_moved", "ug_later", c.baseline("ug_o", "ug_moved", "ug_later")...)
		})
	})
	t.Run("transaction_frontiers", func(t *testing.T) {
		ugScenario(t, func(c *ugCase) {
			c.seed()
			peer := c.peer()
			held := c.hold(c.conn, ugQuery)
			baseline := c.baseline("ug_o", "public", "ug_t")
			granted := append(append([][]any(nil), baseline...), c.origin("ug_o", "ug_r", "NO"))
			upgraded := append(append([][]any(nil), baseline...), c.origin("ug_o", "ug_r", "YES"))
			read := func(e fixture.Executor, want [][]any) { c.check(e, "public", "ug_t", want...) }
			c.exec("BEGIN", "SET LOCAL ROLE ug_o", "GRANT USAGE ON TYPE public.ug_t TO ug_r")
			read(held, granted)
			read(peer, baseline)
			c.exec("SAVEPOINT ug_save", "GRANT USAGE ON TYPE public.ug_t TO ug_r WITH GRANT OPTION")
			read(held, upgraded)
			read(peer, baseline)
			c.exec("ROLLBACK TO SAVEPOINT ug_save")
			read(held, granted)
			c.exec("COMMIT")
			read(held, granted)
			read(peer, granted)
			c.exec("BEGIN", "SET LOCAL ROLE ug_o", "REVOKE USAGE ON TYPE public.ug_t FROM ug_r")
			read(held, baseline)
			read(peer, granted)
			c.exec("ROLLBACK")
			read(held, granted)
			c.exec("BEGIN", "GRANT ug_r TO ug_m WITH INHERIT TRUE")
			c.exec("SET LOCAL ROLE ug_m")
			c.check(held, "public", "ug_t", c.origin("ug_o", "ug_r", "NO"))
			c.exec("RESET ROLE")
			c.run(peer, fixture.Exec("SET SESSION AUTHORIZATION ug_m", 0))
			c.check(peer, "public", "ug_t")
			c.exec("ROLLBACK")
			c.check(peer, "public", "ug_t")
			c.exec("BEGIN", "GRANT ug_r TO ug_m WITH INHERIT TRUE", "COMMIT")
			c.check(peer, "public", "ug_t", c.origin("ug_o", "ug_r", "NO"))
			c.run(peer, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	})
	t.Run("retained_native_frontier", func(t *testing.T) {
		ugScenario(t, func(c *ugCase) {
			c.seed()
			c.exec("SET ROLE ug_o", "GRANT USAGE ON TYPE public.ug_t TO ug_r", "RESET ROLE", "GRANT ug_r TO ug_m WITH INHERIT TRUE", "SET SESSION AUTHORIZATION ug_m")
			held := c.hold(c.conn, ugQuery)
			c.check(held, "public", "ug_t", c.origin("ug_o", "ug_r", "NO"))
			// PostgreSQL streams; only native promises detached eager rows.
			if !c.b.PG {
				rows, err := held.QueryContext(c.p.Ctx, ugQuery, "public", "ug_t")
				if err != nil {
					c.t.Fatal(err)
				}
				defer rows.Close()
				saved := ugMetadata(c.t, rows, false)
				peer := c.peer()
				c.run(peer, fixture.Exec("REVOKE ug_r FROM ug_m", 0))
				s := c.step(ugQuery, [][]any{c.origin("ug_o", "ug_r", "NO")}, "public", "ug_t")
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
				c.check(held, "public", "ug_t")
			}
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
}
