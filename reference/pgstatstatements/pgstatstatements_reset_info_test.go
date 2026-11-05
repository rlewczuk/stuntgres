package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsResetInfo(t *testing.T) {
	t.Run("outcome_edges", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			pgssResetScalar(s, s.observer, pgssResetCall, false)
			for _, args := range []string{"0,0,0,false", "4294967295,4294967295,-1,false", "0,0,0,true", "4294967295,4294967295,-1,true"} {
				q := "SELECT pgss_part.pg_stat_statements_reset(" + args + ")"
				at := pgssResetScalar(s, s.observer, q, false)
				s.both(fixture.Select("SELECT dealloc,stats_reset=$1 AS same FROM pgss_part.pg_stat_statements_info", []string{"dealloc", "same"}, []string{"INT8", "BOOL"}, [][]any{{int64(0), true}}, at))
				pgssResetEqual(s.t, pgssResetSnapshot(s), [][]any(nil))
			}
			a, b := pgssResetSeed(s)
			info := pgssResetInfo(s)
			for _, args := range []string{"4294967295,0,0,false", "0,0,-1,true", "0,0,0,true"} {
				pgssResetScalar(s, s.observer, "SELECT pgss_part.pg_stat_statements_reset("+args+")", false)
				pgssResetEqual(s.t, pgssResetInfo(s), info)
			}
			u, d, qid := pgssResetIdentity(s, a)
			q := "SELECT pgss_part.pg_stat_statements_reset($1::oid,$2::oid,$3::bigint)"
			pgssResetScalar(s, s.observer, q, false, u, d, qid)
			pgssResetEqual(s.t, pgssResetInfo(s), info)
			u, d, qid = pgssResetIdentity(s, b)
			at := pgssResetScalar(s, pgssResetHeld(s, q), q, false, u, d, qid)
			s.both(fixture.Select("SELECT dealloc,stats_reset=$1 AS same FROM pgss_part.pg_stat_statements_info", []string{"dealloc", "same"}, []string{"INT8", "BOOL"}, [][]any{{int64(0), true}}, at))
			pgssResetEqual(s.t, pgssResetSnapshot(s), [][]any(nil))
		})
	})
	t.Run("real_deallocation", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			pgssResetScalar(s, s.observer, pgssResetCall, false)
			s.exec("CREATE TABLE pgss_part.pgss_reset_pressure(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_reset_pressure VALUES(7)", 1))
			for i := range 5100 {
				alias := fmt.Sprintf("pgss_reset_pressure_%04d", i)
				q := "SELECT " + alias + ".k FROM pgss_part.pgss_reset_pressure " + alias
				s.work(fixture.Select(q, []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
			s.both(fixture.Select("SELECT dealloc>0 AS evicted FROM pgss_part.pg_stat_statements_info", []string{"evicted"}, []string{"BOOL"}, [][]any{{true}}))
			info := pgssResetInfo(s)
			var qid int64
			if err := s.observer.QueryRowContext(s.p.Ctx, "SELECT queryid FROM pgss_part.pg_stat_statements ORDER BY queryid LIMIT 1").Scan(&qid); err != nil {
				s.t.Fatal(err)
			}
			pgssResetScalar(s, s.observer, "SELECT pgss_part.pg_stat_statements_reset(queryid=>$1::bigint)", false, qid)
			pgssResetEqual(s.t, pgssResetInfo(s), info)
			at := pgssResetScalar(s, s.observer, pgssResetCall, false)
			s.both(fixture.Select("SELECT dealloc,stats_reset=$1 AS same FROM pgss_part.pg_stat_statements_info", []string{"dealloc", "same"}, []string{"INT8", "BOOL"}, [][]any{{int64(0), true}}, at))
			pgssResetEqual(s.t, pgssResetSnapshot(s), [][]any(nil))
		})
	})
}
