package core_test

import (
	"fmt"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func bgwriterCountRow(t *testing.T, result core.Result, want int64) {
	t.Helper()
	if result.Rows == nil {
		t.Fatal("missing aggregate result")
	}
	defer result.Rows.Close()
	columns := result.Rows.Schema()
	if len(columns) != 1 || columns[0].Name != "count" || columns[0].Usage.Type.ID() != sqltype.Int8 {
		t.Fatal("aggregate INT8 metadata", columns)
	}
	var cell [1]sqltype.Value
	if present, err := result.Rows.Next(t.Context(), cell[:]); err != nil || !present {
		t.Fatal("aggregate singleton", present, err)
	}
	if got, err := cell[0].Integer(); err != nil || got != want {
		t.Fatal("aggregate count", got, want, err)
	}
	if present, err := result.Rows.Next(t.Context(), cell[:]); err != nil || present {
		t.Fatal("aggregate extra row", present, err)
	}
}

func testBGWriterAggregateRejection(t *testing.T) {
	for _, mode := range []string{"cache", "snapshot"} {
		for _, retained := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/retained_%t/prepared_%t", mode, retained, prepared), func(t *testing.T) {
					e := engine.New()
					admin := enginetest.StartSession(t, e, nil)
					s := enginetest.StartSession(t, e, nil)
					statsFunctionExec(t, admin, "CREATE ROLE bgwriter_aggregate_reader")
					statsFunctionExec(t, s, "SET ROLE bgwriter_aggregate_reader", "SET stats_fetch_consistency="+mode, "BEGIN")
					defer statsFunctionExec(t, s, "ROLLBACK")
					var writer statistics.Writer
					writer.Shared(&e.Statistics, statistics.BGWriter, statistics.SharedFact{Counters: [8]int64{3, 5, 7}})
					writer.Shared(&e.Statistics, statistics.Archiver, statistics.SharedFact{Counters: [8]int64{13}})
					writer.Flush(&e.Statistics, time.Unix(100, 0), true)
					old := e.Statistics.ReadShared(statistics.BGWriter)
					if retained {
						statsFunctionExec(t, s, "SELECT buffers_clean FROM pg_stat_bgwriter")
					}
					resetStatsExec(t, admin, "SELECT pg_stat_reset_shared('bgwriter')")
					writer.Shared(&e.Statistics, statistics.BGWriter, statistics.SharedFact{Counters: [8]int64{8, 10, 14}})
					writer.Shared(&e.Statistics, statistics.Archiver, statistics.SharedFact{Counters: [8]int64{5}})
					writer.Flush(&e.Statistics, time.Unix(200, 0), true)
					before := bgwriterReaderState(s)
					const query = "SELECT count(buffers_clean) FROM pg_catalog.pg_stat_bgwriter WHERE FALSE"
					ast, description := enginetest.PreparedDescription(t, e, s, query)
					if bgwriterReaderState(s) != before {
						t.Fatal("aggregate Describe changed Reader")
					}
					result, err := bgwriterExecute(t, e, s, ast, &description, prepared)
					if err != nil {
						t.Fatal(err)
					}
					bgwriterCountRow(t, result, 0)
					if bgwriterReaderState(s) != before {
						t.Fatal("false aggregate initialized or replaced Reader")
					}
					deny := func() {
						t.Helper()
						statsFunctionExec(t, s, "SAVEPOINT aggregate_denied")
						prior := bgwriterReaderState(s)
						result, err := bgwriterExecute(t, e, s, ast, &description, prepared)
						if result.Rows != nil {
							result.Rows.Close()
							t.Fatal("denied aggregate published rows")
						}
						enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function pg_stat_get_bgwriter_buf_written_clean"})
						if bgwriterReaderState(s) != prior {
							t.Fatal("denied aggregate changed Reader before recovery")
						}
						statsFunctionExec(t, s, "ROLLBACK TO SAVEPOINT aggregate_denied", "RELEASE SAVEPOINT aggregate_denied")
						if bgwriterReaderState(s) != prior {
							t.Fatal("recovery cleared retained aggregate cut")
						}
					}
					statsFunctionExec(t, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_buf_written_clean() FROM PUBLIC")
					deny()
					statsFunctionExec(t, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_buf_written_clean() TO PUBLIC")
					trueAST, trueDescription := enginetest.PreparedDescription(t, e, s, "SELECT count(buffers_clean) FROM pg_catalog.pg_stat_bgwriter WHERE TRUE")
					result, err = bgwriterExecute(t, e, s, trueAST, &trueDescription, prepared)
					if err != nil {
						t.Fatal(err)
					}
					bgwriterCountRow(t, result, 1)
					if retained {
						if bgwriterReaderState(s) != before {
							t.Fatal("TRUE replaced positive retained cut")
						}
					} else if bgwriterReaderState(s) == before {
						t.Fatal("TRUE did not perform cumulative access")
					}
					starAST, starDescription := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_stat_bgwriter")
					result, err = bgwriterExecute(t, e, s, starAST, &starDescription, prepared)
					if err != nil {
						t.Fatal(err)
					}
					want := e.Statistics.ReadShared(statistics.BGWriter)
					if retained {
						want = old
					}
					bgwriterOwnerRow(t, result, want)
					statsFunctionExec(t, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_buf_written_clean() FROM PUBLIC")
					deny()
					before = bgwriterReaderState(s)
					presenceAST, presenceDescription := enginetest.PreparedDescription(t, e, s, "SELECT count(*) FROM pg_stat_bgwriter WHERE FALSE")
					result, err = bgwriterExecute(t, e, s, presenceAST, &presenceDescription, prepared)
					if err != nil {
						t.Fatal(err)
					}
					bgwriterCountRow(t, result, 0)
					if bgwriterReaderState(s) != before {
						t.Fatal("presence-only aggregate changed Reader")
					}
				})
			}
		}
	}
}
