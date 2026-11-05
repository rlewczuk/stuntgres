package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatReplicationSlotsNoPublication(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"stat_slots_start_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statsExec(t, p, b, c, "CREATE ROLE stat_slots_start_reader", 0)
		defer c.ExecContext(context.Background(), "RESET ROLE")
		statsExec(t, p, b, c, "CREATE TABLE replication_slots_sentinel(v integer)", 0)
		statsExec(t, p, b, c, "INSERT INTO replication_slots_sentinel VALUES(7)", 1)
		pool := p.Memory
		if b.PG {
			pool = p.Postgres
		}
		peer, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		ownerHeld := prefetchHeld(t, p, c, replicationSlotsSQL)
		defer ownerHeld.Statement.Close()
		peerHeld := prefetchHeld(t, p, peer, replicationSlotsSQL)
		defer peerHeld.Statement.Close()
		check := func() {
			for _, executor := range []fixture.Executor{c, ownerHeld, peer, peerHeld} {
				replicationSlotsEmpty(t, p, b, executor)
			}
			replicationSlotsSentinel(t, p, b, c)
		}
		check()
		replicationSlotsCleanup(t, p, b, "stat_slots_publication_logical", "stat_slots_publication_physical")
		for _, tc := range []struct {
			query, name string
			logical     bool
		}{
			{"SELECT * FROM pg_create_logical_replication_slot('stat_slots_publication_logical','test_decoding')", "stat_slots_publication_logical", true},
			{"SELECT * FROM pg_create_physical_replication_slot('stat_slots_publication_physical')", "stat_slots_publication_physical", false},
		} {
			for _, prepared := range []bool{false, true} {
				if b.PG || tc.logical {
					executor := fixture.Executor(c)
					if prepared {
						held := prefetchHeld(t, p, c, tc.query)
						defer held.Statement.Close()
						executor = held
					}
					rows, err := executor.QueryContext(p.Ctx, tc.query)
					if err != nil {
						t.Fatal(err)
					}
					var name string
					var lsn sql.NullString
					if !rows.Next() {
						t.Fatal("missing genuine created slot", rows.Err())
					}
					if err := rows.Scan(&name, &lsn); err != nil || name != tc.name || lsn.Valid != tc.logical {
						t.Fatal("actual slot identity/position", name, lsn, err)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("one created slot", rows.Err())
					}
					rows.Close()
					resetReplicationVoid(t, p, b, c, "SELECT pg_drop_replication_slot($1::name)", "pg_drop_replication_slot", tc.name)
				} else {
					step := fixture.ErrorDiagnostic(tc.query, replicationSlotsBoundary())
					step.Prepared, step.SelectRows, step.ErrorStage = prepared, true, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				}
				check()
			}
		}
		statsExec(t, p, b, c, "SET ROLE stat_slots_start_reader", 0)
		denied := activityDiagnostic("42501", "permission denied to use replication slots", 0)
		denied.Detail = "Only roles with the REPLICATION attribute may use replication slots."
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic("SELECT * FROM pg_create_logical_replication_slot('stat_slots_denied','test_decoding')", denied)
			step.Prepared, step.SelectRows, step.ErrorStage = prepared, true, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			for _, executor := range []fixture.Executor{c, ownerHeld, peer, peerHeld} {
				replicationSlotsEmpty(t, p, b, executor)
			}
			replicationSlotsSentinel(t, p, b, peer)
		}
		statsExec(t, p, b, c, "RESET ROLE", 0)
		for _, executor := range []fixture.Executor{c, prefetchHeld(t, p, c, "SELECT pg_stat_reset_replication_slot(NULL)")} {
			if held, ok := executor.(fixture.HeldStatement); ok {
				defer held.Statement.Close()
			}
			resetReplicationVoid(t, p, b, executor, "SELECT pg_stat_reset_replication_slot(NULL)", "pg_stat_reset_replication_slot")
			check()
		}
		resetReplicationRuntimeError(t, p, b, c, "SELECT pg_stat_reset_replication_slot('stat_slots_absent')", activityDiagnostic("22023", `replication slot "stat_slots_absent" does not exist`, 0))
		check()
	})
}
