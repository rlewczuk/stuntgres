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

func archiverOwnerRow(t *testing.T, result core.Result, fact statistics.SharedFact) {
	t.Helper()
	if result.Rows == nil {
		t.Fatal("missing captured archiver rows")
	}
	defer result.Rows.Close()
	if len(result.Rows.Schema()) != 7 {
		t.Fatal("seven captured descriptors", result.Rows.Schema())
	}
	values := make([]sqltype.Value, 7)
	if present, err := result.Rows.Next(t.Context(), values); err != nil || !present {
		t.Fatal("captured singleton", present, err)
	}
	for slot, column := range [...]int{0, 3} {
		if n, err := values[column].Integer(); err != nil || n != fact.Counters[slot] {
			t.Fatal("counter mapping", column, n, err)
		}
	}
	for i, name := range [...]string{fact.LastSuccess, fact.LastFailure} {
		cell := values[1+i*3]
		if cell.IsNull() != (name == "") {
			t.Fatal("filename nullness", cell, name)
		}
		if name != "" {
			if got, err := cell.Text(); err != nil || got != name {
				t.Fatal("filename", got, name, err)
			}
		}
	}
	for i, at := range [...]time.Time{fact.LastSuccessAt, fact.LastFailureAt, fact.ResetAt} {
		cell := values[[...]int{2, 5, 6}[i]]
		if cell.IsNull() != at.IsZero() {
			t.Fatal("timestamp nullness", cell, at)
		}
		if !at.IsZero() {
			if got, err := cell.TimestampTime(); err != nil || !got.Equal(at) {
				t.Fatal("event instant", got, at, err)
			}
		}
	}
	if present, err := result.Rows.Next(t.Context(), values); err != nil || present {
		t.Fatal("extra singleton row", present, err)
	}
}

func TestPGStatArchiverCapture(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s, "SET stats_fetch_consistency="+mode, "SET TIME ZONE 'Asia/Tokyo'", "BEGIN")
			defer statsFunctionExec(t, s, "ROLLBACK")
			owned := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
			ast, description := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_catalog.pg_stat_archiver")
			if owned.FieldByName("full").Bool() || owned.FieldByName("kinds").Uint() != 0 || !owned.FieldByName("stamp").IsZero() {
				t.Fatal("Describe captured provider")
			}
			at := time.Unix(100, 123456000).UTC()
			e.Statistics.ResetShared(statistics.Archiver, at)
			var writer statistics.Writer
			writer.Shared(&e.Statistics, statistics.Archiver, statistics.SharedFact{Counters: [8]int64{3, 2, 99}, LastSuccess: "success", LastSuccessAt: at.Add(time.Second), LastFailure: "failure", LastFailureAt: at.Add(2 * time.Second)})
			writer.Flush(&e.Statistics, at, true)
			before := e.Statistics.ReadShared(statistics.Archiver)
			capture := func() core.Result {
				result, err := enginetest.PreparedExecute(e, s, ast, &description)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			old := capture()
			defer old.Rows.Close()
			archiverOwnerRow(t, capture(), before)
			if e.Statistics.ReadShared(statistics.Archiver) != before {
				t.Fatal("projection changed provider")
			}
			resetStatsExec(t, s, "SELECT pg_stat_reset_shared('archiver')")
			after := e.Statistics.ReadShared(statistics.Archiver)
			if after.Counters != [8]int64{} || after.LastSuccess != "" || after.LastFailure != "" || !after.LastSuccessAt.IsZero() || !after.LastFailureAt.IsZero() || after.ResetAt.IsZero() {
				t.Fatal("genuine seeded reset", after)
			}
			archiverOwnerRow(t, old, before)
			want := before
			if mode == "none" {
				want = after
			}
			archiverOwnerRow(t, capture(), want)
			if mode != "none" && owned.FieldByName("shared").Index(int(statistics.Archiver)).FieldByName("Counters").Index(0).Int() != 3 {
				t.Fatal("reset changed private retained family backing")
			}
			resetStatsExec(t, s, "SELECT pg_stat_clear_snapshot()")
			archiverOwnerRow(t, capture(), after)
		})
	}
}

func TestPGStatArchiverReaderOwnership(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, first := range []string{"archiver", "relation"} {
			t.Run(mode+"/"+first, func(t *testing.T) {
				e, s, peer, ps, key, _ := sharedStatsFixture(t)
				key.Category = statistics.Relation
				statsFunctionExec(t, s, "SET stats_fetch_consistency="+mode, "BEGIN")
				defer statsFunctionExec(t, s, "ROLLBACK")
				var writer statistics.Writer
				writer.Shared(&e.Statistics, statistics.Archiver, statistics.SharedFact{Counters: [8]int64{3, 2}})
				writer.Add(&e.Statistics, key, statistics.Fact{Inserts: 9})
				writer.Flush(&e.Statistics, time.Unix(100, 0), true)
				ast, d := enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_catalog.pg_stat_archiver")
				capture := func() core.Result {
					result, err := enginetest.PreparedExecute(e, s, ast, &d)
					if err != nil {
						t.Fatal(err)
					}
					return result
				}
				before := e.Statistics.ReadShared(statistics.Archiver)
				if first == "relation" {
					sharedStatsCapture(t, e, s, key.Object)
				} else {
					archiverOwnerRow(t, capture(), before)
				}
				owned := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
				if owned.FieldByName("full").Bool() != (mode == "snapshot") {
					t.Fatal("all-family ownership", mode)
				}
				if mode == "snapshot" {
					cached := owned.FieldByName("cache").MapIndex(reflect.ValueOf(key))
					if !cached.IsValid() || !cached.FieldByName("exists").Bool() || cached.FieldByName("fact").FieldByName("Inserts").Int() != 9 || owned.FieldByName("shared").Index(int(statistics.Archiver)).FieldByName("Counters").Index(0).Int() != 3 || owned.FieldByName("stamp").IsZero() {
						t.Fatal("coherent local all-family cut")
					}
				} else if mode == "cache" {
					if (owned.FieldByName("kinds").Uint() != 0) != (first == "archiver") || owned.FieldByName("cache").Len() != map[string]int{"relation": 1, "archiver": 0}[first] {
						t.Fatal("cache captured unrequested family")
					}
				}
				// Peer reset traverses mirrors without changing this owner's cut.
				resetStatsExec(t, ps, "SELECT pg_stat_reset_shared('archiver')")
				writer.Add(&e.Statistics, key, statistics.Fact{Inserts: 4})
				writer.Flush(&e.Statistics, time.Unix(200, 0), true)
				after := e.Statistics.ReadShared(statistics.Archiver)
				want := after
				if mode == "snapshot" || mode == "cache" && first == "archiver" {
					want = before
				}
				archiverOwnerRow(t, capture(), want)
				sharedStatsCapture(t, e, s, key.Object)
				if mode != "none" {
					n := int64(13)
					if mode == "snapshot" || first == "relation" {
						n = 9
					}
					cached := owned.FieldByName("cache").MapIndex(reflect.ValueOf(key))
					if !cached.IsValid() || !cached.FieldByName("exists").Bool() || cached.FieldByName("fact").FieldByName("Inserts").Int() != n {
						t.Fatal("relation half of coherent cut", cached, n)
					}
				}
				if peer.Statistics.ReadShared(statistics.Archiver).ResetAt != after.ResetAt {
					t.Fatal("peer reset mirror")
				}
				resetStatsExec(t, s, "SELECT pg_stat_clear_snapshot()")
				if owned.FieldByName("full").Bool() || owned.FieldByName("kinds").Uint() != 0 || owned.FieldByName("cache").Len() != 0 {
					t.Fatal("clear retained owner backing")
				}
				archiverOwnerRow(t, capture(), after)
			})
		}
	}
}
