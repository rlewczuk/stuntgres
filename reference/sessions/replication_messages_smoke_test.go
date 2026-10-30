package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceReplicationMessagesFeatureSmoke(t *testing.T) {
	const role = "replication_messages_smoke_reader"
	const replicator = "replication_messages_smoke_replicator"
	fixture.RoleReferenceCleanup(t, []string{role, replicator})
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE "+role, 0))
		run(fixture.Exec("CREATE ROLE "+replicator+" REPLICATION", 0))
		defer replicationMessagesRestore(t, c, b.PG, role, replicator)()
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		run(fixture.Exec("CREATE TABLE replication_messages_smoke_sentinel(v integer)", 0))
		run(fixture.Exec("INSERT INTO replication_messages_smoke_sentinel VALUES(7)", 1))
		run(fixture.Exec("GRANT SELECT ON replication_messages_smoke_sentinel TO "+role, 0))
		run(fixture.Exec("SET application_name='replication_messages_smoke'", 0))
		q := "SELECT pg_logical_emit_message(message=>'x',prefix=>'p',transactional=>false,flush=>true)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		replicationMessageEmitRoutes(t, p, b, c, q)
		replicationMessageEmitRoutes(t, p, b, c, `SELECT pg_logical_emit_message(false,'p','\x00ff'::bytea,true)`)
		replicationMessageRoutes(t, p, b, c, replicationMessageNull("SELECT pg_logical_emit_message(true,'p',NULL::text) AS emitted"))
		replicationMessageRoutes(t, p, b, c, fixture.Select("SELECT COALESCE(pg_logical_emit_message(true,'p',NULL::bytea),'0/1'::pg_lsn) AS emitted", []string{"emitted"}, []string{"PG_LSN"}, [][]any{{"0/1"}}))
		names := []string{"a", "b", "c"}
		metadata := fixture.Select("SELECT pg_logical_emit_message(true,'p','x') AS a,pg_logical_emit_message(false,'p','\\x00'::bytea) AS b,pg_sync_replication_slots() AS c WHERE FALSE", names, []string{"PG_LSN", "PG_LSN", "VOID"}, nil)
		metadata.CheckMetadata = replicationMessagesMetadata(b.PG, names)
		replicationMessageRoutes(t, p, b, c, metadata)
		replicationMessageRoutes(t, p, b, c, replicationMessageSync("SELECT pg_sync_replication_slots()", b.PG, false))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+replicator, 0))
		replicationMessageRoutes(t, p, b, c, replicationMessageSync("SELECT pg_sync_replication_slots()", b.PG, true))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+replicationMessageIdentities[0]+" FROM PUBLIC", 0))
		for _, granted := range []bool{false, true, false} {
			action := "REVOKE EXECUTE ON FUNCTION " + replicationMessageIdentities[0] + " FROM " + role
			if granted {
				action = "GRANT EXECUTE ON FUNCTION " + replicationMessageIdentities[0] + " TO " + role
			}
			run(fixture.Exec(action, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			if granted {
				replicationMessageEmit(t, p, b, held, q)
			} else {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, recoveryControlDenied(q, "pg_logical_emit_message"))
			}
			replicationMessageEmit(t, p, b, c, `SELECT pg_logical_emit_message(false,'p','\x00'::bytea)`)
			run(fixture.Select("SELECT v FROM replication_messages_smoke_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{role, role}}))
			run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"replication_messages_smoke"}}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
	})
}
