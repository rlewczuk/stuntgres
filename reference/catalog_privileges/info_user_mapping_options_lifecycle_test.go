package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoUserMappingOptionsLifecycle(t *testing.T) {
	umoScenario(t, func(c *umoCase) {
		_, held := c.hold()
		check := func(server string, want [][]any) {
			s := c.selectStep(umoQuery, want, server)
			held(s)
			c.run(s)
			c.run(fixture.Prepared(s))
		}
		check("umo_server", nil)
		c.seed()
		baseline := c.rows("all")
		check("umo_server", baseline)
		c.exec("ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(ADD tag2 'new')")
		added := append(c.rows("all")[:4], c.row("umo_alice", "umo_server", "tag2", "new"), c.rows("all")[4])
		check("umo_server", added)
		c.exec("ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(SET password 'changed=é')")
		added[2][4] = "changed=é"
		check("umo_server", added)
		c.exec("ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(DROP tag2)")
		changed := c.rows("all")
		changed[2][4] = "changed=é"
		check("umo_server", changed)
		c.exec("ALTER USER MAPPING FOR umo_empty SERVER umo_server OPTIONS(ADD x 'new')", "ALTER USER MAPPING FOR umo_empty SERVER umo_server OPTIONS(SET x '')")
		withEmpty := append(c.rows("all"), c.row("umo_empty", "umo_server", "x", ""))
		withEmpty[2][4] = "changed=é"
		check("umo_server", withEmpty)
		c.exec("ALTER USER MAPPING FOR umo_empty SERVER umo_server OPTIONS(DROP x)")
		check("umo_server", changed)
		c.exec("BEGIN", "ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(SET password 'uncommitted')")
		uncommitted := c.rows("all")
		uncommitted[2][4] = "uncommitted"
		check("umo_server", uncommitted)
		c.exec("ROLLBACK")
		check("umo_server", changed)
		c.exec("BEGIN", "SAVEPOINT before_change", "ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(DROP password)")
		withoutPassword := [][]any{changed[0], changed[1], changed[3], changed[4]}
		check("umo_server", withoutPassword)
		c.exec("ROLLBACK TO before_change")
		check("umo_server", changed)
		c.exec("ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(SET password 'committed')", "COMMIT")
		committed := c.rows("all")
		committed[2][4] = "committed"
		check("umo_server", committed)
		s := fixture.ErrorDiagnostic("ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(SET password 'must-not-publish',DROP absent)", fixture.ControlsReferenceDiagnostic("42704", `option "absent" not found`))
		s.ErrorStage = "execute"
		c.run(s)
		check("umo_server", committed)
		c.exec("ALTER SERVER umo_server RENAME TO umo_renamed")
		renamed := c.rows("all")
		renamed[2][4] = "committed"
		for _, row := range renamed {
			row[2] = "umo_renamed"
		}
		check("umo_server", nil)
		check("umo_renamed", renamed)
		c.exec("DROP USER MAPPING FOR umo_alice SERVER umo_renamed")
		remaining := [][]any{renamed[0], renamed[4]}
		check("umo_renamed", remaining)
		c.exec("CREATE USER MAPPING FOR umo_alice SERVER umo_renamed")
		check("umo_renamed", remaining)
		c.exec("ALTER USER MAPPING FOR umo_alice SERVER umo_renamed OPTIONS(ADD password 'real')")
		check("umo_renamed", [][]any{renamed[0], c.row("umo_alice", "umo_renamed", "password", "real"), renamed[4]})
		c.exec("DROP USER MAPPING FOR umo_alice SERVER umo_renamed", "DROP USER MAPPING FOR umo_bob SERVER umo_renamed", "DROP USER MAPPING FOR umo_empty SERVER umo_renamed")
		*c.notices = nil
		c.exec("DROP SERVER umo_renamed CASCADE")
		iumCascadeNotice(t, c.b.PG, *c.notices, "umo_renamed", "public")
		check("umo_renamed", nil)
		c.exec("CREATE SERVER umo_server FOREIGN DATA WRAPPER umo_fdw", "CREATE USER MAPPING FOR PUBLIC SERVER umo_server OPTIONS(password 'final')")
		check("umo_server", [][]any{c.row("PUBLIC", "umo_server", "password", "final")})
		*c.notices = nil
		c.exec("DROP FOREIGN DATA WRAPPER umo_fdw CASCADE")
		want := []fixture.Diagnostic{}
		if c.b.PG {
			want = append(want, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 2 other objects", Detail: "drop cascades to server umo_server\ndrop cascades to user mapping for public on server umo_server"})
		}
		fixture.RegtypeAssertNotices(t, *c.notices, want)
		check("umo_server", nil)
	})
}
