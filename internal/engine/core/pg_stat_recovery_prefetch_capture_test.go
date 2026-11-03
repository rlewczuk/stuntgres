package core_test

import (
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func prefetchOwnerRow(t *testing.T, result core.Result, at time.Time, technical [9]int64) {
	t.Helper()
	if result.Rows == nil {
		t.Fatal("missing captured prefetch rows")
	}
	defer result.Rows.Close()
	if len(result.Rows.Schema()) != 10 {
		t.Fatal("ten captured descriptors", result.Rows.Schema())
	}
	values := make([]sqltype.Value, 10)
	present, err := result.Rows.Next(t.Context(), values)
	if err != nil || !present {
		t.Fatal("one captured row", present, err)
	}
	if values[0].Type().ID() != sqltype.Timestamptz {
		t.Fatal("reset descriptor", values[0].Type())
	}
	if at.IsZero() {
		if !values[0].IsNull() {
			t.Fatal("missing reset metadata was fabricated", values[0])
		}
	} else {
		stamp, err := values[0].TimestampTime()
		if err != nil || !stamp.Equal(at) {
			t.Fatal("exact captured reset metadata", stamp, at, err)
		}
	}
	for i, want := range technical {
		id := sqltype.Int8
		if i >= 6 {
			id = sqltype.Int4
		}
		got, err := values[i+1].Integer()
		if values[i+1].Type().ID() != id || err != nil || got != want {
			t.Fatal("exact typed technical projection", i, values[i+1], id, want, err)
		}
	}
	if present, err := result.Rows.Next(t.Context(), values); err != nil || present {
		t.Fatal("captured singleton cardinality", present, err)
	}
}

func TestPGStatRecoveryPrefetchCapture(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			activityOwnerRun(t, e, s, "SET stats_fetch_consistency="+mode, "BEGIN")
			defer activityOwnerRun(t, e, s, "ROLLBACK")
			ast, description := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_catalog.pg_stat_recovery_prefetch")
			t1, t2, t3 := time.Unix(100, 0).UTC(), time.Unix(200, 0).UTC(), time.Unix(300, 0).UTC()
			e.Statistics.ResetShared(statistics.RecoveryPrefetch, t1)
			var writer statistics.Writer
			writer.Shared(&e.Statistics, statistics.RecoveryPrefetch, statistics.SharedFact{Counters: [8]int64{11, 13, 17, 19, 23, 29}})
			writer.Flush(&e.Statistics, t1, true)
			e.Statistics.ReportPrefetchGauges(3, 5, 7)
			published := e.Statistics.ReadShared(statistics.RecoveryPrefetch)
			capture := func() core.Result {
				result, err := enginetest.PreparedExecute(e, s, ast, &description)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			old := capture()
			defer old.Rows.Close()
			prefetchOwnerRow(t, capture(), t1, [9]int64{11, 13, 17, 19, 23, 29, 3, 5, 7})
			if got := e.Statistics.ReadShared(statistics.RecoveryPrefetch); got != published {
				t.Fatal("projection changed provider", got, published)
			}
			e.Statistics.ReportPrefetchGauges(31, 37, 41)
			prefetchOwnerRow(t, old, t1, [9]int64{11, 13, 17, 19, 23, 29, 3, 5, 7})
			prefetchOwnerRow(t, capture(), t1, [9]int64{11, 13, 17, 19, 23, 29, 31, 37, 41})
			e.Statistics.ResetShared(statistics.RecoveryPrefetch, t2)
			prefetchOwnerRow(t, capture(), t2, [9]int64{0, 0, 0, 0, 0, 0, 31, 37, 41})
			// Unlike targeted reset, accepted replacement clears all nine values.
			retained := capture()
			defer retained.Rows.Close()
			e.Statistics.Replace(t3, nil)
			prefetchOwnerRow(t, retained, t2, [9]int64{0, 0, 0, 0, 0, 0, 31, 37, 41})
			prefetchOwnerRow(t, capture(), t3, [9]int64{})
			e.Statistics.Replace(time.Time{}, nil)
			prefetchOwnerRow(t, capture(), time.Time{}, [9]int64{})
			if got := e.Statistics.ReadShared(statistics.RecoveryPrefetch); got != (statistics.SharedFact{}) {
				t.Fatal("missing metadata read initialized provider", got)
			}
			e.Statistics.ReportPrefetchGauges(1<<31-1, 1<<31-1, 1<<31-1)
			prefetchOwnerRow(t, capture(), time.Time{}, [9]int64{0, 0, 0, 0, 0, 0, 1<<31 - 1, 1<<31 - 1, 1<<31 - 1})
			for _, gauges := range [][3]int64{{1 << 31, 0, 0}, {0, 1 << 31, 0}, {0, 0, 1 << 31}} {
				activityOwnerRun(t, e, s, "SAVEPOINT prefetch_overflow")
				e.Statistics.ReportPrefetchGauges(gauges[0], gauges[1], gauges[2])
				before := e.Statistics.ReadShared(statistics.RecoveryPrefetch)
				result, err := enginetest.PreparedExecute(e, s, ast, &description)
				if result.Rows != nil {
					result.Rows.Close()
					t.Fatal("out-of-range gauge exposed partial rows")
				}
				enginetest.State(t, err, "22003")
				activityOwnerRun(t, e, s, "ROLLBACK TO SAVEPOINT prefetch_overflow", "RELEASE SAVEPOINT prefetch_overflow")
				if got := e.Statistics.ReadShared(statistics.RecoveryPrefetch); got != before {
					t.Fatal("failed conversion changed provider", got, before)
				}
			}
		})
	}
}
