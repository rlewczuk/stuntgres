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

func checkpointerCountRow(t *testing.T, result core.Result, want int64) {
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

func testCheckpointerAggregateRejection(t *testing.T) {
	for _, mode := range []string{"cache", "snapshot"} {
		for _, retained := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/retained_%t/prepared_%t", mode, retained, prepared), func(t *testing.T) {
					e := engine.New()
					admin := enginetest.StartSession(t, e, nil)
					s := enginetest.StartSession(t, e, nil)
					statsFunctionExec(t, admin, "CREATE ROLE checkpointer_aggregate_reader")
					statsFunctionExec(t, s, "SET ROLE checkpointer_aggregate_reader", "SET stats_fetch_consistency="+mode, "BEGIN")
					defer statsFunctionExec(t, s, "ROLLBACK")
					var writer statistics.Writer
					writer.Shared(&e.Statistics, statistics.Checkpointer, statistics.SharedFact{Counters: [8]int64{3, 5, 7}})
					writer.Shared(&e.Statistics, statistics.Archiver, statistics.SharedFact{Counters: [8]int64{13}})
					writer.Flush(&e.Statistics, time.Unix(100, 0), true)
					old := e.Statistics.ReadShared(statistics.Checkpointer)
					if retained {
						statsFunctionExec(t, s, "SELECT num_timed FROM pg_stat_checkpointer")
					}
					resetStatsExec(t, admin, "SELECT pg_stat_reset_shared('checkpointer')")
					writer.Shared(&e.Statistics, statistics.Checkpointer, statistics.SharedFact{Counters: [8]int64{8, 10, 14}})
					writer.Shared(&e.Statistics, statistics.Archiver, statistics.SharedFact{Counters: [8]int64{5}})
					writer.Flush(&e.Statistics, time.Unix(200, 0), true)
					before := checkpointerReaderState(s)
					const query = "SELECT count(num_timed) FROM pg_catalog.pg_stat_checkpointer WHERE FALSE"
					ast, description := enginetest.PreparedDescription(t, e, s, query)
					if checkpointerReaderState(s) != before {
						t.Fatal("aggregate Describe changed Reader")
					}
					result, err := checkpointerExecute(t, e, s, ast, &description, prepared)
					if err != nil {
						t.Fatal(err)
					}
					checkpointerCountRow(t, result, 0)
					if checkpointerReaderState(s) != before {
						t.Fatal("false aggregate initialized or replaced Reader")
					}
					deny := func() {
						t.Helper()
						statsFunctionExec(t, s, "SAVEPOINT aggregate_denied")
						prior := checkpointerReaderState(s)
						result, err := checkpointerExecute(t, e, s, ast, &description, prepared)
						if result.Rows != nil {
							result.Rows.Close()
							t.Fatal("denied aggregate published rows")
						}
						enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function pg_stat_get_checkpointer_num_timed"})
						if checkpointerReaderState(s) != prior {
							t.Fatal("denied aggregate changed Reader before recovery")
						}
						statsFunctionExec(t, s, "ROLLBACK TO SAVEPOINT aggregate_denied", "RELEASE SAVEPOINT aggregate_denied")
						if checkpointerReaderState(s) != prior {
							t.Fatal("recovery cleared retained aggregate cut")
						}
					}
					statsFunctionExec(t, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_checkpointer_num_timed() FROM PUBLIC")
					deny()
					statsFunctionExec(t, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_checkpointer_num_timed() TO PUBLIC")
					trueAST, trueDescription := enginetest.PreparedDescription(t, e, s, "SELECT count(num_timed) FROM pg_catalog.pg_stat_checkpointer WHERE TRUE")
					result, err = checkpointerExecute(t, e, s, trueAST, &trueDescription, prepared)
					if err != nil {
						t.Fatal(err)
					}
					checkpointerCountRow(t, result, 1)
					if retained {
						if checkpointerReaderState(s) != before {
							t.Fatal("TRUE replaced positive retained cut")
						}
					} else if checkpointerReaderState(s) == before {
						t.Fatal("TRUE did not perform cumulative access")
					}
					starAST, starDescription := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_stat_checkpointer")
					result, err = checkpointerExecute(t, e, s, starAST, &starDescription, prepared)
					if err != nil {
						t.Fatal(err)
					}
					want := e.Statistics.ReadShared(statistics.Checkpointer)
					if retained {
						want = old
					}
					checkpointerOwnerRow(t, result, want)
					statsFunctionExec(t, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_checkpointer_num_timed() FROM PUBLIC")
					deny()
					before = checkpointerReaderState(s)
					presenceAST, presenceDescription := enginetest.PreparedDescription(t, e, s, "SELECT count(*) FROM pg_stat_checkpointer WHERE FALSE")
					result, err = checkpointerExecute(t, e, s, presenceAST, &presenceDescription, prepared)
					if err != nil {
						t.Fatal(err)
					}
					checkpointerCountRow(t, result, 0)
					if checkpointerReaderState(s) != before {
						t.Fatal("presence-only aggregate changed Reader")
					}
				})
			}
		}
	}
}
