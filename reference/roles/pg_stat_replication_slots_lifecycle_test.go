package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatReplicationSlotsLifecycle(t *testing.T) {
	slotCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		const logical, physical = "stat_slots_lifecycle_logical", "stat_slots_lifecycle_physical"
		if s.Backend.PG {
			slotCleanup(s.T, db, logical, physical)
		}
		defer s.C.ExecContext(context.Background(), "RESET ROLE")
		const query = "SELECT * FROM pg_catalog.pg_stat_replication_slots WHERE slot_name=$1::text"
		statement, err := s.C.PrepareContext(s.P.Ctx, query)
		if err != nil {
			s.T.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: s.C, Statement: statement, SQL: query}
		names := []string{"slot_name", "spill_txns", "spill_count", "spill_bytes", "stream_txns", "stream_count", "stream_bytes", "total_txns", "total_bytes", "stats_reset"}
		types := []string{"TEXT", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "TIMESTAMPTZ"}
		empty := func(name string) {
			s.T.Helper()
			for _, executor := range []fixture.Executor{s.C, held} {
				fixture.RunStep(s.T, s.P.Ctx, executor, s.Record, fixture.Select(query, names, types, nil, name))
			}
		}
		clear := func() {
			s.T.Helper()
			predefinedSelect(s, "SELECT pg_stat_clear_snapshot()", []string{"pg_stat_clear_snapshot"}, []string{"VOID"}, [][]any{{""}})
		}
		clock := func() time.Time {
			var stamp time.Time
			if err := s.C.QueryRowContext(s.P.Ctx, "SELECT clock_timestamp()").Scan(&stamp); err != nil {
				s.T.Fatal(err)
			}
			return stamp
		}
		empty(logical)
		empty(physical)
		lower := clock()
		slotCreate(s, logical, true, false, false)
		slotCreate(s, physical, false, false, false)
		upper := clock()
		clear()
		empty(physical)
		if !s.Backend.PG {
			empty(logical)
			return
		}
		read := func(executor fixture.Executor) sql.NullTime {
			s.T.Helper()
			rows, err := executor.QueryContext(s.P.Ctx, query, logical)
			if err != nil {
				s.T.Fatal(err)
			}
			defer rows.Close()
			if !rows.Next() {
				s.T.Fatal("genuine logical slot missing", rows.Err())
			}
			var name string
			var counters [8]int64
			var reset sql.NullTime
			if err := rows.Scan(&name, &counters[0], &counters[1], &counters[2], &counters[3], &counters[4], &counters[5], &counters[6], &counters[7], &reset); err != nil {
				s.T.Fatal("actual slot's counters must be non-NULL", err)
			}
			if name != logical || counters != [8]int64{} {
				s.T.Fatal("exact logical identity and initial counters", name, counters)
			}
			if reset.Valid && (reset.Time.IsZero() || reset.Time.Before(lower) || reset.Time.After(upper) || reset.Time.Nanosecond()%1000 != 0) {
				s.T.Fatal("actual reset metadata outside creation bounds", reset, lower, upper)
			}
			if rows.Next() || rows.Err() != nil {
				s.T.Fatal("exactly one logical slot", rows.Err())
			}
			return reset
		}
		before := read(s.C)
		same := func() {
			for _, executor := range []fixture.Executor{s.C, held} {
				got := read(executor)
				if got.Valid != before.Valid || !got.Time.Equal(before.Time) {
					s.T.Fatal("slot fields were redacted or changed", before, got)
				}
			}
		}
		same()
		s.Exec("ALTER ROLE rsl_repl REPLICATION")
		s.Exec("GRANT pg_read_all_stats TO rsl_member")
		for _, role := range []string{"rsl_actor", "rsl_repl", "rsl_member"} {
			s.Exec("SET ROLE " + role)
			same()
			empty(physical)
			s.Exec("RESET ROLE")
		}
		// Reuse the real member fixture with a distinct current membership cut.
		s.Exec("REVOKE pg_read_all_stats FROM rsl_member")
		s.Exec("GRANT pg_monitor TO rsl_member")
		s.Exec("SET ROLE rsl_member")
		same()
		s.Exec("RESET ROLE")
		slotDrop(s, logical)
		clear()
		empty(logical)
		empty(physical)
		slotDrop(s, physical)
		clear()
		empty(logical)
		empty(physical)
	})
}
