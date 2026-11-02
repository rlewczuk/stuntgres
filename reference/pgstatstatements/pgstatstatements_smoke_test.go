package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgss_smoke_actor", "pgss_smoke_viewer"})
	pgssCase(t, true, func(s *pgssScenario) {
		s.exec("CREATE ROLE pgss_smoke_actor SUPERUSER")
		s.exec("CREATE ROLE pgss_smoke_viewer")
		s.exec("GRANT USAGE ON SCHEMA pgss_part TO PUBLIC")
		s.exec("CREATE TABLE pgss_part.pgss_smoke_data(k integer)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_smoke_data VALUES(1),(2)", 2))
		s.exec("CREATE TABLE pgss_part.pgss_smoke_target(k integer,v integer)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_smoke_target VALUES(99,99)", 1))
		// A unique effective principal prevents unrelated cluster history from
		// making the foreign-reader witness ambiguous after text redaction.
		s.exec("SET ROLE pgss_smoke_actor")
		defer s.observer.ExecContext(s.p.Ctx, "RESET ROLE")
		s.work(fixture.Exec("SET ROLE pgss_smoke_actor", 0))
		defer s.worker.ExecContext(s.p.Ctx, "RESET ROLE")
		pattern := "SELECT k AS pgss_smoke_measured FROM pgss_part.pgss_smoke_data WHERE k<=%"
		s.counts(pattern, 0, 0, 0)
		for _, c := range []struct {
			q      string
			values [][]any
		}{
			{"SELECT k AS pgss_smoke_measured FROM pgss_part.pgss_smoke_data WHERE k<=1 ORDER BY k", [][]any{{int64(1)}}},
			{"SELECT k AS pgss_smoke_measured FROM pgss_part.pgss_smoke_data WHERE k<=2 ORDER BY k", [][]any{{int64(1)}, {int64(2)}}},
		} {
			s.work(fixture.Select(c.q, []string{"pgss_smoke_measured"}, []string{"INT4"}, c.values))
		}
		s.counts(pattern, 1, 2, 3)
		s.entry("SELECT k AS pgss_smoke_measured FROM pgss_part.pgss_smoke_data WHERE k<=$1 ORDER BY k", 2, 3)
		s.both(fixture.Select("SELECT total_exec_time>=0 AS measured,min_exec_time>=0 AND min_exec_time<=mean_exec_time AND mean_exec_time<=max_exec_time AS extrema,abs(mean_exec_time-total_exec_time/calls)<0.000000001 AS mean,stats_since IS NOT NULL AND minmax_stats_since IS NOT NULL AS timestamps FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user)", []string{"measured", "extrema", "mean", "timestamps"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, pattern))
		heldSQL := "SELECT k+0 AS pgss_smoke_held FROM pgss_part.pgss_smoke_data WHERE k=$1::integer"
		held, err := s.worker.PrepareContext(s.p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		heldPattern := "SELECT k+% AS pgss_smoke_held FROM pgss_part.pgss_smoke_data%"
		s.counts(heldPattern, 0, 0, 0)
		for _, value := range []int64{1, 2} {
			fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.worker, Statement: held, SQL: heldSQL}, s.b.Recorder, fixture.Select(heldSQL, []string{"pgss_smoke_held"}, []string{"INT4"}, [][]any{{value}}, value))
		}
		s.counts(heldPattern, 1, 2, 2)
		failedSQL := "INSERT INTO pgss_part.pgss_smoke_target SELECT k,100/(k-$1::integer) FROM pgss_part.pgss_smoke_data ORDER BY k"
		s.counts("INSERT INTO pgss_part.pgss_smoke_target SELECT%", 0, 0, 0)
		failure := fixture.ImplicitError(failedSQL, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, int64(2))
		s.work(failure)
		s.counts("INSERT INTO pgss_part.pgss_smoke_target SELECT%", 0, 0, 0)
		s.both(fixture.Select("SELECT k,v FROM pgss_part.pgss_smoke_target", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(99)}}))
		s.both(fixture.Select("SELECT * FROM pgss_part.pg_stat_statements(NULL::boolean)", pgssNames, pgssTypes, nil))
		s.exec("RESET ROLE")
		s.exec("SET ROLE pgss_smoke_viewer")
		s.both(fixture.Select("SELECT queryid IS NULL AS hidden,query,calls,rows FROM pgss_part.pg_stat_statements WHERE userid=(SELECT oid FROM pg_roles WHERE rolname='pgss_smoke_actor') AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database()) ORDER BY rows", []string{"hidden", "query", "calls", "rows"}, []string{"BOOL", "TEXT", "INT8", "INT8"}, [][]any{{true, "<insufficient privilege>", int64(2), int64(2)}, {true, "<insufficient privilege>", int64(2), int64(3)}}))
		s.exec("RESET ROLE")
		s.work(fixture.Exec("RESET ROLE", 0))
		pgssProviderRelationships(s)
		pgssControlRoundtrip(s, "pgss_part.pgss_smoke_data", []int64{1, 2})
	})
}
