package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatReplicationSlotsFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"stat_slots_smoke_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		statsExec(t, p, b, c, "CREATE ROLE stat_slots_smoke_reader", 0)
		defer c.ExecContext(context.Background(), "RESET ROLE")
		statsExec(t, p, b, c, "CREATE TABLE replication_slots_sentinel(v integer)", 0)
		statsExec(t, p, b, c, "INSERT INTO replication_slots_sentinel VALUES(7)", 1)
		for _, query := range []string{replicationSlotsSQL, "SELECT * FROM pg_stat_replication_slots"} {
			step := fixture.Select(query, replicationSlotsNames, replicationSlotsTypes, nil)
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { replicationSlotsMetadata(t, rows, b.PG) }
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}
		const logical, physical = "stat_slots_smoke_logical", "stat_slots_smoke_physical"
		const query = replicationSlotsSQL + " WHERE slot_name=$1::text"
		held := prefetchHeld(t, p, c, query)
		defer held.Statement.Close()
		empty := func(executor fixture.Executor, name string) {
			t.Helper()
			step := fixture.Select(query, replicationSlotsNames, replicationSlotsTypes, nil, name)
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { replicationSlotsMetadata(t, rows, b.PG) }
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
		}
		empty(held, logical)
		statsExec(t, p, b, c, "SET ROLE stat_slots_smoke_reader", 0)
		empty(c, logical)
		empty(held, logical)
		statsExec(t, p, b, c, "RESET ROLE", 0)
		pool := p.Memory
		if b.PG {
			pool = p.Postgres
		}
		peer, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		if !b.PG {
			step := fixture.ErrorDiagnostic("SELECT * FROM pg_create_logical_replication_slot('stat_slots_smoke_logical','test_decoding')", replicationSlotsBoundary(true))
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			empty(c, logical)
			empty(held, logical)
			replicationSlotsEmpty(t, p, b, peer)
		} else {
			replicationSlotsCleanup(t, p, b, logical, physical)
			lower := prefetchClock(t, p, c)
			var name, lsn string
			if err := c.QueryRowContext(p.Ctx, "SELECT * FROM pg_create_logical_replication_slot('stat_slots_smoke_logical','test_decoding')").Scan(&name, &lsn); err != nil || name != logical || lsn == "" {
				t.Fatal("genuine logical creation", name, lsn, err)
			}
			var physicalLSN sql.NullString
			if err := c.QueryRowContext(p.Ctx, "SELECT * FROM pg_create_physical_replication_slot('stat_slots_smoke_physical')").Scan(&name, &physicalLSN); err != nil || name != physical || physicalLSN.Valid {
				t.Fatal("genuine physical creation", name, physicalLSN, err)
			}
			upper := prefetchClock(t, p, c)
			resetReplicationVoid(t, p, b, c, "SELECT pg_stat_clear_snapshot()", "pg_stat_clear_snapshot")
			read := func(executor fixture.Executor) sql.NullTime {
				rows, err := executor.QueryContext(p.Ctx, query, logical)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				replicationSlotsMetadata(t, rows, b.PG)
				if !rows.Next() {
					t.Fatal("genuine logical slot missing", rows.Err())
				}
				var counters [8]int64
				var reset sql.NullTime
				if err := rows.Scan(&name, &counters[0], &counters[1], &counters[2], &counters[3], &counters[4], &counters[5], &counters[6], &counters[7], &reset); err != nil || name != logical || counters != [8]int64{} {
					t.Fatal("logical name/eight non-NULL initial zeros", name, counters, err)
				}
				if reset.Valid && (reset.Time.IsZero() || reset.Time.Before(lower) || reset.Time.After(upper) || reset.Time.Nanosecond()%1000 != 0) {
					t.Fatal("reset outside creation bounds", reset, lower, upper)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("exactly one genuine logical slot", rows.Err())
				}
				return reset
			}
			before := read(c)
			statsExec(t, p, b, c, "SET ROLE stat_slots_smoke_reader", 0)
			for _, executor := range []fixture.Executor{c, held} {
				got := read(executor)
				if got.Valid != before.Valid || !got.Time.Equal(before.Time) {
					t.Fatal("ordinary role redacted slot statistics", before, got)
				}
				empty(executor, physical)
			}
			statsExec(t, p, b, c, "RESET ROLE", 0)
			for _, slot := range []string{logical, physical} {
				resetReplicationVoid(t, p, b, c, "SELECT pg_drop_replication_slot($1::name)", "pg_drop_replication_slot", slot)
			}
			resetReplicationVoid(t, p, b, c, "SELECT pg_stat_clear_snapshot()", "pg_stat_clear_snapshot")
			empty(c, logical)
			empty(held, logical)
			replicationSlotsEmpty(t, p, b, peer)
		}
		step := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_replication_slots SET total_txns=1 WHERE FALSE", replicationSlotsMutationDiagnostic("UPDATE"))
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		empty(c, logical)
		empty(held, logical)
		replicationSlotsSentinel(t, p, b, c)
	})
}
