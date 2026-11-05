package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsResetFilters(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgss_reset_filter_role"})
	var databases []string
	fixture.DatabaseReferenceCleanup(t, &databases)
	pgssCase(t, true, func(s *pgssScenario) {
		s.exec("CREATE ROLE pgss_reset_filter_role")
		s.exec("CREATE DATABASE pgss_reset_filter_peer")
		if s.b.PG {
			databases = append(databases, "pgss_reset_filter_peer")
		}
		pool, _, record := fixture.DatabaseScenarioPool(s.t, s.p, s.b.PG, "pgss_reset_filter_peer", "")
		peer, err := pool.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.Close()
		peerStep := func(step fixture.Step) { fixture.RunStep(s.t, s.p.Ctx, peer, record, step) }
		for _, q := range []string{"SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track_planning=off", "SET pg_stat_statements.track=top"} {
			peerStep(fixture.Exec(q, 0))
		}
		const target = "SELECT 17 AS pgss_reset_filter"
		s.exec("CREATE FUNCTION pgss_part.pgss_reset_nested() RETURNS integer LANGUAGE sql VOLATILE SET search_path=pg_catalog AS $$" + target + "$$")
		s.work(fixture.Exec("SET pg_stat_statements.track='all'", 0))
		seed := func() (int64, int64, int64) {
			pgssResetScalar(s, s.observer, pgssResetCall, false)
			s.work(fixture.Select(target, []string{"pgss_reset_filter"}, []string{"INT4"}, [][]any{{int64(17)}}))
			s.work(fixture.Select("SELECT pgss_part.pgss_reset_nested()", []string{"pgss_reset_nested"}, []string{"INT4"}, [][]any{{int64(17)}}))
			s.work(fixture.Exec("SET ROLE pgss_reset_filter_role", 0))
			s.work(fixture.Select(target, []string{"pgss_reset_filter"}, []string{"INT4"}, [][]any{{int64(17)}}))
			s.work(fixture.Exec("RESET ROLE", 0))
			peerStep(fixture.Select(target, []string{"pgss_reset_filter"}, []string{"INT4"}, [][]any{{int64(17)}}))
			s.work(fixture.Select("SELECT 17::bigint AS pgss_reset_other", []string{"pgss_reset_other"}, []string{"INT8"}, [][]any{{int64(17)}}))
			var u, d, q int64
			if err := s.observer.QueryRowContext(s.p.Ctx, "SELECT userid::bigint,dbid::bigint,queryid FROM pgss_part.pg_stat_statements WHERE query='SELECT $1 AS pgss_reset_filter' AND toplevel AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())").Scan(&u, &d, &q); err != nil {
				s.t.Fatal(err)
			}
			s.both(fixture.Select("SELECT count(*) AS n,count(DISTINCT userid) AS users,count(DISTINCT dbid) AS databases,count(DISTINCT toplevel) AS levels FROM pgss_part.pg_stat_statements WHERE queryid=$1", []string{"n", "users", "databases", "levels"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(4), int64(2), int64(2), int64(2)}}, q))
			return u, d, q
		}
		defer s.worker.ExecContext(s.p.Ctx, "RESET ROLE")
		for mask := range 8 {
			for _, prepared := range []bool{false, true} {
				u, d, qid := seed()
				filter := []int64{u, d, qid}
				for i := range filter {
					if mask&(1<<i) == 0 {
						filter[i] = 0
					}
				}
				before := pgssResetRead(s, "SELECT userid::bigint,dbid::bigint,queryid,toplevel,query FROM pgss_part.pg_stat_statements ORDER BY userid,dbid,queryid,toplevel")
				full := pgssResetSnapshot(s)
				var want [][]any
				removed := 0
				for i, row := range before {
					match := true
					for column, value := range filter {
						match = match && (value == 0 || row[column] == value)
					}
					if match {
						removed++
					} else {
						want = append(want, full[i])
					}
				}
				if removed < 2 || mask == 7 && removed != 2 {
					s.t.Fatalf("mask %d selected %d entries, want both levels and only two for conjunction", mask, removed)
				}
				query := "SELECT pgss_part.pg_stat_statements_reset($1::oid,$2::oid,$3::bigint)"
				var x fixture.Executor = s.observer
				if prepared {
					x = pgssResetHeld(s, query)
				}
				pgssResetScalar(s, x, query, false, filter[0], filter[1], filter[2])
				pgssResetEqual(s.t, pgssResetSnapshot(s), want)
			}
		}
		seed()
		before, info := pgssResetSnapshot(s), pgssResetInfo(s)
		for _, args := range []string{"4294967295,0,0", "0,4294967295,0", "0,0,-9223372036854775808", "0,0,9223372036854775807", "0,0,-1"} {
			q := fmt.Sprintf("SELECT pgss_part.pg_stat_statements_reset(%s)", args)
			pgssResetScalar(s, s.observer, q, false)
			pgssResetScalar(s, pgssResetHeld(s, q), q, false)
			pgssResetEqual(s.t, pgssResetSnapshot(s), before)
			pgssResetEqual(s.t, pgssResetInfo(s), info)
		}
	})
}
