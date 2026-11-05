package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func TestPgStatStatementsCountersWorkers(t *testing.T) {
	for _, counts := range []string{"on","off"} {
		t.Run(counts,func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t,e,nil)
			for _, q := range []string{"SET pg_stat_statements.track='none'","CREATE TABLE counter_workers(k integer)","INSERT INTO counter_workers VALUES(1),(2),(3),(4),(5),(6)","SET max_parallel_workers_per_gather=2","SET parallel_setup_cost=0","SET parallel_tuple_cost=0","SET min_parallel_table_scan_size=0","SET track_counts="+counts,"SET pg_stat_statements.track_utility=off","SET pg_stat_statements.track='top'"} { pgssOwnerExec(t,e,s,q) }
			for _, tc := range []struct{ query string; workers int64; failure bool }{
				{"SELECT k FROM counter_workers AS worker_projection",2,false},
				{"SELECT string_agg(k::text,','::text) FROM counter_workers AS worker_aggregate",2,false},
				{"EXPLAIN SELECT k FROM counter_workers AS worker_explain",0,false},
				{"SELECT k/(k-k) FROM counter_workers AS worker_failure",2,true},
			} {
				func() {
				// Command admission captures the actual launch ceiling and
				// planner costs; initialization Acquire intentionally does not.
				op,_,err := s.AcquireCommand(t.Context())
				if err != nil { t.Fatal(err) }
				op.OriginalSQL,op.SQLAvailable = tc.query,true
				scope,err := e.StartStatement(op)
				if err != nil { op.Done(); t.Fatal(err) }
				defer op.Done()
				defer func() {
					if !scope.Finished {
						scope.Finish(errors.New("worker owner assertion interrupted command"))
					}
				}()
				result,err := scope.Execute(enginetest.Statement(t,tc.query),0,nil)
				if err == nil && result.Rows != nil {
					values := make([]sqltype.Value,len(result.Rows.Schema()))
					for {
						present,nextErr := result.Rows.Next(context.Background(),values)
						if nextErr != nil { err = nextErr; break }
						if !present { break }
					}
				}
				c := core.ScopeStatementCounters(scope)
				if tc.failure != (err != nil) || c.WorkersPlanned != tc.workers || c.WorkersLaunched != tc.workers {
					t.Fatal("actual coordinator launch/join facts",tc,c,err)
				}
				if !tc.failure {
					requests := c.SharedRead + c.SharedHit
					if requests != tc.workers {
						t.Fatal("genuine worker source Opens", tc, c)
					}
				}
				authority := core.ScopeAuthority(scope)
				if result.Rows != nil { closeErr := result.Rows.Close(); if err == nil { err = closeErr } }
				err = scope.Finish(err)
				if tc.failure != (err != nil) || authority != nil && authority.Host != nil { t.Fatal("joined worker resource escaped terminal",err) }
				if tc.failure {
					entry := pgssPhaseEntry(t,e,"worker_failure")
					if entry.Execution.Count != 0 || entry.Counters != (statistics.StatementCounters{}) { t.Fatal("failed joined workers published successful work",entry) }
				}
				}()
			}
		})
	}
}
