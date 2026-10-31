package statistics_test

import (
	"stuntgres/internal/engine/statistics"
	"testing"
	"time"
)

func TestStatsSnapshotTransactionAccounting(t *testing.T) {
	key := statistics.Key{}
	event := func(i, u, d int64, truncated bool) statistics.Fact {
		return statistics.Fact{Inserts: i, Updates: u, Deletes: d, Live: i - d, Dead: u + d, Modifications: i + u + d, InsertsSince: i, Truncated: truncated}
	}
	vector := func(f statistics.Fact) [7]int64 {
		return [7]int64{f.Inserts, f.Updates, f.Deletes, f.Live, f.Dead, f.Modifications, f.InsertsSince}
	}
	want := func(t *testing.T, f statistics.Fact, expected [7]int64) {
		t.Helper()
		if got := vector(f); got != expected {
			t.Fatalf("got %v want %v", got, expected)
		}
	}
	t.Run("same_level_first_truncate_and_cow", func(t *testing.T) {
		var ledger, stage statistics.Ledger
		ledger.Record(key, event(2, 1, 1, false), 0)
		saved := ledger.Clone()
		stage.Record(key, event(3, 0, 0, false), 0)
		stage.Record(key, event(0, 0, 0, true), 0)
		stage.Record(key, event(1, 0, 0, false), 0)
		ledger.Take(&stage)
		if stage != nil || ledger[key].BeforeTruncate != (statistics.TupleCounts{Inserts: 5, Updates: 1, Deletes: 1}) || saved[key].Truncated || saved[key].Inserts != 2 {
			t.Fatal("same-level prefix or immutable mark lost", ledger, saved)
		}
		ledger.Record(key, event(0, 0, 0, true), 0)
		ledger.Record(key, event(3, 2, 1, false), 0)
		want(t, ledger[key].Committed(), [7]int64{3, 2, 1, 2, 3, 6, 3})
		want(t, ledger[key].AbortedFrom(0), [7]int64{5, 1, 1, 0, 6, 0, 5})
	})
	t.Run("child_abort_is_not_flat_subtraction", func(t *testing.T) {
		var ledger statistics.Ledger
		ledger.Record(key, event(2, 0, 0, false), 0)
		mark := ledger.Clone()
		ledger.Record(key, event(3, 1, 1, false), 1)
		ledger.Record(key, event(0, 0, 0, true), 1)
		ledger.Record(key, event(1, 0, 0, false), 1)
		want(t, ledger[key].AbortedFrom(1), [7]int64{3, 1, 1, 0, 4, 0, 3})
		if mark[key].Inserts != 2 || mark[key].Truncated {
			t.Fatal("child mutated saved parent")
		}
		ledger = mark.Clone()
		ledger.Record(key, event(1, 0, 0, false), 1)
		ledger.Record(key, event(0, 0, 0, true), 1)
		ledger.Record(key, event(4, 0, 0, false), 1)
		want(t, ledger[key].AbortedFrom(1), [7]int64{1, 0, 0, 0, 1, 0, 1})
		ledger = mark.Clone()
		want(t, ledger[key].Committed(), [7]int64{2, 0, 0, 2, 0, 2, 2})
	})
	for _, parent := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing_parent_promotion", true: "adjacent_parent_preservation"}[parent], func(t *testing.T) {
			var ledger statistics.Ledger
			if parent {
				ledger.Record(key, event(2, 0, 0, false), 0)
			}
			ledger.Record(key, event(3, 0, 0, false), 2)
			ledger.Record(key, event(0, 0, 0, true), 2)
			ledger.Record(key, event(1, 0, 0, false), 2)
			ledger.Release(1)
			if ledger[key].Level != 1 || ledger[key].BeforeTruncate.Inserts != 3 {
				t.Fatal("missing immediate parent did not promote intact", ledger)
			}
			ledger.Release(0)
			before := int64(3)
			if parent {
				before = 2
			}
			if ledger[key].Level != 0 || ledger[key].BeforeTruncate.Inserts != before {
				t.Fatal("successful child used wrong first-pre state", ledger)
			}
			want(t, ledger[key].Committed(), [7]int64{1, 0, 0, 1, 0, 1, 1})
			want(t, ledger[key].AbortedFrom(0), [7]int64{before, 0, 0, 0, before, 0, before})
		})
	}
	t.Run("unreleased_abort_and_prepared_collapse_differ", func(t *testing.T) {
		var ledger statistics.Ledger
		ledger.Record(key, event(2, 0, 0, false), 0)
		ledger.Record(key, event(3, 0, 0, false), 1)
		ledger.Record(key, event(0, 0, 0, true), 1)
		ledger.Record(key, event(1, 0, 0, false), 1)
		want(t, ledger[key].AbortedFrom(0), [7]int64{5, 0, 0, 0, 5, 0, 5})
		prepared := ledger.Clone()
		prepared.Release(0)
		want(t, prepared[key].AbortedFrom(0), [7]int64{2, 0, 0, 0, 2, 0, 2})
		if ledger[key].Level != 1 || ledger[key].BeforeTruncate.Inserts != 3 {
			t.Fatal("PREPARE mutated retained child root")
		}
	})
	for _, published := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending_prefix_before_truncate", true: "published_prefix_before_truncate"}[published], func(t *testing.T) {
			var provider statistics.Provider
			var writer statistics.Writer
			writer.Add(&provider, key, event(4, 0, 0, false))
			if published {
				writer.Flush(&provider, time.Unix(10, 0), true)
			}
			var ledger statistics.Ledger
			ledger.Record(key, event(2, 0, 0, false), 0)
			ledger.Record(key, event(0, 0, 0, true), 0)
			ledger.Record(key, event(1, 0, 0, false), 0)
			writer.Add(&provider, key, ledger[key].Committed())
			writer.Flush(&provider, time.Unix(11, 0), true)
			since := int64(5)
			if published {
				since = 1
			}
			want(t, provider.Read(key), [7]int64{5, 0, 0, 1, 0, 5, since})
		})
	}
}
