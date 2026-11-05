package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsCountersRead(t *testing.T) {
	t.Run("frontiers", pgssCounterReadFrontiers)
	t.Run("cold_warm_held_and_prepare", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			pgssCounterSetup(s)
			s.exec("CREATE TABLE pgss_part.pgss_counter_read(k integer,v text)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_counter_read VALUES(1,'a'),(2,'b')", 2))
			q := "SELECT k,v FROM pgss_part.pgss_counter_read ORDER BY k"
			step := fixture.Select(q, []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}})
			s.counts(q, 0, 0, 0)
			held, err := s.worker.PrepareContext(s.p.Ctx, q)
			if err != nil {
				s.t.Fatal(err)
			}
			defer held.Close()
			s.counts(q, 0, 0, 0) // Parse/Describe are not reads.
			s.work(step)
			pgssCounterCheck(s, q, 1, 2, pgssCounterWant{read: 1, pgShared: true, serial: true})
			fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.worker, Statement: held, SQL: q}, s.b.Recorder, step)
			pgssCounterCheck(s, q, 2, 4, pgssCounterWant{hit: 1, read: 1, pgShared: true, serial: true})
			s.work(fixture.Prepared(step))
			pgssCounterCheck(s, q, 3, 6, pgssCounterWant{hit: 2, read: 1, pgShared: true, serial: true})
		})
	})
	t.Run("source_not_output_cardinality", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			pgssCounterSetup(s)
			for _, name := range []string{"one", "two", "empty"} {
				s.exec("CREATE TABLE pgss_part.pgss_counter_" + name + "(k integer)")
				if name != "empty" {
					s.run(fixture.Exec("INSERT INTO pgss_part.pgss_counter_"+name+" VALUES(1)", 1))
				}
			}
			for _, tc := range []struct {
				q        string
				rows     [][]any
				requests int64
			}{
				{"SELECT a.k AS v FROM pgss_part.pgss_counter_one a", [][]any{{int64(1)}}, 1},
				{"SELECT a.k AS v FROM pgss_part.pgss_counter_one a CROSS JOIN pgss_part.pgss_counter_two b", [][]any{{int64(1)}}, 2},
				{"SELECT k AS v FROM pgss_part.pgss_counter_one rejected WHERE k=99", nil, 1},
				{"SELECT k AS v FROM pgss_part.pgss_counter_empty empty_source", nil, 0},
				{"SELECT CASE WHEN true THEN 7 ELSE (SELECT k FROM pgss_part.pgss_counter_two unchosen) END AS v", [][]any{{int64(7)}}, 0},
			} {
				pattern := strings.ReplaceAll(tc.q, "99", "%")
				if strings.Contains(tc.q, "CASE WHEN") {
					pattern = "SELECT CASE WHEN % THEN % ELSE (SELECT k FROM pgss_part.pgss_counter_two unchosen) END AS v"
				}
				s.counts(pattern, 0, 0, 0)
				s.work(fixture.Select(tc.q, []string{"v"}, []string{"INT4"}, tc.rows))
				pgssCounterCheck(s, pattern, 1, int64(len(tc.rows)), pgssCounterWant{anySharedSplit: true, sharedRequests: tc.requests, pgShared: tc.requests > 0, serial: true})
			}
		})
	})
	t.Run("temp_and_private_resident_sources", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			pgssCounterSetup(s)
			s.work(fixture.Exec("CREATE TEMP TABLE pgss_counter_temp(k integer)", 0))
			s.work(fixture.Exec("INSERT INTO pgss_counter_temp VALUES(9)", 1))
			q := "SELECT k FROM pgss_counter_temp"
			step := fixture.Select(q, []string{"k"}, []string{"INT4"}, [][]any{{int64(9)}})
			s.counts(q, 0, 0, 0)
			s.work(step)
			pgssCounterCheck(s, q, 1, 1, pgssCounterWant{localHit: 1, pgLocal: true, pgNoWAL: true, serial: true})
			s.work(fixture.Prepared(step))
			pgssCounterCheck(s, q, 2, 2, pgssCounterWant{localHit: 2, pgLocal: true, pgNoWAL: true, serial: true})
			s.work(fixture.Exec("BEGIN", 0))
			defer s.worker.ExecContext(s.p.Ctx, "ROLLBACK")
			s.work(fixture.Exec("CREATE TABLE pgss_part.pgss_counter_private(k integer)", 0))
			s.work(fixture.Exec("INSERT INTO pgss_part.pgss_counter_private VALUES(12)", 1))
			q = "SELECT k FROM pgss_part.pgss_counter_private"
			s.work(fixture.Select(q, []string{"k"}, []string{"INT4"}, [][]any{{int64(12)}}))
			pgssCounterCheck(s, q, 1, 1, pgssCounterWant{hit: 1, pgShared: true, serial: true})
			s.work(fixture.Exec("ROLLBACK", 0))
			pgssCounterCheck(s, q, 1, 1, pgssCounterWant{hit: 1, pgShared: true, serial: true})
		})
	})
	t.Run("reached_hierarchy_and_materialized_replay", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			pgssCounterSetup(s)
			s.exec("CREATE TABLE pgss_part.pgss_counter_parent(k integer)")
			s.exec("CREATE TABLE pgss_part.pgss_counter_child() INHERITS(pgss_part.pgss_counter_parent)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_counter_parent VALUES(1)", 1))
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_counter_child VALUES(1)", 1))
			q := "SELECT k FROM pgss_part.pgss_counter_parent reached LIMIT 1"
			s.work(fixture.Select(q, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			pgssCounterCheck(s, "SELECT k FROM pgss_part.pgss_counter_parent reached LIMIT %", 1, 1, pgssCounterWant{read: 1, pgShared: true, serial: true})
			q = "SELECT k FROM pgss_part.pgss_counter_parent drained ORDER BY k"
			s.work(fixture.Select(q, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
			pgssCounterCheck(s, q, 1, 2, pgssCounterWant{hit: 1, read: 1, pgShared: true, serial: true})
			q = "WITH c AS MATERIALIZED (SELECT k FROM ONLY pgss_part.pgss_counter_parent materialized) SELECT a.k FROM c a CROSS JOIN c b"
			s.work(fixture.Select(q, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			pgssCounterCheck(s, q, 1, 1, pgssCounterWant{hit: 1, pgShared: true, serial: true})
		})
	})
	t.Run("selected_hash_index", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			pgssCounterSetup(s)
			s.exec("CREATE TABLE pgss_part.pgss_counter_index(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_counter_index VALUES(1),(2),(3)", 3))
			s.exec("CREATE INDEX pgss_counter_hash ON pgss_part.pgss_counter_index USING hash(k)")
			s.work(fixture.Exec("SET enable_seqscan=off", 0))
			q := "SELECT k FROM pgss_part.pgss_counter_index indexed WHERE k=$1::integer"
			// Assert actual path selection rather than inferring work from DDL.
			plan, err := s.worker.QueryContext(s.p.Ctx, "EXPLAIN "+q, int64(2))
			if err != nil {
				s.t.Fatal(err)
			}
			selected := false
			for plan.Next() {
				var line string
				if err := plan.Scan(&line); err != nil {
					s.t.Fatal(err)
				}
				selected = selected || strings.Contains(line, "pgss_counter_hash")
			}
			if err := plan.Err(); err != nil {
				s.t.Fatal(err)
			}
			plan.Close()
			if !selected {
				s.t.Fatal("forced hash path did not select pgss_counter_hash")
			}
			s.counts(q, 0, 0, 0)
			s.work(fixture.Prepared(fixture.Select(q, []string{"k"}, []string{"INT4"}, [][]any{{int64(2)}}, int64(2))))
			pgssCounterCheck(s, q, 1, 1, pgssCounterWant{anySharedSplit: true, sharedRequests: 2, pgShared: true, serial: true})
		})
	})
}
