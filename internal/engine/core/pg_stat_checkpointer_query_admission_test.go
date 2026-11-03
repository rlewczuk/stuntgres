package core_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Retain values and actual map/owner identities, not a reflect.Value alias to
// mutable Reader storage. Formatting the private value includes every family,
// key, stamp, reset and generation without exporting or changing that owner.
type checkpointerReaderOwnership struct {
	value string
	owner uintptr
	maps  [3]string
}

func checkpointerReaderState(s *session.Session) checkpointerReaderOwnership {
	reader := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
	state := checkpointerReaderOwnership{value: fmt.Sprintf("%#v", reader), owner: reader.Addr().Pointer()}
	for i, field := range [...]string{"cache", "activity", "backends"} {
		state.maps[i] = fmt.Sprintf("%p", reader.FieldByName(field).UnsafePointer())
	}
	return state
}

func checkpointerExecute(t *testing.T, e *core.Engine, s *session.Session, ast parser.Statement, description *core.Description, prepared bool) (core.Result, error) {
	t.Helper()
	if prepared {
		return enginetest.PreparedExecute(e, s, ast, description)
	}
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	return e.Execute(op, ast, 0, nil)
}

func checkpointerAdmissionRow(t *testing.T, result core.Result, first int64, reset time.Time) {
	t.Helper()
	if result.Rows == nil {
		t.Fatal("missing admitted two-occurrence row")
	}
	defer result.Rows.Close()
	columns := result.Rows.Schema()
	if len(columns) != 2 || columns[0].Usage.Type.ID() != sqltype.Int8 || columns[1].Usage.Type.ID() != sqltype.Timestamptz {
		t.Fatal("two-occurrence typed schema", columns)
	}
	var row [2]sqltype.Value
	if present, err := result.Rows.Next(t.Context(), row[:]); err != nil || !present {
		t.Fatal("two-occurrence singleton", present, err)
	}
	if got, err := row[0].Integer(); err != nil || got != first {
		t.Fatal("current/retained first occurrence", got, first, err)
	}
	if got, err := row[1].TimestampTime(); err != nil || !got.Equal(reset) {
		t.Fatal("current/retained reset occurrence", got, reset, err)
	}
	if present, err := result.Rows.Next(t.Context(), row[:]); err != nil || present {
		t.Fatal("extra two-occurrence row", present, err)
	}
}

func TestPGStatCheckpointerQueryAdmission(t *testing.T) {
	for _, mode := range []string{"cache", "snapshot"} {
		for _, retained := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct{ name, query, setup string }{
					{"two_checkpointer", "SELECT a.num_timed,b.stats_reset FROM pg_catalog.pg_stat_checkpointer a CROSS JOIN pg_catalog.pg_stat_checkpointer b", ""},
					{"earlier_archiver", "SELECT a.archived_count,b.stats_reset FROM pg_catalog.pg_stat_archiver a CROSS JOIN pg_catalog.pg_stat_checkpointer b", ""},
					{"nested_denied", "SELECT a.num_timed,b.stats_reset FROM pg_catalog.pg_stat_checkpointer a CROSS JOIN checkpointer_admission_nested b", "CREATE VIEW checkpointer_admission_nested AS SELECT stats_reset FROM pg_catalog.pg_stat_checkpointer"},
				} {
					t.Run(fmt.Sprintf("%s/retained_%t/prepared_%t/%s", mode, retained, prepared, tc.name), func(t *testing.T) {
						e := engine.New()
						admin := enginetest.StartSession(t, e, nil)
						s := enginetest.StartSession(t, e, nil)
						statsFunctionExec(t, admin, "CREATE ROLE checkpointer_query_reader", "CREATE TABLE checkpointer_admission_marker(v integer)")
						if tc.setup != "" {
							statsFunctionExec(t, admin, tc.setup, "GRANT SELECT ON checkpointer_admission_nested TO checkpointer_query_reader")
						}
						statsFunctionExec(t, s, "SET ROLE checkpointer_query_reader", "SET stats_fetch_consistency="+mode, "BEGIN")
						defer statsFunctionExec(t, s, "ROLLBACK")
						key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "checkpointer_admission_marker"), Category: statistics.Relation}
						var writer statistics.Writer
						writer.Shared(&e.Statistics, statistics.Checkpointer, statistics.SharedFact{Counters: [8]int64{3, 5, 7}})
						writer.Shared(&e.Statistics, statistics.Archiver, statistics.SharedFact{Counters: [8]int64{13}})
						writer.Add(&e.Statistics, key, statistics.Fact{Inserts: 11})
						writer.Flush(&e.Statistics, time.Unix(100, 0), true)
						if retained {
							statsFunctionExec(t, s, "SELECT num_timed FROM pg_stat_checkpointer")
						}
						beforeDescribe := checkpointerReaderState(s)
						ast, description := enginetest.PreparedDescription(t, e, s, tc.query)
						if checkpointerReaderState(s) != beforeDescribe {
							t.Fatal("Describe changed Reader ownership")
						}
						writer.Shared(&e.Statistics, statistics.Checkpointer, statistics.SharedFact{Counters: [8]int64{5, 5, 7}})
						writer.Shared(&e.Statistics, statistics.Archiver, statistics.SharedFact{Counters: [8]int64{5}})
						writer.Add(&e.Statistics, key, statistics.Fact{Inserts: 7})
						writer.Flush(&e.Statistics, time.Unix(200, 0), true)
						current := e.Statistics.ReadShared(statistics.Checkpointer)
						if current.Counters != ([8]int64{8, 10, 14}) || e.Statistics.Read(key).Inserts != 18 {
							t.Fatal("provider must diverge from retained cut", current)
						}
						deny := func() {
							t.Helper()
							statsFunctionExec(t, s, "SAVEPOINT checkpointer_query_denied")
							before := checkpointerReaderState(s)
							result, err := checkpointerExecute(t, e, s, ast, &description, prepared)
							if result.Rows != nil {
								result.Rows.Close()
								t.Fatal("later denied occurrence published rows")
							}
							enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function pg_stat_get_checkpointer_stat_reset_time"})
							if after := checkpointerReaderState(s); after != before {
								t.Fatal("denial changed actual Reader values/backing before recovery", before, after)
							}
							statsFunctionExec(t, s, "ROLLBACK TO SAVEPOINT checkpointer_query_denied", "RELEASE SAVEPOINT checkpointer_query_denied")
							if checkpointerReaderState(s) != before {
								t.Fatal("savepoint recovery hid a changed Reader")
							}
						}
						statsFunctionExec(t, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_checkpointer_stat_reset_time() FROM PUBLIC")
						deny()
						statsFunctionExec(t, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_checkpointer_stat_reset_time() TO PUBLIC")
						result, err := checkpointerExecute(t, e, s, ast, &description, prepared)
						if err != nil {
							t.Fatal("grant did not admit current query", err)
						}
						first := int64(8)
						if retained {
							first = 3
						}
						if tc.name == "earlier_archiver" {
							first = 18
							if retained && mode == "snapshot" {
								first = 13
							}
						}
						checkpointerAdmissionRow(t, result, first, current.ResetAt)
						statsFunctionExec(t, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_checkpointer_stat_reset_time() FROM PUBLIC")
						deny()
					})
				}
			}
		}
	}
}
