package core_test

import (
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func TestPGStatReplicationSlotsOrphanFacts(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			activityOwnerRun(t, e, s, "SET stats_fetch_consistency="+mode, "BEGIN")
			defer activityOwnerRun(t, e, s, "ROLLBACK")
			view := e.CatalogRead()
			ref := e.Catalog.Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatReplicationSlotsOID})
			orphan := statistics.Key{Object: ref, Category: statistics.ReplicationSlot}
			control := statistics.Key{Object: ref, Category: statistics.Subscription}
			reset := time.Unix(100, 123000).UTC()
			e.Statistics.Replace(reset, map[statistics.Key]statistics.Fact{
				orphan: {SpillTxns: 11, SpillCount: 13, SpillBytes: 17, StreamTxns: 19, StreamCount: 23, StreamBytes: 29, TotalTxns: 31, TotalBytes: 37, ResetAt: reset},
				control: {ApplyErrorCount: 41, SyncErrorCount: 43, ResetAt: reset},
			})
			before, unrelated := e.Statistics.Read(orphan), e.Statistics.Read(control)
			// Observe the actual transaction-owned reader, without introducing an
			// exported inspection API or a separate Reader as a substitute.
			reader := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
			unchanged := func() {
				t.Helper()
				if e.Statistics.Read(orphan) != before || e.Statistics.Read(control) != unrelated {
					t.Fatal("view read or rejected mutation changed provider facts")
				}
				if reader.FieldByName("cache").Len() != 0 || !reader.FieldByName("stamp").IsZero() || reader.FieldByName("full").Bool() {
					t.Fatal("absent inventory established a cumulative snapshot")
				}
			}
			ast, description := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_catalog.pg_stat_replication_slots")
			unchanged()
			empty := func(result core.Result) {
				t.Helper()
				if result.Rows == nil || len(result.Rows.Schema()) != 10 {
					t.Fatal("missing ten-column source", result.Rows)
				}
				defer result.Rows.Close()
				var values [10]sqltype.Value
				if present, err := result.Rows.Next(t.Context(), values[:]); present || err != nil {
					t.Fatal("provider-only fact became an inventory row", present, err)
				}
				unchanged()
			}
			for range 2 {
				result, err := enginetest.PreparedExecute(e, s, ast, &description)
				if err != nil {
					t.Fatal(err)
				}
				empty(result)
				op, err := s.Acquire(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				result, err = e.Execute(op, ast, 0, nil)
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
				empty(result)
			}
			for _, query := range []string{
				"INSERT INTO pg_stat_replication_slots(slot_name) VALUES('orphan')",
				"UPDATE pg_stat_replication_slots SET total_txns=0 WHERE FALSE",
				"DELETE FROM pg_stat_replication_slots WHERE FALSE",
			} {
				activityOwnerRun(t, e, s, "SAVEPOINT slots_failure")
				op, err := s.Acquire(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				result, err := e.Execute(op, enginetest.Statement(t, query), 0, nil)
				op.Done()
				if result.Rows != nil {
					result.Rows.Close()
					t.Fatal("rejected mutation published rows")
				}
				enginetest.State(t, err, "55000")
				unchanged()
				activityOwnerRun(t, e, s, "ROLLBACK TO SAVEPOINT slots_failure", "RELEASE SAVEPOINT slots_failure")
				result, err = enginetest.PreparedExecute(e, s, ast, &description)
				if err != nil {
					t.Fatal(err)
				}
				empty(result)
			}
		})
	}
}
