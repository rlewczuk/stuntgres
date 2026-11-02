package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsTiming(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%v", prepared), func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				name := "pgss_part.pgss_timing_direct"
				if prepared {
					name = "pgss_part.pgss_timing_prepared"
				}
				s.exec("CREATE TABLE " + name + "(k integer)")
				s.run(fixture.Exec("INSERT INTO "+name+" VALUES(1),(2)", 2))
				q := "SELECT k AS pgss_timed FROM " + name + " WHERE k=$1::integer"
				s.counts(q, 0, 0, 0)
				held, err := s.worker.PrepareContext(s.p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				s.counts(q, 0, 0, 0)
				var previous float64
				for call, value := range []int64{1, 2, 1} {
					step := fixture.Select(q, []string{"pgss_timed"}, []string{"INT4"}, [][]any{{value}}, value)
					if prepared {
						fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.worker, Statement: held, SQL: q}, s.b.Recorder, step)
					} else {
						s.work(step)
					}
					calls := int64(call + 1)
					s.counts(q, 1, calls, calls)
					// Both direct and freshly prepared observers read the same real
					// accumulator without contributing any measured work themselves.
					current := s.executionTiming(q, calls, false)
					if again := s.executionTiming(q, calls, true); again != current {
						t.Fatalf("timing changed without execution: direct=%v prepared=%v", current, again)
					}
					if current[0] < previous {
						t.Fatalf("total execution time decreased: before=%g after=%g", previous, current[0])
					}
					if calls == 2 {
						// With two samples population stddev is half their distance.
						second := current[0] - previous
						want := math.Abs(previous-second) / 2
						if !pgssTimeClose(current[4], want) {
							t.Fatalf("two-sample population stddev=%g want=%g", current[4], want)
						}
					}
					previous = current[0]
				}
			})
		})
	}
}

// Returns total/min/max/mean/population-stddev, in actual milliseconds.
func (s *pgssScenario) executionTiming(query string, calls int64, prepared bool) [5]float64 {
	s.t.Helper()
	q := "SELECT calls,rows,total_exec_time,min_exec_time,max_exec_time,mean_exec_time,stddev_exec_time,plans,total_plan_time,min_plan_time,max_plan_time,mean_plan_time,stddev_plan_time FROM pgss_part.pg_stat_statements WHERE query=$1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())"
	var timing [5]float64
	step := fixture.Select(q, []string{"calls", "rows", "total_exec_time", "min_exec_time", "max_exec_time", "mean_exec_time", "stddev_exec_time", "plans", "total_plan_time", "min_plan_time", "max_plan_time", "mean_plan_time", "stddev_plan_time"}, []string{"INT8", "INT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "INT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}, nil, query)
	// Durations are measured, not portable expected literals. Read them through
	// the public driver and assert independently specified numerical properties.
	var result *sql.Rows
	var err error
	if prepared {
		stmt, prepareErr := s.observer.PrepareContext(s.p.Ctx, q)
		if prepareErr != nil {
			s.t.Fatal(prepareErr)
		}
		defer stmt.Close()
		result, err = stmt.QueryContext(s.p.Ctx, query)
	} else {
		result, err = s.observer.QueryContext(s.p.Ctx, q, query)
	}
	if err != nil {
		s.t.Fatal(err)
	}
	defer result.Close()
	columns, err := result.ColumnTypes()
	if err != nil {
		s.t.Fatal(err)
	}
	if len(columns) != len(step.Columns) {
		s.t.Fatalf("timing columns=%d want=%d", len(columns), len(step.Columns))
	}
	for i, column := range columns {
		if column.Name() != step.Columns[i] || column.DatabaseTypeName() != step.Types[i] {
			s.t.Fatalf("timing column %d=(%s,%s) want=(%s,%s)", i, column.Name(), column.DatabaseTypeName(), step.Columns[i], step.Types[i])
		}
	}
	if !result.Next() {
		s.t.Fatalf("missing timing entry %q: %v", query, result.Err())
	}
	var gotCalls, rows, plans int64
	var planning [5]float64
	if err := result.Scan(&gotCalls, &rows, &timing[0], &timing[1], &timing[2], &timing[3], &timing[4], &plans, &planning[0], &planning[1], &planning[2], &planning[3], &planning[4]); err != nil {
		s.t.Fatal(err)
	}
	if gotCalls != calls || rows != calls {
		s.t.Fatalf("timing calls/rows=(%d,%d) want=(%d,%d)", gotCalls, rows, calls, calls)
	}
	if plans != 0 || planning != [5]float64{} {
		s.t.Fatalf("disabled planning recorded plans=%d timings=%v", plans, planning)
	}
	for i, value := range timing {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			s.t.Fatalf("execution timing[%d]=%g, want finite nonnegative milliseconds", i, value)
		}
	}
	if !pgssTimeClose(timing[3], timing[0]/float64(calls)) || timing[1] > timing[3] || timing[3] > timing[2] {
		s.t.Fatalf("incoherent execution timing total/min/max/mean/stddev=%v calls=%d", timing, calls)
	}
	if calls == 1 && (timing[0] != timing[1] || timing[0] != timing[2] || timing[0] != timing[3] || timing[4] != 0) {
		s.t.Fatalf("single-sample timing=%v, want total=min=max=mean and stddev=0", timing)
	}
	if result.Next() {
		s.t.Fatalf("multiple timing entries for %q", query)
	}
	if err := result.Err(); err != nil {
		s.t.Fatal(err)
	}
	return timing
}

func pgssTimeClose(got, want float64) bool {
	return math.Abs(got-want) <= 1e-12*math.Max(1, math.Max(math.Abs(got), math.Abs(want)))
}
