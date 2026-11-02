package reference_test

import (
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsPlanning(t *testing.T) {
	for _, mode := range []string{"force_generic_plan", "force_custom_plan"} {
		t.Run(mode, func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				name := "pgss_part.pgss_plan_" + mode
				s.exec("CREATE TABLE " + name + "(k integer)")
				s.run(fixture.Exec("INSERT INTO "+name+" VALUES(1)", 1))
				s.work(fixture.Exec("SET plan_cache_mode = '"+mode+"'", 0))
				s.work(fixture.Exec("SET pg_stat_statements.track_planning = on", 0))
				q := "SELECT 10/(k-$1::integer) AS pgss_planned FROM " + name
				pattern := "SELECT % AS pgss_planned FROM " + name
				s.planCounts(pattern, 0, 0, 0, 0)
				held, err := s.worker.PrepareContext(s.p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				// Parse/Describe do not execute or build an executable plan.
				s.planCounts(pattern, 0, 0, 0, 0)
				for i := int64(1); i <= 2; i++ {
					fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.worker, Statement: held, SQL: q}, s.b.Recorder,
						fixture.Select(q, []string{"pgss_planned"}, []string{"INT4"}, [][]any{{int64(10)}}, int64(0)))
					plans := i
					if s.b.PG && mode == "force_generic_plan" {
						plans = 1
					}
					s.planCounts(pattern, 1, plans, i, i)
				}
				failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, int64(1))
				failure.SelectRows, failure.ErrorStage = true, "execute"
				fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.worker, Statement: held, SQL: q}, s.b.Recorder, failure)
				plans := int64(3)
				if s.b.PG && mode == "force_generic_plan" {
					plans = 1
				}
				s.planCounts(pattern, 1, plans, 2, 2)
			})
		})
	}
	t.Run("sql_prepare_and_failed_phases", func(t *testing.T) {
		pgssCase(t, true, func(s *pgssScenario) {
			s.exec("CREATE TABLE pgss_part.pgss_plan_sql(k integer)")
			s.run(fixture.Exec("INSERT INTO pgss_part.pgss_plan_sql VALUES(1)", 1))
			s.work(fixture.Exec("SET plan_cache_mode = force_generic_plan", 0))
			s.work(fixture.Exec("SET pg_stat_statements.track_planning = on", 0))
			q := "PREPARE pgss_plan_body(integer) AS SELECT k/$1 AS pgss_plan_sql_value FROM pgss_part.pgss_plan_sql"
			s.work(fixture.Exec(q, 0))
			pattern := "PREPARE pgss_plan_body%"
			s.planCounts(pattern, 0, 0, 0, 0)
			failed := fixture.ImplicitError("EXECUTE pgss_plan_body(0)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			failed.SelectRows = true
			s.work(failed)
			s.planCounts(pattern, 1, 1, 0, 0)
			for i := int64(1); i <= 2; i++ {
				s.work(fixture.Select("EXECUTE pgss_plan_body(1)", []string{"pgss_plan_sql_value"}, []string{"INT4"}, [][]any{{int64(1)}}))
				plans := int64(1)
				if !s.b.PG {
					plans = i + 1
				}
				s.planCounts(pattern, 1, plans, i, i)
			}
			s.work(fixture.Exec("DEALLOCATE pgss_plan_body", 0))
			s.counts("EXECUTE pgss_plan_body%", 0, 0, 0)
			// PostgreSQL plans constant arithmetic here. Ordinary native SELECT
			// retains its existing row-owned demand, after a real accepted build.
			q = "SELECT 1/0 AS pgss_failed_constant_plan"
			s.counts("SELECT % AS pgss_failed_constant_plan", 0, 0, 0)
			failed = fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			failed.SelectRows = true
			s.work(failed)
			if s.b.PG {
				s.counts("SELECT % AS pgss_failed_constant_plan", 0, 0, 0)
			} else {
				s.planCounts("SELECT % AS pgss_failed_constant_plan", 1, 1, 0, 0)
			}
		})
	})
}

// Plans and calls are independent successful phases. Timing assertions are
// numerical properties of real samples, not backend-equal elapsed literals.
func (s *pgssScenario) planCounts(pattern string, groups, plans, calls, rows int64) {
	s.t.Helper()
	q := "SELECT count(*) AS groups,COALESCE(sum(plans),0)::bigint AS plans,COALESCE(sum(calls),0)::bigint AS calls,COALESCE(sum(rows),0)::bigint AS rows FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
	s.both(fixture.Select(q, []string{"groups", "plans", "calls", "rows"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{groups, plans, calls, rows}}, pattern))
	if plans == 0 {
		return
	}
	q = "SELECT plans,total_plan_time,min_plan_time,max_plan_time,mean_plan_time,stddev_plan_time FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
	result, err := s.observer.QueryContext(s.p.Ctx, q, pattern)
	if err != nil {
		s.t.Fatal(err)
	}
	defer result.Close()
	if !result.Next() {
		s.t.Fatalf("missing planning sample: %v", result.Err())
	}
	var n int64
	var sample [5]float64
	if err := result.Scan(&n, &sample[0], &sample[1], &sample[2], &sample[3], &sample[4]); err != nil {
		s.t.Fatal(err)
	}
	if n != plans {
		s.t.Fatalf("plans=%d want=%d", n, plans)
	}
	for _, v := range sample {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			s.t.Fatalf("nonfinite/negative planning timing %v", sample)
		}
	}
	if !pgssTimeClose(sample[3], sample[0]/float64(n)) || sample[1] > sample[3] || sample[3] > sample[2] {
		s.t.Fatalf("incoherent planning timing %v plans=%d", sample, n)
	}
	if n == 1 && (sample[0] != sample[1] || sample[0] != sample[2] || sample[0] != sample[3] || sample[4] != 0) {
		s.t.Fatalf("single planning sample %v", sample)
	}
	if result.Next() {
		s.t.Fatal("multiple planning entries")
	}
	if err := result.Err(); err != nil {
		s.t.Fatal(err)
	}
}
