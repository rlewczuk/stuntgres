package reference_test

import (
	"context"
	"testing"
	"time"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoTablePrivilegesDiagnostics(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			for _, prefix := range []string{"", "/* λ雪 */ "} {
				q := prefix + "SELECT missing FROM information_schema.table_privileges"
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: fixture.Position(utf8.RuneCountInString(prefix) + 8)})
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					c.run(c.conn, s)
				}
			}
			c.run(c.conn, c.step("SELECT * FROM information_schema.table_privileges WHERE false", nil))
		})
	})
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			tpScenario(t, func(c *tpCase) {
				c.seed()
				c.run(c.conn, fixture.Exec("INSERT INTO tp_t VALUES(7)", 1))
				c.exec("SET ROLE tp_o", "GRANT SELECT ON tp_t TO tp_r", "RESET ROLE")
				prior := c.r("SELECT", "NO")
				unchanged := func() {
					c.check(c.conn, "tp_t", "tp_r", prior)
					c.check(c.conn, "tp_t", "tp_d")
					c.check(c.conn, "tp_t", "PUBLIC")
					c.run(c.conn, fixture.Select("SELECT a FROM tp_t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
				for _, tc := range []struct{ q, code, message string }{
					{"GRANT UPDATE ON tp_t TO tp_missing_role", "42704", `role "tp_missing_role" does not exist`},
					{"REVOKE SELECT ON tp_t FROM tp_missing_role", "42704", `role "tp_missing_role" does not exist`},
					{"GRANT SELECT ON tp_t TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
					{"GRANT UPDATE ON tp_t TO tp_r,tp_missing_role", "42704", `role "tp_missing_role" does not exist`},
					{"REVOKE SELECT ON tp_t FROM tp_r,tp_missing_role", "42704", `role "tp_missing_role" does not exist`},
					{"GRANT UPDATE ON tp_t,tp_missing_table TO tp_r", "42P01", `relation "tp_missing_table" does not exist`},
					{"REVOKE SELECT ON tp_t,tp_missing_table FROM tp_r", "42P01", `relation "tp_missing_table" does not exist`},
				} {
					c.exec("SET ROLE tp_o")
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
					c.exec("RESET ROLE")
					unchanged()
				}
				c.actor("tp_p", func() {
					s := fixture.ImplicitError("GRANT UPDATE ON tp_t TO tp_d", fixture.Diagnostic{Code: "42501", Message: "permission denied for table tp_t"})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
					c.check(c.conn, "tp_t", "tp_d")
				})
				unchanged()
			})
		})
	}
	t.Run("transactional_target_acl", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			c.seed()
			{
				// Always restore shared catalog ACLs under an independent deadline.
				c.exec("BEGIN")
				rollbackNeeded := true
				defer func() {
					if !rollbackNeeded {
						return
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, err := c.conn.ExecContext(ctx, "ROLLBACK"); err != nil {
						c.t.Error(err)
					}
				}()
				for _, q := range []string{"SELECT * FROM information_schema.table_privileges", "SELECT * FROM information_schema.table_privileges WHERE false"} {
					held := c.hold(c.conn, q)
					c.exec("SAVEPOINT tp_acl", "REVOKE SELECT ON information_schema.table_privileges FROM PUBLIC", "SET LOCAL ROLE tp_p")
					for _, route := range []string{"direct", "prepared", "held"} {
						c.exec("SAVEPOINT tp_denial")
						s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for view table_privileges"))
						s.SelectRows, s.ErrorStage = true, "execute"
						var executor fixture.Executor = c.conn
						if route == "prepared" {
							s.Prepared = true
						}
						if route == "held" {
							executor = held
						}
						c.run(executor, s)
						c.exec("ROLLBACK TO SAVEPOINT tp_denial", "RELEASE SAVEPOINT tp_denial")
					}
					c.exec("ROLLBACK TO SAVEPOINT tp_acl", "RELEASE SAVEPOINT tp_acl")
				}
				c.exec("ROLLBACK")
				rollbackNeeded = false
			}
			c.actor("tp_p", func() { c.check(c.conn, "tp_t", "tp_r") })
		})
	})
}
