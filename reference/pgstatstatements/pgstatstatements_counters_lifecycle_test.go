package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsCountersLifecycle(t *testing.T) {
	t.Run("counts_independent_and_rollback", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			pgssCounterSetup(s)
			s.exec("CREATE TABLE pgss_part.pgss_counter_counts(k integer,v text)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_counter_counts VALUES(1,'a')", 1))
			s.work(fixture.Exec("SET track_counts=off", 0))
			read := "SELECT k FROM pgss_part.pgss_counter_counts counts_off"
			s.work(fixture.Prepared(fixture.Select(read, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}})))
			pgssCounterCheck(s, read, 1, 1, pgssCounterWant{read: 1, pgShared: true, serial: true})
			s.work(fixture.Exec("BEGIN", 0))
			defer s.worker.ExecContext(s.p.Ctx, "ROLLBACK")
			s.work(fixture.Exec("SAVEPOINT before_image", 0))
			insert := "INSERT INTO pgss_part.pgss_counter_counts VALUES($1::integer,$2::text)"
			s.work(fixture.Prepared(fixture.Exec(insert, 1, int64(2), "rollback")))
			pgssCounterCheck(s, insert, 1, 1, pgssCounterWant{dirty: 1, records: 1, bytes: 72, pgWAL: true, serial: true})
			s.work(fixture.Exec("ROLLBACK TO before_image", 0))
			s.work(fixture.Exec("ROLLBACK", 0))
			pgssCounterCheck(s, insert, 1, 1, pgssCounterWant{dirty: 1, records: 1, bytes: 72, pgWAL: true, serial: true})
			s.both(fixture.Select("SELECT k,v FROM pgss_part.pgss_counter_counts", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}}))
		})
	})
	t.Run("accepted_plan_failed_execution", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			pgssCounterSetup(s)
			s.exec("CREATE TABLE pgss_part.pgss_counter_phase(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_counter_phase VALUES(1)", 1))
			s.work(fixture.Exec("SET pg_stat_statements.track_planning=on", 0))
			s.work(fixture.Exec("SET plan_cache_mode=force_custom_plan", 0))
			q := "SELECT k/$1::integer AS v FROM pgss_part.pgss_counter_phase phase_read"
			held, err := s.worker.PrepareContext(s.p.Ctx, q)
			if err != nil {
				s.t.Fatal(err)
			}
			defer held.Close()
			x := fixture.HeldStatement{Executor: s.worker, Statement: held, SQL: q}
			s.planCounts(q, 0, 0, 0, 0)
			failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, int64(0))
			failure.SelectRows, failure.ErrorStage = true, "execute"
			fixture.RunStep(s.t, s.p.Ctx, x, s.b.Recorder, failure)
			s.planCounts(q, 1, 1, 0, 0)
			pgssCounterCheck(s, q, 0, 0, pgssCounterWant{serial: true})
			fixture.RunStep(s.t, s.p.Ctx, x, s.b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}, int64(1)))
			s.planCounts(q, 1, 2, 1, 1)
			// The failed traversal may warm cache, but it has no published vector.
			pgssCounterCheck(s, q, 1, 1, pgssCounterWant{hit: 1, pgShared: true, serial: true})
		})
	})
	for _, policy := range []string{"top", "all"} {
		t.Run("inclusive_"+policy, func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				pgssCounterSetup(s)
				base := "pgss_part.pgss_counter_nested_" + policy
				s.exec("CREATE TABLE " + base + "(k integer)")
				s.run(fixture.Exec("INSERT INTO "+base+" VALUES(7)", 1))
				s.exec("CREATE FUNCTION " + base + "_fn() RETURNS integer LANGUAGE sql VOLATILE SET search_path=pg_catalog AS $$SELECT k FROM " + base + "$$")
				s.work(fixture.Exec("SET pg_stat_statements.track='"+policy+"'", 0))
				outer := "SELECT " + base + "_fn() AS v"
				s.work(fixture.Prepared(fixture.Select(outer, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})))
				pgssCounterCheck(s, outer, 1, 1, pgssCounterWant{read: 1, pgShared: true, serial: true})
				child := "SELECT k FROM " + base
				if policy == "all" {
					pgssCounterCheckTop(s, child, false, 1, 1, pgssCounterWant{read: 1, pgShared: true, serial: true})
				} else {
					s.nestedCounts(child, false, 0, 0, 0)
				}
				caught := base + "_caught"
				s.exec("CREATE FUNCTION " + caught + "() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM k/(k-k) FROM " + base + "; RETURN 0; EXCEPTION WHEN division_by_zero THEN RETURN 9; END$$")
				q := "SELECT " + caught + "() AS v"
				s.work(fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
				pgssCounterCheck(s, q, 1, 1, pgssCounterWant{hit: 1, pgShared: true, serial: true})
				s.nestedCounts("SELECT k/(k-k) FROM "+base, false, 0, 0, 0)
			})
		})
	}
	t.Run("subset_minmax_and_full_reset", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			pgssCounterSetup(s)
			pgssResetScalar(s, s.observer, pgssResetCall, false)
			a, b := pgssResetSeed(s)
			beforeA, beforeB, info := pgssCounterSnapshot(s, a), pgssCounterSnapshot(s, b), pgssResetInfo(s)
			pgssCounterCheck(s, a, 2, 3, pgssCounterWant{read: 1, hit: 1, pgShared: true, serial: true})
			pgssCounterCheck(s, b, 1, 1, pgssCounterWant{hit: 1, pgShared: true, serial: true})
			u, d, id := pgssResetIdentity(s, a)
			pgssResetScalar(s, s.observer, "SELECT pgss_part.pg_stat_statements_reset($1::oid,$2::oid,$3::bigint,true)", false, u, d, id)
			pgssResetEqual(s.t, pgssCounterSnapshot(s, a), beforeA)
			pgssResetEqual(s.t, pgssResetInfo(s), info)
			pgssResetScalar(s, s.observer, "SELECT pgss_part.pg_stat_statements_reset($1::oid,$2::oid,$3::bigint,false)", false, u, d, id)
			s.counts(a, 0, 0, 0)
			pgssResetEqual(s.t, pgssCounterSnapshot(s, b), beforeB)
			pgssResetEqual(s.t, pgssResetInfo(s), info)
			at := pgssResetScalar(s, s.observer, pgssResetCall, false)
			s.counts(b, 0, 0, 0)
			s.both(fixture.Select("SELECT dealloc,stats_reset=$1 AS same FROM pgss_part.pg_stat_statements_info", []string{"dealloc", "same"}, []string{"INT8", "BOOL"}, [][]any{{int64(0), true}}, at))
		})
	})
}
