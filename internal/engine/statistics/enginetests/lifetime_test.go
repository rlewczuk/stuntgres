package statistics_test

import (
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/statistics"
)

func TestStatsProviderObjectLifetime(t *testing.T) {
	for _, category := range []statistics.Category{statistics.Relation, statistics.Function} {
		t.Run(map[statistics.Category]string{statistics.Relation: "relation", statistics.Function: "function"}[category], func(t *testing.T) {
			var p statistics.Provider
			var owner, late statistics.Writer
			key, other := statsKeys()
			key.Category, other.Category = category, category
			otherCategory := key
			otherCategory.Category = statistics.IO
			now := time.Now()
			owner.Add(&p, key, statistics.Fact{Inserts: 2, Calls: 3, TotalTime: time.Millisecond})
			owner.Add(&p, other, statistics.Fact{Inserts: 7, Calls: 11})
			owner.Add(&p, otherCategory, statistics.Fact{Operations: 13})
			owner.Flush(&p, now, true)
			var snapshot statistics.Reader
			snapshot.Configure("snapshot", 1)
			before := snapshot.Read(&p, key, now)
			if !reflect.ValueOf(&snapshot).Elem().FieldByName("cache").MapIndex(reflect.ValueOf(key)).IsValid() {
				t.Fatal("positive full snapshot did not capture the published key")
			}
			late.Add(&p, key, statistics.Fact{Inserts: 17, Calls: 19})
			late.Add(&p, other, statistics.Fact{Inserts: 1, Calls: 1})
			usage, ok := late.BeginFunction(&p, key)
			if !ok {
				t.Fatal("live identity rejected")
			}
			// Shared retirement also works without a physical writer/session.
			p.Retire(key)
			late.EndFunction(&p, usage, 5*time.Millisecond)
			late.Add(&p, key, statistics.Fact{Inserts: 23, Calls: 29})
			p.ReportAnalyze(key, statistics.Policy{Counts: true}, now, time.Millisecond, 31, 0, true)
			late.Flush(&p, now, true)
			if got := p.Read(key); got.Inserts != 0 || got.Calls != 0 || got.TotalTime != 0 || got.AnalyzeCount != 0 {
				t.Fatal("late collector resurrected retired identity", got)
			}
			if _, ok := late.BeginFunction(&p, key); ok {
				t.Fatal("retired identity admitted usage")
			}
			if got := p.Read(other); got.Inserts != 8 || got.Calls != 12 || got.Generation != before.Generation {
				t.Fatal("retirement changed unrelated key or generation", got)
			}
			if got := p.Read(otherCategory); got.Operations != 13 {
				t.Fatal("retirement was not category-qualified", got)
			}
			if got := snapshot.Read(&p, key, now); got != before || snapshot.Timestamp() != now {
				t.Fatal("retirement invalidated an existing snapshot", got)
			}
			snapshot.Clear()
			if got := snapshot.Read(&p, key, now); got.Inserts != 0 || got.Calls != 0 {
				t.Fatal("fresh snapshot copied orphan statistics", got)
			}
			// Inspect actual captured membership: a zero-valued orphan entry
			// would pass Read's value assertion but must fail this ownership proof.
			if reflect.ValueOf(&snapshot).Elem().FieldByName("cache").MapIndex(reflect.ValueOf(key)).IsValid() {
				t.Fatal("fresh full snapshot retained a zero-valued orphan entry")
			}
			p.Reset(now)
			late.Add(&p, key, statistics.Fact{Calls: 1})
			late.Flush(&p, now, true)
			if p.Read(key).Calls != 0 {
				t.Fatal("statistics-only reset erased the tombstone")
			}
			// Writer retirement removes only its own exact pending entry.
			var local statistics.Writer
			local.Add(&p, other, statistics.Fact{Calls: 1})
			local.Add(&p, otherCategory, statistics.Fact{Operations: 1})
			local.Retire(&p, other)
			local.Flush(&p, now, true)
			if p.Read(other).Calls != 0 || p.Read(otherCategory).Operations != 1 {
				t.Fatal("local retirement discarded unrelated pending ownership")
			}
			successor := key
			successor.Object.Epoch++
			stale, ok := late.BeginFunction(&p, successor)
			if !ok {
				t.Fatal("successor usage rejected")
			}
			p.Replace(now, nil)
			late.EndFunction(&p, stale, time.Second)
			u, ok := late.BeginFunction(&p, successor)
			if !ok {
				t.Fatal("new catalog incarnation inherited old tombstone")
			}
			late.EndFunction(&p, u, time.Millisecond)
			late.Flush(&p, now, true)
			if p.Read(key).Calls != 0 || p.Read(successor).Calls != 1 {
				t.Fatal("replacement lost generation-qualified lifetime")
			}
		})
	}
}
