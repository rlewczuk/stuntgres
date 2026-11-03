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

func bgwriterOwnerRow(t *testing.T, result core.Result, fact statistics.SharedFact) {
	t.Helper()
	if result.Rows == nil {
		t.Fatal("missing captured bgwriter rows")
	}
	defer result.Rows.Close()
	if len(result.Rows.Schema()) != 4 {
		t.Fatal("four captured descriptors", result.Rows.Schema())
	}
	values := make([]sqltype.Value, 4)
	if present, err := result.Rows.Next(t.Context(), values); err != nil || !present {
		t.Fatal("captured singleton", present, err)
	}
	for column := range 3 {
		if n, err := values[column].Integer(); err != nil || n != fact.Counters[column] {
			t.Fatal("counter mapping", column, n, err)
		}
	}
	if got, err := values[3].TimestampTime(); err != nil || !got.Equal(fact.ResetAt) {
		t.Fatal("genuine reset instant", got, fact.ResetAt, err)
	}
	if present, err := result.Rows.Next(t.Context(), values); err != nil || present {
		t.Fatal("extra singleton row", present, err)
	}
}

func testBGWriterCapture(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s, "SET stats_fetch_consistency="+mode, "SET TIME ZONE 'Asia/Tokyo'", "BEGIN")
			defer statsFunctionExec(t, s, "ROLLBACK")
			owned := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
			ast, description := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_catalog.pg_stat_bgwriter")
			if owned.FieldByName("full").Bool() || owned.FieldByName("kinds").Uint() != 0 || !owned.FieldByName("stamp").IsZero() {
				t.Fatal("Describe captured provider")
			}
			at := time.Unix(100, 123456000).UTC()
			e.Statistics.ResetShared(statistics.BGWriter, at)
			var writer statistics.Writer
			writer.Shared(&e.Statistics, statistics.BGWriter, statistics.SharedFact{Counters: [8]int64{3, 5, 7, 91, 92, 93}, LastSuccess: "success", LastSuccessAt: at.Add(time.Second), LastFailure: "failure", LastFailureAt: at.Add(2 * time.Second)})
			writer.Flush(&e.Statistics, at, true)
			before := e.Statistics.ReadShared(statistics.BGWriter)
			capture := func() core.Result {
				result, err := enginetest.PreparedExecute(e, s, ast, &description)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			old := capture()
			defer old.Rows.Close()
			bgwriterOwnerRow(t, capture(), before)
			if e.Statistics.ReadShared(statistics.BGWriter) != before {
				t.Fatal("projection changed provider")
			}
			resetStatsExec(t, s, "SELECT pg_stat_reset_shared('bgwriter')")
			after := e.Statistics.ReadShared(statistics.BGWriter)
			if after.Counters != [8]int64{} || after.LastSuccess != "" || after.LastFailure != "" || !after.LastSuccessAt.IsZero() || !after.LastFailureAt.IsZero() || after.ResetAt.IsZero() {
				t.Fatal("genuine seeded reset", after)
			}
			bgwriterOwnerRow(t, old, before)
			want := before
			if mode == "none" {
				want = after
			}
			bgwriterOwnerRow(t, capture(), want)
			if mode != "none" && owned.FieldByName("shared").Index(int(statistics.BGWriter)).FieldByName("Counters").Index(0).Int() != 3 {
				t.Fatal("reset changed private retained family backing")
			}
			resetStatsExec(t, s, "SELECT pg_stat_clear_snapshot()")
			bgwriterOwnerRow(t, capture(), after)
		})
	}
}
