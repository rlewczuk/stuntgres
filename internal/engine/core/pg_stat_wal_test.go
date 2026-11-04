package core_test

import (
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func walOwnerRow(t *testing.T, result core.Result, fact statistics.SharedFact) {
	t.Helper()
	if result.Rows == nil {
		t.Fatal("missing captured WAL rows")
	}
	defer result.Rows.Close()
	columns := result.Rows.Schema()
	if len(columns) != 5 {
		t.Fatal("five captured descriptors", columns)
	}
	values := make([]sqltype.Value, 5)
	if present, err := result.Rows.Next(t.Context(), values); err != nil || !present {
		t.Fatal("captured singleton", present, err)
	}
	for i, cell := range values {
		if cell.Type() != columns[i].Usage.Type || cell.IsNull() {
			t.Fatal("owned non-NULL typed value", i, cell)
		}
	}
	for _, column := range [...]int{0, 1, 3} {
		if n, err := values[column].Integer(); err != nil || n != 0 {
			t.Fatal("physical absence formula", column, n, err)
		}
	}
	zero, err := sqltype.NumericUint64Value(columns[2].Usage.Type, 0)
	if err != nil || values[2] != zero {
		t.Fatal("exact typed NUMERIC zero", values[2], zero, err)
	}
	if got, err := values[4].TimestampTime(); err != nil || !got.Equal(fact.ResetAt) {
		t.Fatal("genuine reset instant", got, fact.ResetAt, err)
	}
	if present, err := result.Rows.Next(t.Context(), values); err != nil || present {
		t.Fatal("extra singleton row", present, err)
	}
}

func TestPGStatWALProviderAuthority(t *testing.T) {
	t.Run("routine", testWALRoutineMetadata)
	t.Run("admission", testWALReaderAuthority)
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s, "CREATE TABLE wal_provider_control(v integer)")
			resetStatsExec(t, s, "SELECT pg_stat_reset_shared('wal')")
			key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "wal_provider_control"), Category: statistics.WAL}
			control := key
			control.Category = statistics.IO
			before := e.Statistics.ReadShared(statistics.SharedWAL)
			if before.ResetAt.IsZero() || !before.ResetAt.Equal(e.Cluster.WALResetAt) {
				t.Fatal("positive genuine cluster reset authority", before)
			}
			var writer statistics.Writer
			writer.Add(&e.Statistics, key, statistics.Fact{Operations: 20, TotalTime: 30 * time.Millisecond})
			writer.Add(&e.Statistics, control, statistics.Fact{Operations: 7, TotalTime: 11 * time.Millisecond})
			writer.Shared(&e.Statistics, statistics.SharedWAL, statistics.SharedFact{Counters: [8]int64{3, 5, 7, 11}, TotalTime: time.Second, LastSuccess: "not a physical WAL event"})
			writer.Flush(&e.Statistics, time.Now(), true)
			if e.Statistics.ReadShared(statistics.SharedWAL) != before || e.Statistics.Read(key).Operations != 20 || e.Statistics.Read(key).TotalTime != 30*time.Millisecond {
				t.Fatal("keyed WAL operations are real, SharedWAL writer remains excluded")
			}
			controlBefore, generation := e.Statistics.Read(control), e.Statistics.Generation()
			statsFunctionExec(t, s, "SET stats_fetch_consistency="+mode, "BEGIN")
			defer statsFunctionExec(t, s, "ROLLBACK")
			owned := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
			ast, description := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_catalog.pg_stat_wal")
			if owned.FieldByName("full").Bool() || owned.FieldByName("kinds").Uint() != 0 || !owned.FieldByName("stamp").IsZero() {
				t.Fatal("Describe captured provider")
			}
			capture := func() core.Result {
				result, err := enginetest.PreparedExecute(e, s, ast, &description)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			old := capture()
			defer old.Rows.Close()
			walOwnerRow(t, capture(), before)
			resetStatsExec(t, s, "SELECT pg_stat_reset_shared('archiver')")
			if e.Statistics.ReadShared(statistics.SharedWAL) != before || e.Statistics.Read(key).Operations != 20 {
				t.Fatal("unrelated reset changed WAL authority")
			}
			resetStatsExec(t, s, "SELECT pg_stat_reset_shared('wal')")
			after := e.Statistics.ReadShared(statistics.SharedWAL)
			if !after.ResetAt.After(before.ResetAt) || after != (statistics.SharedFact{ResetAt: e.Cluster.WALResetAt}) || e.Statistics.Read(key).Operations != 0 || e.Statistics.Read(key).TotalTime != 0 || e.Statistics.Read(control) != controlBefore || e.Statistics.Generation() != generation {
				t.Fatal("selected reset must isolate keyed category and preserve generation", before, after)
			}
			walOwnerRow(t, old, before)
			want := before
			if mode == "none" {
				want = after
			}
			walOwnerRow(t, capture(), want)
			if mode != "none" && !owned.FieldByName("shared").Index(int(statistics.SharedWAL)).FieldByName("ResetAt").Equal(reflect.ValueOf(before.ResetAt)) {
				t.Fatal("reset changed private retained WAL reset backing")
			}
			resetStatsExec(t, s, "SELECT pg_stat_clear_snapshot()")
			walOwnerRow(t, capture(), after)
		})
	}
}
