package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
)

func TestPlannerOtherStatisticsTarget(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE other_stats(n integer)", "CREATE TABLE other_stats_child() INHERITS(other_stats)", "INSERT INTO other_stats VALUES(1),(1),(1),(1),(2),(2),(2),(3),(3),(4)", "INSERT INTO other_stats_child VALUES(1),(1),(1),(1),(2),(2),(2),(3),(3),(4)", "SET default_statistics_target=1", "ANALYZE other_stats"} {
		plannerCostCommand(t, s, q)
	}
	ref, _ := vacuumStore(t, e, "other_stats")
	relation := e.CatalogRead().MustRelation(ref.Address)
	old := analyzeFacts(e, ref.Address)
	check := func(want int) {
		facts := analyzeFacts(e, ref.Address)
		for _, inherited := range []bool{false, true} {
			key := storage.StatisticsKey{Attribute: relation.Attributes[0], Inherited: inherited}
			fact := facts.Columns[key]
			rows := uint64(10)
			if inherited {
				rows = 20
			}
			if fact == nil || fact.Rows != rows || fact.DistinctCount != 4 || len(fact.MCV) != want {
				t.Fatal("ANALYZE ignored captured target/flavor", inherited, fact, want)
			}
			for i, mcv := range fact.MCV {
				n, _ := mcv.Value.Integer()
				count := uint64(4 - i)
				if inherited {
					count *= 2
				}
				if n != int64(i+1) || mcv.Count != count {
					t.Fatal(inherited, mcv)
				}
			}
		}
	}
	check(1)
	plannerCostCommand(t, s, "SET default_statistics_target=3")
	if analyzeFacts(e, ref.Address) != old {
		t.Fatal("SET rewrote collected facts")
	}
	// Capture target3, then mutate the live store to1 before executing ANALYZE.
	// The real publication must still consume the operation's detached target3.
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.Settings.Mu.Lock()
	v := s.Settings.TypedValue("default_statistics_target")
	v.Number = 1
	s.Settings.Typed["default_statistics_target"] = v
	s.Settings.Values["default_statistics_target"] = "1"
	s.Settings.Mu.Unlock()
	_, err = e.Execute(op, enginetest.Statement(t, "ANALYZE other_stats"), 0, nil)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	check(3)
	for _, fact := range old.Columns {
		if len(fact.MCV) != 1 {
			t.Fatal("new collection mutated retained old facts")
		}
	}
}
