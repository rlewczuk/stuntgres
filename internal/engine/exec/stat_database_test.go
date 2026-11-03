package exec

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/engine/storage"
	"testing"
)

func TestStatDatabaseTupleFrontiers(t *testing.T) {
	rows := make([]storage.RowVersion, 6)
	for i := range rows {
		rows[i] = storage.RowVersion{Row: ids.RowID(i + 1), Version: ids.VersionID(i + 1)}
	}
	physical := func(enabled bool) (*Source, Stage) {
		source := &Source{Committed: rows, statsInitialized: true, statsCounts: enabled}
		return source, Stage{Source: source, physicalScan: true}
	}
	t.Run("incremental_drain_rescan", func(t *testing.T) {
		source, stage := physical(true)
		ctx := context.Background()
		for range 2 {
			if _, present, err := stage.NextOccurrence(ctx); err != nil || !present {
				t.Fatal(present, err)
			}
		}
		if source.databaseStatistics.Returned != 2 {
			t.Fatal(source.databaseStatistics)
		}
		if err := stage.Drain(ctx); err != nil {
			t.Fatal(err)
		}
		if err := stage.Drain(ctx); err != nil {
			t.Fatal(err)
		}
		if source.databaseStatistics.Returned != 6 {
			t.Fatal("drain recounted demanded prefix", source.databaseStatistics)
		}
		// Replaying an already admitted occurrence is not a physical restart.
		for range 2 {
			if _, present, err := stage.NextOccurrence(ctx); err != nil || !present {
				t.Fatal(present, err)
			}
		}
		if source.databaseStatistics.Returned != 6 {
			t.Fatal("materialized occurrence replay", source.databaseStatistics)
		}
		if _, err := stage.Rewind(ctx); err != nil {
			t.Fatal(err)
		}
		if err := stage.Drain(ctx); err != nil {
			t.Fatal(err)
		}
		if source.databaseStatistics.Returned != 12 || source.databaseStatistics.Fetched != 0 {
			t.Fatal("real physical restart", source.databaseStatistics)
		}
	})
	t.Run("canceled_prefix", func(t *testing.T) {
		source, stage := physical(true)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		for range 3 {
			if _, present, err := stage.NextOccurrence(ctx); err != nil || !present {
				t.Fatal(present, err)
			}
		}
		cancel()
		if _, _, err := stage.NextOccurrence(ctx); err != context.Canceled {
			t.Fatal(err)
		}
		if source.databaseStatistics.Returned != 3 {
			t.Fatal("canceled executed prefix lost", source.databaseStatistics)
		}
	})
	t.Run("disabled_no_allocation", func(t *testing.T) {
		source, stage := physical(false)
		ctx := context.Background()
		allocations := testing.AllocsPerRun(100, func() {
			stage.Position, stage.statsFrontier = 0, 0
			for range 6 {
				_, _, _ = stage.NextOccurrence(ctx)
			}
		})
		if allocations != 0 || source.databaseStatistics != (statistics.DatabaseFact{}) {
			t.Fatal("disabled per-row instrumentation", allocations, source.databaseStatistics)
		}
	})
}
