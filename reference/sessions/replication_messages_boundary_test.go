package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceReplicationMessagesPhysicalBoundary(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE replication_messages_sentinel(v integer)", 0))
		run(fixture.Exec("INSERT INTO replication_messages_sentinel VALUES(7)", 1))
		run(fixture.Exec("SET application_name='replication_messages_boundary'", 0))
		var session, current string
		if err := c.QueryRowContext(p.Ctx, "SELECT session_user,current_user").Scan(&session, &current); err != nil {
			t.Fatal(err)
		}
		check := func() {
			run(fixture.Select("SELECT v FROM replication_messages_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{session, current}}))
			run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"replication_messages_boundary"}}))
			if !b.PG {
				// This engine has no physical slot catalog view. Preserve that
				// observable absence rather than inventing an empty inventory.
				s := fixture.ImplicitError("SELECT slot_name FROM pg_replication_slots ORDER BY slot_name", fixture.Diagnostic{Code: "42P01", Message: `relation "pg_replication_slots" does not exist`, Position: fixture.Position(23)})
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
				s.Prepared, s.ErrorStage = true, "prepare"
				run(s)
			}
		}
		check()
		for _, call := range []string{"pg_logical_emit_message(false,'p','x')", "pg_logical_emit_message(true,'p','x',true)", `pg_logical_emit_message(false,'p','\x00ff'::bytea,true)`, `pg_logical_emit_message(true,'','\x'::bytea,false)`} {
			replicationMessageEmitRoutes(t, p, b, c, "SELECT "+call)
			check()
		}
		replicationMessageRoutes(t, p, b, c, replicationMessageSync("SELECT pg_sync_replication_slots()", b.PG, true))
		check()
		if !b.PG {
			for _, q := range []string{"SELECT pg_sync_replication_slots()", "SELECT pg_logical_emit_message(true,'p','x')"} {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				rows, err := stmt.QueryContext(ctx)
				if rows != nil {
					rows.Close()
					t.Fatal("cancellation published physical result")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("context error hidden", err)
				}
				stmt.Close()
			}
			check()
		}
	})
}
