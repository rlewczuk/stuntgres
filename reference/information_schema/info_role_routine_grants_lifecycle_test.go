package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoRoleRoutineGrantsLifecycle(t *testing.T) {
	t.Run("rename_move_recreate_roles", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			c.seed()
			c.exec("CREATE SCHEMA rrg_moved AUTHORIZATION rrg_o", "SET ROLE rrg_o", "GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_r", "RESET ROLE")
			held := c.hold(c.conn, rrgQuery)
			_, _, _, _, oldOID := c.identity("public.rrg_f()")
			c.exec("ALTER FUNCTION public.rrg_f() RENAME TO rrg_later", "ALTER FUNCTION public.rrg_later() SET SCHEMA rrg_moved")
			schema, name, owner, specific, oid := c.identity("rrg_moved.rrg_later()")
			if oid != oldOID {
				c.t.Fatal("rename changed OID", oldOID, oid)
			}
			c.check(held, "public", "rrg_f")
			want := append(c.baseline("rrg_moved.rrg_later()"), c.row(owner, "rrg_r", schema, name, specific, "NO"))
			c.check(held, schema, name, want...)
			c.check(c.conn, schema, name, want...)
			c.exec("SET ROLE rrg_o", "REVOKE EXECUTE ON FUNCTION rrg_moved.rrg_later() FROM rrg_r", "RESET ROLE", "DROP ROLE rrg_r", "CREATE ROLE rrg_r")
			c.check(held, schema, name, c.baseline("rrg_moved.rrg_later()")...)
			c.actor("rrg_r", func() { c.check(held, schema, name) })
			c.exec("SET ROLE rrg_o", "GRANT EXECUTE ON FUNCTION rrg_moved.rrg_later() TO rrg_r", "RESET ROLE")
			c.actor("rrg_r", func() { c.check(held, schema, name, c.row(owner, "rrg_r", schema, name, specific, "NO")) })
			c.exec("DROP FUNCTION rrg_moved.rrg_later()", "CREATE FUNCTION rrg_moved.rrg_later() RETURNS integer RETURN 8", "ALTER FUNCTION rrg_moved.rrg_later() OWNER TO rrg_o")
			_, _, _, newSpecific, newOID := c.identity("rrg_moved.rrg_later()")
			if newOID == oldOID || newSpecific == specific {
				c.t.Fatal("recreate reused identity")
			}
			c.check(held, schema, name, c.baseline("rrg_moved.rrg_later()")...)
		})
	})
	t.Run("transaction_frontiers", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			c.seed()
			peer := c.peer()
			held := c.hold(c.conn, rrgQuery)
			baseline := c.baseline("public.rrg_f()")
			read := func(e fixture.Executor, want [][]any) { c.check(e, "public", "rrg_f", want...) }
			read(held, baseline)
			c.exec("BEGIN", "SET LOCAL ROLE rrg_o", "GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_r")
			row := c.origin("rrg_o", "rrg_r", "NO")
			granted := append(append([][]any(nil), baseline...), row)
			read(held, granted)
			read(peer, baseline)
			c.exec("SAVEPOINT rrg_save", "GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_r WITH GRANT OPTION")
			upgraded := append(append([][]any(nil), baseline...), c.origin("rrg_o", "rrg_r", "YES"))
			read(held, upgraded)
			read(peer, baseline)
			c.exec("ROLLBACK TO SAVEPOINT rrg_save")
			read(held, granted)
			c.exec("COMMIT")
			read(held, granted)
			read(peer, granted)
			c.exec("BEGIN", "SET LOCAL ROLE rrg_o", "REVOKE EXECUTE ON FUNCTION public.rrg_f() FROM rrg_r")
			read(held, baseline)
			read(peer, granted)
			c.exec("ROLLBACK")
			read(held, granted)
			c.exec("BEGIN", "CREATE FUNCTION public.rrg_tx() RETURNS integer RETURN 9")
			own := c.baseline("public.rrg_tx()")
			c.check(c.conn, "public", "rrg_tx", own...)
			c.check(peer, "public", "rrg_tx")
			c.exec("ROLLBACK")
			c.check(held, "public", "rrg_tx")
			c.check(peer, "public", "rrg_tx")
			c.exec("BEGIN", "CREATE FUNCTION public.rrg_tx() RETURNS integer RETURN 9", "COMMIT")
			c.check(peer, "public", "rrg_tx", c.baseline("public.rrg_tx()")...)
		})
	})
	t.Run("retained_native_frontier", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			c.seed()
			c.exec("SET ROLE rrg_o", "GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_g", "RESET ROLE", "GRANT rrg_g TO rrg_m WITH INHERIT TRUE", "SET SESSION AUTHORIZATION rrg_m")
			held := c.hold(c.conn, rrgQuery)
			old := c.origin("rrg_o", "rrg_g", "NO")
			c.check(held, "public", "rrg_f", old)
			// Native eager rows intentionally preserve the complete detached old frontier.
			if !c.b.PG {
				rows, err := held.QueryContext(c.p.Ctx, rrgQuery, "public", "rrg_f")
				if err != nil {
					c.t.Fatal(err)
				}
				defer rows.Close()
				peer := c.peer()
				for _, q := range []string{"ALTER FUNCTION public.rrg_f() RENAME TO rrg_later", "REVOKE rrg_g FROM rrg_m", "SET ROLE rrg_o", "GRANT EXECUTE ON FUNCTION public.rrg_later() TO rrg_g WITH GRANT OPTION", "RESET ROLE"} {
					c.run(peer, fixture.Exec(q, 0))
				}
				rrgConsume(c.t, rows, false, [][]any{old})
				c.check(held, "public", "rrg_f")
				c.check(held, "public", "rrg_later")
				c.run(peer, fixture.Exec("GRANT rrg_g TO rrg_m WITH INHERIT TRUE", 0))
				schema, name, _, specific, _ := c.identity("public.rrg_later()")
				c.check(held, schema, name, c.row("rrg_o", "rrg_g", schema, name, specific, "YES"))
			}
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("acl_free_native_controls", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			c.exec("CREATE FUNCTION public.rrg_control() RETURNS integer RETURN 7")
			old := c.baseline("public.rrg_control()")
			c.check(c.conn, "public", "rrg_control", old...)
			if !c.b.PG {
				rows, err := c.conn.QueryContext(c.p.Ctx, rrgQuery, "public", "rrg_control")
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
				c.check(c.conn, "public", "rrg_control")
				if err := stuntgres.Restore(c.p.Ctx, c.p.Native, &dump); err != nil {
					c.t.Fatal(err)
				}
				c.check(c.conn, "public", "rrg_control", c.baseline("public.rrg_control()")...)
				rrgConsume(c.t, rows, false, old)
			}
		})
	})
}
