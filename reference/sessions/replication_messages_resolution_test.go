package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceReplicationMessagesResolution(t *testing.T) {
	for _, message := range []string{"'x'", "'x'::text", `'\x00ff'::bytea`} {
		for _, transactional := range []string{"false", "true"} {
			for _, flush := range []string{"", ",false", ",true"} {
				t.Run(message+transactional+flush, func(t *testing.T) {
					recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						replicationMessageEmitRoutes(t, p, b, c, "SELECT pg_logical_emit_message("+transactional+",'p',"+message+flush+")")
					})
				})
			}
		}
	}
	t.Run("named-domain-empty", func(t *testing.T) {
		recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE DOMAIN replication_messages_text AS text", 0))
			run(fixture.Exec("CREATE DOMAIN replication_messages_bytes AS bytea", 0))
			for _, call := range []string{
				"pg_logical_emit_message(true,'','')", `pg_logical_emit_message(false,'','\x'::bytea,true)`,
				"pg_logical_emit_message(true,'p','v'::varchar)", "pg_logical_emit_message(true,'p','v'::replication_messages_text)", `pg_logical_emit_message(true,'p','\x00ff'::replication_messages_bytes)`,
				"pg_logical_emit_message(message=>'x',prefix=>'p',transactional=>true,flush=>true)", `pg_logical_emit_message(message:='\x00'::bytea,prefix:='p',transactional:=false)`,
				"pg_logical_emit_message(true,prefix=>'p',message=>'x')", "PG_LOGICAL_EMIT_MESSAGE(MESSAGE=>'x',PREFIX=>'p',TRANSACTIONAL=>false)",
			} {
				replicationMessageEmitRoutes(t, p, b, c, "SELECT "+call)
			}
			replicationMessageEmitRoutes(t, p, b, c, "SELECT pg_logical_emit_message(true,'p',$1)", "unknown text")
			replicationMessageEmitRoutes(t, p, b, c, "SELECT pg_logical_emit_message(true,'p',$1::bytea)", []byte{0, 255})
		})
	})
	t.Run("observable-overload", func(t *testing.T) {
		const role = "replication_messages_resolution_reader"
		fixture.RoleReferenceCleanup(t, []string{role})
		recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE "+role, 0))
			defer replicationMessagesRestore(t, c, b.PG, role)()
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
			run(fixture.Exec("CREATE DOMAIN replication_messages_acl_text AS text", 0))
			run(fixture.Exec("CREATE DOMAIN replication_messages_acl_bytes AS bytea", 0))
			run(fixture.Exec("CREATE TABLE replication_messages_choices(t text,b bytea,u boolean)", 0))
			run(fixture.Exec("INSERT INTO replication_messages_choices VALUES(NULL,NULL,NULL)", 1))
			run(fixture.Exec("GRANT SELECT ON replication_messages_choices TO "+role, 0))
			messages := []struct {
				sql    string
				binary bool
			}{
				{"t", false}, {"t::varchar", false}, {"t::replication_messages_acl_text", false}, {"b", true}, {"b::replication_messages_acl_bytes", true}, {"'unknown'", false}, {"NULL", false}, {"$1", false}, {"$1::bytea", true},
			}
			// u is a table NULL: the invocation survives planning even when the
			// message is an unknown literal or parameter, making the selected ACL visible.
			for revoked := range 2 {
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+replicationMessageIdentities[revoked]+" FROM PUBLIC", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
				for _, message := range messages {
					q := "SELECT pg_logical_emit_message(u,'p'," + message.sql + ") AS emitted FROM replication_messages_choices"
					var args []any
					if message.sql == "$1" {
						args = []any{"x"}
					}
					if message.sql == "$1::bytea" {
						args = []any{[]byte{0, 255}}
					}
					s := replicationMessageNull(q, args...)
					if message.binary == (revoked == 1) && message.sql != "NULL" {
						s = recoveryControlDenied(q, "pg_logical_emit_message")
						s.Args = args
					}
					// A constant NULL message itself erases the strict call before ACL.
					replicationMessageRoutes(t, p, b, c, s)
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+replicationMessageIdentities[revoked]+" TO PUBLIC", 0))
			}
			// A typed binary prepared call keeps its identity across payload changes.
			q := "SELECT pg_logical_emit_message(u,'p',$1::bytea) AS emitted FROM replication_messages_choices"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+replicationMessageIdentities[0]+" FROM PUBLIC", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, arg := range []any{[]byte{0, 255}, []byte{}, "raw"} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, replicationMessageNull(q, arg))
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	})
}
