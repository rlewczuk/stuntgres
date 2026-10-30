package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceReplicationMessagesPrivileges(t *testing.T) {
	t.Run("live-actor", func(t *testing.T) {
		const ordinary = "replication_messages_ordinary"
		const direct = "replication_messages_replicator"
		const member = "replication_messages_member"
		fixture.RoleReferenceCleanup(t, []string{ordinary, direct, member})
		recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, sql := range []string{"CREATE ROLE " + ordinary, "CREATE ROLE " + direct + " REPLICATION", "CREATE ROLE " + member, "GRANT " + direct + " TO " + member} {
				run(fixture.Exec(sql, 0))
			}
			defer replicationMessagesRestore(t, c, b.PG, ordinary, direct, member)()
			q := "SELECT pg_sync_replication_slots()"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			check := func(authorized bool) {
				s := replicationMessageSync(q, b.PG, authorized)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				run(s)
				run(fixture.Prepared(s))
			}
			check(true)
			for _, role := range []string{ordinary, member, direct} {
				run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
				run(fixture.Select("SELECT current_user,rolreplication FROM pg_roles WHERE rolname=current_user", []string{"current_user", "rolreplication"}, []string{"NAME", "BOOL"}, [][]any{{role, role == direct}}))
				check(role == direct)
				replicationMessageEmitRoutes(t, p, b, c, "SELECT pg_logical_emit_message(true,'p','ordinary allowed')")
				if role == member {
					run(fixture.Exec("SET ROLE "+direct, 0))
					check(true)
					run(fixture.Exec("RESET ROLE", 0))
					check(false)
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
			// The already prepared handle observes changed own attributes and ACLs.
			for _, attribute := range []bool{false, true, false} {
				option := "NOREPLICATION"
				if attribute {
					option = "REPLICATION"
				}
				run(fixture.Exec("ALTER ROLE "+ordinary+" "+option, 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_sync_replication_slots() TO "+ordinary, 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION "+ordinary, 0))
				check(attribute)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_sync_replication_slots() FROM PUBLIC", 0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_sync_replication_slots() FROM "+ordinary, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+ordinary, 0))
			denied := recoveryControlDenied(q, "pg_sync_replication_slots")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
			run(denied)
			run(fixture.Prepared(denied))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_sync_replication_slots() TO "+ordinary, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+ordinary, 0))
			check(false)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	})
	t.Run("per-overload-live-acl", func(t *testing.T) {
		const role = "replication_messages_acl_reader"
		fixture.RoleReferenceCleanup(t, []string{role})
		recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE "+role, 0))
			defer replicationMessagesRestore(t, c, b.PG, role)()
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
			run(fixture.Exec("CREATE TABLE replication_messages_acl_null(t text,b bytea)", 0))
			run(fixture.Exec("INSERT INTO replication_messages_acl_null VALUES(NULL,NULL)", 1))
			run(fixture.Exec("GRANT SELECT ON replication_messages_acl_null TO "+role, 0))
			queries := []string{"SELECT pg_logical_emit_message(true,'p','x')", `SELECT pg_logical_emit_message(true,'p','\x00ff'::bytea)`}
			statements := make([]*sql.Stmt, 2)
			for i, q := range queries {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				statements[i] = stmt
			}
			for selected := range 2 {
				identity := replicationMessageIdentities[selected]
				typ, column, message := "text", "t", "'x'"
				if selected == 1 {
					typ, column, message = "bytea", "b", `'\x00'::bytea`
				}
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+identity+" FROM PUBLIC", 0))
				held := fixture.HeldStatement{Executor: c, Statement: statements[selected], SQL: queries[selected]}
				for _, granted := range []bool{false, true, false} {
					action := "REVOKE EXECUTE ON FUNCTION " + identity + " FROM " + role
					if granted {
						action = "GRANT EXECUTE ON FUNCTION " + identity + " TO " + role
					}
					run(fixture.Exec(action, 0))
					run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
					if granted {
						replicationMessageEmit(t, p, b, held, queries[selected])
					} else {
						denied := recoveryControlDenied(queries[selected], "pg_logical_emit_message")
						fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
						run(denied)
						run(fixture.Prepared(denied))
					}
					runtimeNull := "SELECT pg_logical_emit_message(true,'p'," + column + ") AS emitted FROM replication_messages_acl_null"
					s := replicationMessageNull(runtimeNull)
					if !granted {
						s = recoveryControlDenied(runtimeNull, "pg_logical_emit_message")
					}
					replicationMessageRoutes(t, p, b, c, s)
					sourceNull := "SELECT * FROM replication_messages_acl_null, LATERAL pg_logical_emit_message(true,'p'," + column + ") AS x"
					// A lateral scalar source is still a surviving invocation; it must admit EXECUTE.
					if !granted {
						replicationMessageRoutes(t, p, b, c, recoveryControlDenied(sourceNull, "pg_logical_emit_message"))
					}
					replicationMessageRoutes(t, p, b, c, replicationMessageNull("SELECT pg_logical_emit_message(true,'p',NULL::"+typ+") AS emitted"))
					replicationMessageRoutes(t, p, b, c, fixture.Select("SELECT * FROM pg_logical_emit_message(true,'p',NULL::"+typ+")", []string{"pg_logical_emit_message"}, []string{"PG_LSN"}, [][]any{{nil}}))
					// Protocol-bound NULL is substituted by ordinary strict planning, unlike a table NULL.
					replicationMessageRoutes(t, p, b, c, replicationMessageNull("SELECT pg_logical_emit_message(true,'p',$1::"+typ+") AS emitted", nil))
					bound := "SELECT pg_logical_emit_message(true,'p',$1::" + typ + ")"
					if !granted {
						s := recoveryControlDenied(bound, "pg_logical_emit_message")
						s.Args = []any{"x"}
						if selected == 1 {
							s.Args = []any{[]byte{0, 255}}
						}
						replicationMessageRoutes(t, p, b, c, s)
					}
					other := 1 - selected
					replicationMessageEmit(t, p, b, fixture.HeldStatement{Executor: c, Statement: statements[other], SQL: queries[other]}, queries[other])
					otherColumn := "b"
					if other == 0 {
						otherColumn = "t"
					}
					replicationMessageRoutes(t, p, b, c, replicationMessageNull("SELECT pg_logical_emit_message(true,'p',"+otherColumn+") AS emitted FROM replication_messages_acl_null"))
					// Explicit flush remains a supplied operand, not a different ACL identity.
					if !granted {
						run(recoveryControlDenied("SELECT pg_logical_emit_message(false,'p',"+message+",true)", "pg_logical_emit_message"))
					}
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				}
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+identity+" TO PUBLIC", 0))
			}
		})
	})
}
