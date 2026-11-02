package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnPrivilegesLifecycle(t *testing.T) {
	cpScenario(t, func(c *cpCase) {
		c.seed()
		peer := c.peer()
		c.exec("SET SESSION AUTHORIZATION cp_r")
		held := c.hold(c.conn, cpQuery)
		c.check(held, "cp_t", "cp_r")
		c.run(peer, fixture.Exec("SET ROLE cp_o", 0))
		for _, phase := range []struct{ query, option string }{
			{"GRANT SELECT(a) ON cp_t TO cp_r", "NO"},
			{"GRANT SELECT(a) ON cp_t TO cp_r WITH GRANT OPTION", "YES"},
			{"REVOKE GRANT OPTION FOR SELECT(a) ON cp_t FROM cp_r", "NO"},
			{"REVOKE SELECT(a) ON cp_t FROM cp_r", ""},
		} {
			c.run(peer, fixture.Exec(phase.query, 0))
			var want [][]any
			if phase.option != "" {
				want = [][]any{c.r("a", "SELECT", phase.option)}
			}
			c.check(held, "cp_t", "cp_r", want...)
			c.check(c.conn, "cp_t", "cp_r", want...)
		}
		c.run(peer, fixture.Exec("GRANT UPDATE(b) ON cp_t TO cp_g", 0))
		c.run(peer, fixture.Exec("RESET ROLE", 0))
		group := c.row("cp_o", "cp_g", "public", "cp_t", "b", "UPDATE", "NO")
		c.check(held, "cp_t", "cp_g")
		c.run(peer, fixture.Exec("GRANT cp_g TO cp_r WITH INHERIT TRUE", 0))
		c.check(held, "cp_t", "cp_g", group)
		c.run(peer, fixture.Exec("GRANT cp_g TO cp_r WITH INHERIT FALSE, SET TRUE", 0))
		c.check(held, "cp_t", "cp_g")
		c.exec("SET ROLE cp_g")
		c.check(held, "cp_t", "cp_g", group)
		c.exec("RESET ROLE")
		c.check(held, "cp_t", "cp_g")
		c.run(peer, fixture.Exec("REVOKE cp_g FROM cp_r", 0))
		c.check(held, "cp_t", "cp_g")
		c.exec("RESET SESSION AUTHORIZATION")
	})
}

func TestReferenceInfoColumnPrivilegesRetained(t *testing.T) {
	cpScenario(t, func(c *cpCase) {
		c.seed()
		c.exec("SET ROLE cp_o", "GRANT SELECT(a) ON cp_t TO cp_g", "RESET ROLE", "GRANT cp_g TO cp_m WITH INHERIT TRUE", "SET SESSION AUTHORIZATION cp_m")
		held := c.hold(c.conn, cpQuery)
		old := c.row("cp_o", "cp_g", "public", "cp_t", "a", "SELECT", "NO")
		c.check(held, "cp_t", "cp_g", old)
		peer := c.peer()
		var retained *sql.Rows
		var saved []*sql.ColumnType
		// Native public rows are eager; pq is streaming and cannot supply this
		// ownership witness. The shared held executions below remain dual-mode.
		if !c.b.PG {
			var err error
			retained, err = held.QueryContext(c.p.Ctx, cpQuery, "cp_t", "cp_g")
			if err != nil {
				c.t.Fatal(err)
			}
			c.t.Cleanup(func() { retained.Close() })
			saved = rcgMetadata(c.t, retained, false)
		}
		c.run(peer, fixture.Exec("ALTER TABLE cp_t RENAME COLUMN a TO renamed", 0))
		c.run(peer, fixture.Exec("REVOKE cp_g FROM cp_m", 0))
		c.check(held, "cp_t", "cp_g")
		c.run(peer, fixture.Exec("GRANT cp_g TO cp_m WITH INHERIT TRUE", 0))
		c.check(held, "cp_t", "cp_g", c.row("cp_o", "cp_g", "public", "cp_t", "renamed", "SELECT", "NO"))
		c.run(peer, fixture.Exec("SET ROLE cp_o", 0))
		c.run(peer, fixture.Exec("REVOKE SELECT(renamed) ON cp_t FROM cp_g", 0))
		c.check(held, "cp_t", "cp_g")
		if retained != nil {
			rcgConsume(c.t, retained, false, [][]any{old})
			rcgColumnTypes(c.t, saved, false)
		}
		c.exec("RESET SESSION AUTHORIZATION")
	})
}
