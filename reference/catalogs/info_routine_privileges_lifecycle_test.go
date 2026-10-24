package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutinePrivilegesLifecycle(t *testing.T) {
	t.Run("transaction_frontiers", func(t *testing.T) {
		rpScenario(t, func(c *rrgCase) {
			rpSeed(c)
			peer := c.peer()
			held := c.hold(c.conn, rpQuery)
			baseline := c.baseline("public.rp_f()")
			granted := append(append([][]any(nil), baseline...), rpOrigin(c, "rp_o", "rp_r", "NO"))
			upgraded := append(append([][]any(nil), baseline...), rpOrigin(c, "rp_o", "rp_r", "YES"))
			read := func(e fixture.Executor, want [][]any) { c.check(e, "public", "rp_f", want...) }
			c.exec("BEGIN", "SET LOCAL ROLE rp_o", "GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_r")
			read(held, granted)
			read(peer, baseline)
			c.exec("SAVEPOINT rp_save", "GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_r WITH GRANT OPTION")
			read(held, upgraded)
			read(peer, baseline)
			c.exec("ROLLBACK TO SAVEPOINT rp_save")
			read(held, granted)
			c.exec("COMMIT")
			read(held, granted)
			read(peer, granted)
			c.exec("BEGIN", "SET LOCAL ROLE rp_o", "REVOKE EXECUTE ON FUNCTION public.rp_f() FROM rp_r")
			read(held, baseline)
			read(peer, granted)
			c.exec("ROLLBACK")
			read(held, granted)
			c.exec("BEGIN", "CREATE FUNCTION public.rp_tx() RETURNS integer RETURN 9")
			c.check(c.conn, "public", "rp_tx", c.baseline("public.rp_tx()")...)
			c.check(peer, "public", "rp_tx")
			c.exec("ROLLBACK")
			c.check(held, "public", "rp_tx")
			c.check(peer, "public", "rp_tx")
		})
	})
	t.Run("rename_move_recreate", func(t *testing.T) {
		rpScenario(t, func(c *rrgCase) {
			rpSeed(c)
			c.exec("CREATE SCHEMA rp_moved AUTHORIZATION rp_o", "SET ROLE rp_o", "GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_r", "RESET ROLE")
			held := c.hold(c.conn, rpQuery)
			_, _, _, _, oldOID := c.identity("public.rp_f()")
			c.exec("ALTER FUNCTION public.rp_f() RENAME TO rp_later", "ALTER FUNCTION public.rp_later() SET SCHEMA rp_moved")
			schema, name, owner, specific, oid := c.identity("rp_moved.rp_later()")
			if oid != oldOID {
				c.t.Fatal("rename changed identity", oldOID, oid)
			}
			rpRead(c, held)
			want := append(c.baseline("rp_moved.rp_later()"), c.row(owner, "rp_r", schema, name, specific, "NO"))
			c.check(c.conn, schema, name, want...)
			c.check(held, schema, name, want...)
			c.exec("SET ROLE rp_o", "REVOKE EXECUTE ON FUNCTION rp_moved.rp_later() FROM rp_r", "RESET ROLE", "DROP ROLE rp_r", "CREATE ROLE rp_r")
			public := c.row(owner, "PUBLIC", schema, name, specific, "NO")
			c.actor("rp_r", func() { c.check(held, schema, name, public) })
			c.exec("SET ROLE rp_o", "GRANT EXECUTE ON FUNCTION rp_moved.rp_later() TO rp_r", "RESET ROLE")
			c.actor("rp_r", func() { c.check(held, schema, name, public, c.row(owner, "rp_r", schema, name, specific, "NO")) })
			c.exec("DROP FUNCTION rp_moved.rp_later()", "CREATE FUNCTION rp_moved.rp_later() RETURNS integer RETURN 8", "ALTER FUNCTION rp_moved.rp_later() OWNER TO rp_o")
			_, _, _, newSpecific, newOID := c.identity("rp_moved.rp_later()")
			if newOID == oldOID || newSpecific == specific {
				c.t.Fatal("recreation reused old identity")
			}
			c.check(held, schema, name, c.baseline("rp_moved.rp_later()")...)
			c.check(c.conn, schema, name, c.baseline("rp_moved.rp_later()")...)
		})
	})
	t.Run("retained_native_frontier", func(t *testing.T) {
		rpScenario(t, func(c *rrgCase) {
			rpSeed(c)
			c.exec("SET ROLE rp_o", "GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_g", "RESET ROLE", "GRANT rp_g TO rp_m WITH INHERIT TRUE", "SET SESSION AUTHORIZATION rp_m")
			held := c.hold(c.conn, rpQuery)
			public, named := rpOrigin(c, "rp_o", "PUBLIC", "NO"), rpOrigin(c, "rp_o", "rp_g", "NO")
			rpRead(c, held, public, named)
			if !c.b.PG {
				rows, err := held.QueryContext(c.p.Ctx, rpQuery, "public", "rp_f")
				if err != nil {
					c.t.Fatal(err)
				}
				defer rows.Close()
				peer := c.peer()
				for _, q := range []string{"ALTER FUNCTION public.rp_f() RENAME TO rp_later", "REVOKE rp_g FROM rp_m", "SET ROLE rp_o", "REVOKE EXECUTE ON FUNCTION public.rp_later() FROM PUBLIC", "GRANT EXECUTE ON FUNCTION public.rp_later() TO rp_g WITH GRANT OPTION", "RESET ROLE"} {
					c.run(peer, fixture.Exec(q, 0))
				}
				rrgConsume(c.t, rows, false, [][]any{public, named})
				c.check(held, "public", "rp_f")
				c.check(held, "public", "rp_later")
				c.run(peer, fixture.Exec("GRANT rp_g TO rp_m WITH INHERIT TRUE", 0))
				schema, name, _, specific, _ := c.identity("public.rp_later()")
				c.check(held, schema, name, c.row("rp_o", "rp_g", schema, name, specific, "YES"))
			}
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("acl_free_native_controls", func(t *testing.T) {
		rpScenario(t, func(c *rrgCase) {
			c.exec("CREATE FUNCTION public.rp_control() RETURNS integer RETURN 7")
			old := c.baseline("public.rp_control()")
			c.check(c.conn, "public", "rp_control", old...)
			if !c.b.PG {
				rows, err := c.conn.QueryContext(c.p.Ctx, rpQuery, "public", "rp_control")
				if err != nil {
					c.t.Fatal(err)
				}
				defer rows.Close()
				var dump bytes.Buffer
				if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
					c.t.Fatal(err)
				}
				if dump.Len() == 0 {
					c.t.Fatal("empty dump")
				}
				if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
					c.t.Fatal(err)
				}
				c.check(c.conn, "public", "rp_control")
				if err := stuntgres.Restore(c.p.Ctx, c.p.Native, &dump); err != nil {
					c.t.Fatal(err)
				}
				c.check(c.conn, "public", "rp_control", c.baseline("public.rp_control()")...)
				rrgConsume(c.t, rows, false, old)
			}
		})
	})
}
