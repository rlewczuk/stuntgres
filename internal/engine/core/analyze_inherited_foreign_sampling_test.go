package core_test

import (
	"context"
	"math"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/storage"
)

func TestAnalyzeInheritedForeignAllocation(t *testing.T) {
	for _, partition := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "ordinary", true: "partition"}[partition]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				f := newInheritedForeignFixture(t, 1000, partition)
				rows, versions := f.e.Storage.RowFrontier(), f.e.Storage.VersionFrontier()
				f.analyze(t, prepared, "")
				source := f.wrapper.sources["mixed_foreign"]
				fact, b := f.inherited(t, 0), f.inherited(t, 1)
				metadata := f.e.Storage.RelationMetadata[f.parent.Address]
				if len(source.targets) != 1 || source.targets[0] != 225 || fact.Rows != 300 || fact.DistinctCount != 2 || mixedMCVCount(t, fact, 2) != 225 || b.Rows != 300 || mixedMCVCount(t, b, 20) != 225 || metadata.InheritedTuples != 4000.5 || metadata.AnalyzeCount != 1 || metadata.LastAnalyze.IsZero() {
					t.Fatal("weights8/24 did not produce genuine75/225 sample and fractional population", source.targets, fact, b, metadata)
				}
				if f.e.Storage.RelationMetadata[f.foreign.Address] != nil || f.e.Storage.Relations[f.foreign.Address] != nil || f.e.Storage.RowFrontier() != rows || f.e.Storage.VersionFrontier() != versions {
					t.Fatal("ONLY sampling published child facts, manufactured a heap or allocated tuple identities")
				}
				if partition {
					if f.e.Storage.Relations[f.parent.Address] != nil || metadata.Scanned || metadata.Tuples != 0 {
						t.Fatal("partition inherited estimate became local physical authority")
					}
					enginetest.TransactionRows(t, f.s, "SELECT (reltuples=4000.5)::integer FROM pg_class WHERE oid='mixed_parent'::regclass", 1)
				} else if local := f.e.Storage.Relations[f.parent.Address].Maintenance; !local.Scanned || local.Tuples != 1000 {
					t.Fatal("inherited estimate replaced exact local capture", local)
				}
				for _, request := range f.wrapper.negotiated {
					if request.MappingOID != 0 || request.Transaction != nil || len(request.MappingOptions) != 0 {
						t.Fatal("negotiation required acquisition authority", request)
					}
				}
				if len(source.requests) != 1 || source.requests[0].MappingOID == 0 || f.wrapper.active.Load() != 0 {
					t.Fatal("acquisition mapping or resource lifetime lost")
				}
				old := f.retained(t)
				oldB := b
				for _, row := range source.sample.Rows {
					row[0], row[1] = int64(30), int64(3)
				}
				f.analyze(t, prepared, "(a)")
				if f.inherited(t, 1) != oldB || mixedMCVCount(t, f.inherited(t, 0), 3) != 225 {
					t.Fatal("selected refresh copied b or failed to import actual changed a")
				}
				old()
				enginetest.LockEmpty(t, f.e)
			})
		}
	}
}

func TestAnalyzeInheritedForeignZeroWork(t *testing.T) {
	for _, test := range []struct {
		name       string
		native     int
		pages      float64
		sample     int
		population float64
		quota      int
		rows       uint64
		total      float64
	}{
		{"zero_pages", 1000, 0, 3000, 3000, 0, 300, 1000},
		{"rounded_zero", 1000, .001, 3000, 3000, 0, 300, 1000},
		{"all_zero", 0, 0, 30, 30, 0, 0, 0},
		{"native_rounds_zero", 10, 1000, 3000, 3000.5, 300, 300, 3000.5},
		{"underfill", 1000, 8, 20, 2000.5, 150, 170, 3000.5},
		{"empty_positive", 0, 1, 0, 3000.5, 300, 0, 3000.5},
		{"finite_extreme", 1000, math.MaxFloat64, 300, 3000.5, 300, 300, 3000.5},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newInheritedForeignFixture(t, test.native, false)
			source := mixedForeignSource(test.pages, test.sample, test.population)
			f.wrapper.sources["mixed_foreign"] = source
			if test.quota == 0 {
				enginetest.SessionSQL(t, f.s, "DROP USER MAPPING FOR CURRENT_USER SERVER mixed_owner_server")
			}
			f.analyze(t, false, "")
			metadata := f.e.Storage.RelationMetadata[f.parent.Address]
			if metadata.InheritedTuples != test.total || !metadata.InheritedScanned || metadata.AnalyzeCount != 1 {
				t.Fatal("skipped population guessed or acquired estimate lost", metadata)
			}
			if test.quota == 0 {
				if len(source.targets) != 0 {
					t.Fatal("zero work acquired a sample", source.targets)
				}
			} else if len(source.targets) != 1 || source.targets[0] != test.quota {
				t.Fatal("wrong rounded/capped quota", source.targets)
			}
			if test.rows != 0 {
				if fact := f.inherited(t, 0); fact.Rows != test.rows {
					t.Fatal("sample padded, biased or counted quota rather than actual rows", fact)
				}
			} else if metadata.Statistics != nil {
				for key := range metadata.Statistics.Columns {
					if key.Inherited {
						t.Fatal("empty sample fabricated inherited distribution")
					}
				}
			}
			if f.wrapper.active.Load() != 0 {
				t.Fatal("acquisition still active")
			}
		})
	}
}

func TestAnalyzeInheritedForeignExtendedSample(t *testing.T) {
	f := newInheritedForeignFixture(t, 1000, false)
	for _, query := range []string{
		"CREATE STATISTICS mixed_joint(ndistinct,dependencies,mcv) ON a,b FROM mixed_parent",
		"ALTER STATISTICS mixed_joint SET STATISTICS 2",
	} {
		enginetest.SessionSQL(t, f.s, query)
	}
	f.analyze(t, true, "")
	key := ndistinctKey(t, f.e, "mixed_joint")
	key.Inherited = true
	facts := f.e.Storage.RelationMetadata[f.parent.Address].Statistics
	extended := facts.Extended[key]
	source := f.wrapper.sources["mixed_foreign"]
	if len(source.targets) != 1 || source.targets[0] != 450 || extended == nil || len(extended.Items) != 1 || extended.Items[0].Value != 2 || len(extended.DependencyItems) != 2 || extended.MCVData == nil || extended.MCVData.Rows != 600 || len(extended.MCVData.Items) != 2 || extended.MCVData.Items[0].Count != 450 || extended.MCVData.Items[1].Count != 150 {
		t.Fatal("admitted extended target did not share one genuine150/450 acquisition", source.targets, extended)
	}
	if f.inherited(t, 0).Rows != 600 || mixedMCVCount(t, f.inherited(t, 0), 2) != 450 {
		t.Fatal("scalar and extended sample streams diverged")
	}
	root := f.e.CatalogRead().MustRelation(f.parent.Address)
	calls := len(f.wrapper.negotiated)
	f.wrapper.hook = func(context.Context, string, string) error {
		t.Fatal("metadata consumer negotiated provider")
		return nil
	}
	if !storage.StatisticsHierarchyCurrent(extended.Hierarchy, root, f.e.CatalogRead()) || storage.CompatibleExtendedStatistics(facts, root, f.e.CatalogRead()).Extended[key] != extended {
		t.Fatal("provider-free hierarchy rejected real foreign member")
	}
	enginetest.SessionSQL(t, f.s, "EXPLAIN SELECT * FROM mixed_parent WHERE a=2")
	if len(f.wrapper.negotiated) != calls {
		t.Fatal("EXPLAIN negotiated source")
	}
}

func TestAnalyzeInheritedForeignUnderfillCapacity(t *testing.T) {
	f := newInheritedForeignFixture(t, 0, false)
	enginetest.SessionSQL(t, f.s, "CREATE FOREIGN TABLE mixed_last(a integer,b integer) SERVER mixed_owner_server")
	enginetest.SessionSQL(t, f.s, "ALTER FOREIGN TABLE mixed_last INHERIT mixed_parent")
	f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 20, 200)
	last := &inheritedForeignSource{pages: 3, sample: catalog.ForeignSample{TotalRows: 900, TotalPages: 3}}
	for range 300 {
		last.sample.Rows = append(last.sample.Rows, catalog.ForeignRow{int64(3), int64(30)})
	}
	f.wrapper.sources["mixed_last"] = last
	f.analyze(t, false, "")
	first := f.wrapper.sources["mixed_foreign"]
	if len(first.targets) != 1 || first.targets[0] != 75 || len(last.targets) != 1 || last.targets[0] != 225 || f.inherited(t, 0).Rows != 245 || f.e.Storage.RelationMetadata[f.parent.Address].InheritedTuples != 1100 {
		t.Fatal("underfill consumed quota rather than actual capacity", first.targets, last.targets)
	}
}
