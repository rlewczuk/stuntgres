package core_test

import (
	"context"
	"errors"
	"math"
	"runtime"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
)

func TestAnalyzeInheritedForeignPassFailure(t *testing.T) {
	for _, partition := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			for _, failure := range []string{"mapping", "provider", "import", "negative_pages", "nan_pages", "infinite_pages", "nil_sampler", "sample_overflow", "sample_width", "sample_negative", "sample_nan", "sample_infinite", "population_overflow"} {
				t.Run(map[bool]string{false: "ordinary", true: "partition"}[partition]+map[bool]string{false: "/direct/", true: "/prepared/"}[prepared]+failure, func(t *testing.T) {
					f := newInheritedForeignFixture(t, 10, partition)
					f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 30)
					enginetest.SessionSQL(t, f.s, "CREATE STATISTICS mixed_failure(ndistinct,dependencies,mcv) ON a,b FROM mixed_parent")
					enginetest.SessionSQL(t, f.s, "ALTER STATISTICS mixed_failure SET STATISTICS 1")
					if failure == "population_overflow" {
						enginetest.SessionSQL(t, f.s, "CREATE FOREIGN TABLE mixed_overflow(a integer,b integer) SERVER mixed_owner_server")
						if partition {
							enginetest.SessionSQL(t, f.s, "ALTER TABLE mixed_parent ATTACH PARTITION mixed_overflow FOR VALUES FROM(4) TO(6)")
						} else {
							enginetest.SessionSQL(t, f.s, "ALTER FOREIGN TABLE mixed_overflow INHERIT mixed_parent")
						}
						f.wrapper.sources["mixed_overflow"] = &inheritedForeignSource{pages: 1, sample: catalog.ForeignSample{Rows: []catalog.ForeignRow{{int64(4), int64(40)}}, TotalRows: 1, TotalPages: 1}}
					}
					f.analyze(t, false, "")
					before := f.e.Storage.RelationMetadata[f.parent.Address]
					retained := f.retained(t)
					for range 20 {
						enginetest.SessionSQL(t, f.s, "INSERT INTO mixed_parent VALUES(1,11)")
					}
					sentinel := errors.New("inherited acquisition failed")
					source := f.wrapper.sources["mixed_foreign"]
					switch failure {
					case "mapping":
						enginetest.SessionSQL(t, f.s, "DROP USER MAPPING FOR CURRENT_USER SERVER mixed_owner_server")
					case "provider":
						source.failure = sentinel
					case "import":
						f.wrapper.importHook = func(context.Context) error { return sentinel }
					case "negative_pages":
						source.pages = -1
					case "nan_pages":
						source.pages = math.NaN()
					case "infinite_pages":
						source.pages = math.Inf(1)
					case "nil_sampler":
						source.nilSampler = true
					case "sample_overflow":
						source.sample = mixedForeignSource(1, 301, 301).sample
						source.uncapped = true
					case "sample_width":
						source.sample.Rows = []catalog.ForeignRow{{int64(2)}}
					case "sample_negative":
						source.sample.TotalDeadRows = -1
					case "sample_nan":
						source.sample.TotalRows = math.NaN()
					case "sample_infinite":
						source.sample.TotalPages = math.Inf(1)
					case "population_overflow":
						source.sample.TotalRows = math.MaxFloat64
						f.wrapper.sources["mixed_overflow"].sample.TotalRows = math.MaxFloat64
					}
					err := f.execute(t, context.Background(), "ANALYZE ONLY mixed_parent", prepared)
					if err == nil {
						t.Fatal("invalid inherited work succeeded")
					}
					if (failure == "provider" || failure == "import") && !errors.Is(err, sentinel) {
						t.Fatal("original provider/import error lost", err)
					}
					after := f.e.Storage.RelationMetadata[f.parent.Address]
					if after.Statistics != before.Statistics || after.InheritedTuples != before.InheritedTuples || after.InheritedScanned != before.InheritedScanned {
						t.Fatal("failed inherited work published typed or inherited deltas")
					}
					if partition {
						if after != before || after.Scanned || after.Tuples != 0 || f.e.Storage.Relations[f.parent.Address] != nil {
							t.Fatal("partition fabricated completed local pass")
						}
					} else {
						if after.AnalyzeCount != before.AnalyzeCount+1 || !after.LastAnalyze.After(before.LastAnalyze) || f.e.Storage.Relations[f.parent.Address].Maintenance.Tuples != 30 {
							t.Fatal("completed local30/count+1/time lost after inherited failure", before, after)
						}
					}
					if journal := f.s.Transactions.Catalog; journal != nil && len(journal.Statistics) != 0 {
						t.Fatal("failed statement retained typed delta")
					}
					if f.e.Storage.RelationMetadata[f.foreign.Address] != nil || f.wrapper.active.Load() != 0 {
						t.Fatal("failed inherited work leaked child publication/resources")
					}
					retained()
					enginetest.LockEmpty(t, f.e)
				})
			}
		}
	}
}

func TestAnalyzeInheritedForeignPassCancellation(t *testing.T) {
	for _, partition := range []bool{false, true} {
		for _, phase := range []string{"before", "negotiate", "acquire", "import"} {
			t.Run(map[bool]string{false: "ordinary/", true: "partition/"}[partition]+phase, func(t *testing.T) {
				f := newInheritedForeignFixture(t, 10, partition)
				f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 30)
				f.analyze(t, false, "")
				enginetest.SessionSQL(t, f.s, "CREATE STATISTICS mixed_cancel(ndistinct,dependencies,mcv) ON a,b FROM mixed_parent")
				enginetest.SessionSQL(t, f.s, "CREATE STATISTICS mixed_cancel_expression ON (a+b) FROM mixed_parent")
				f.analyze(t, false, "")
				before := f.e.Storage.RelationMetadata[f.parent.Address]
				retained := f.retained(t)
				enginetest.SessionSQL(t, f.s, "INSERT INTO mixed_parent VALUES(1,11)")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				entered := false
				if phase == "before" {
					cancel()
				} else if phase == "import" {
					f.wrapper.importHook = func(context.Context) error { entered = true; cancel(); return ctx.Err() }
				} else {
					f.wrapper.hook = func(_ context.Context, p, _ string) error {
						if p == phase {
							entered = true
							cancel()
							return ctx.Err()
						}
						return nil
					}
				}
				err := f.execute(t, ctx, "ANALYZE ONLY mixed_parent", true)
				if !errors.Is(err, context.Canceled) || (phase != "before" && !entered) {
					t.Fatal("cancellation phase missed or ordinary error changed", entered, err)
				}
				after := f.e.Storage.RelationMetadata[f.parent.Address]
				if after.Statistics != before.Statistics || after.InheritedTuples != 40 {
					t.Fatal("cancelled inherited pass changed typed/population backing")
				}
				if partition || phase == "before" {
					if after != before {
						t.Fatal("cancel before completed pass published maintenance")
					}
				} else if after.AnalyzeCount != before.AnalyzeCount+1 || !after.LastAnalyze.After(before.LastAnalyze) || f.e.Storage.Relations[f.parent.Address].Maintenance.Tuples != 11 {
					t.Fatal("cancel retracted completed local pass")
				}
				if f.wrapper.active.Load() != 0 {
					t.Fatal("canceled acquisition still active")
				}
				retained()
				enginetest.LockEmpty(t, f.e)
			})
		}
	}
}

func TestAnalyzeInheritedForeignRootPass(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			f := newInheritedForeignFixture(t, 0, false)
			for _, query := range []string{"CREATE TABLE mixed_root_child(b integer,a integer)", "ALTER TABLE mixed_root_child INHERIT mixed_foreign", "INSERT INTO mixed_root_child VALUES(10,1),(10,1),(10,1),(10,1),(10,1),(10,1),(10,1),(10,1),(10,1),(10,1)", "ALTER FOREIGN TABLE mixed_foreign ALTER a SET STATISTICS 1", "ALTER FOREIGN TABLE mixed_foreign ALTER b SET STATISTICS 1"} {
				enginetest.SessionSQL(t, f.s, query)
			}
			f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 30)
			f.parent = f.foreign
			if err := f.execute(t, context.Background(), "ANALYZE ONLY mixed_foreign", prepared); err != nil {
				t.Fatal(err)
			}
			before := f.e.Storage.RelationMetadata[f.parent.Address]
			if before.ForeignAnalyze == nil || before.ForeignAnalyze.TotalRows != 30 || before.InheritedTuples != 40 || before.AnalyzeCount != 1 || f.e.Storage.Relations[f.parent.Address] != nil || f.inherited(t, 1).Rows != 40 || mixedMCVCount(t, f.inherited(t, 1), 2) != 30 {
				t.Fatal("foreign root omitted own local or inherited pass", before)
			}
			retained := f.retained(t)
			early := errors.New("foreign root local failure")
			f.wrapper.sources["mixed_foreign"].failure = early
			if err := f.execute(t, context.Background(), "ANALYZE ONLY mixed_foreign", prepared); !errors.Is(err, early) {
				t.Fatal("foreign local failure missed", err)
			}
			if f.e.Storage.RelationMetadata[f.parent.Address] != before {
				t.Fatal("failed foreign local pass published maintenance or typed products")
			}
			f.wrapper.sources["mixed_foreign"].failure = nil
			f.wrapper.sources["mixed_foreign"].sample.TotalRows = 50.5
			acquisitions := 0
			sentinel := errors.New("foreign root inherited failure")
			f.wrapper.hook = func(_ context.Context, phase, _ string) error {
				if phase == "acquire" {
					acquisitions++
					if acquisitions == 2 {
						return sentinel
					}
				}
				return nil
			}
			if err := f.execute(t, context.Background(), "ANALYZE ONLY mixed_foreign", prepared); !errors.Is(err, sentinel) {
				t.Fatal("missing later inherited failure", err)
			}
			after := f.e.Storage.RelationMetadata[f.parent.Address]
			if acquisitions != 2 || after.ForeignAnalyze.TotalRows != 50.5 || after.AnalyzeCount != before.AnalyzeCount+1 || !after.LastAnalyze.After(before.LastAnalyze) || after.InheritedTuples != 40 || after.Statistics != before.Statistics {
				t.Fatal("foreign local maintenance not independent of inherited typed publication", after)
			}
			retained()
			f.wrapper.hook = nil
			if err := f.execute(t, context.Background(), "ANALYZE ONLY mixed_foreign", prepared); err != nil {
				t.Fatal(err)
			}
			retried := f.e.Storage.RelationMetadata[f.parent.Address]
			if retried.AnalyzeCount != after.AnalyzeCount+1 || retried.ForeignAnalyze.TotalRows != 50.5 || retried.InheritedTuples != 60.5 || retried.Statistics == before.Statistics {
				t.Fatal("successful foreign retry failed to publish both flavors once", retried)
			}
			// Direct local acquisition is deliberately unchanged at zero
			// negotiated weight; only the inherited visit skips its sampler.
			source := f.wrapper.sources["mixed_foreign"]
			source.pages = 0
			source.targets = nil
			if err := f.execute(t, context.Background(), "ANALYZE ONLY mixed_foreign", prepared); err != nil {
				t.Fatal(err)
			}
			zero := f.e.Storage.RelationMetadata[f.parent.Address]
			if len(source.targets) != 1 || zero.ForeignAnalyze.TotalRows != 50.5 || zero.InheritedTuples != 10 || zero.AnalyzeCount != retried.AnalyzeCount+1 {
				t.Fatal("zero negotiated weight suppressed direct local or guessed inherited root population", source.targets, zero)
			}
			retained()
			enginetest.LockEmpty(t, f.e)
		})
	}
}

// Empty acquired samples carry population, not invented typed records. Both
// selected and unselected compatible payloads retain their precise backing.
func TestAnalyzeInheritedForeignEmptyRetention(t *testing.T) {
	f := newInheritedForeignFixture(t, 0, false)
	f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 30)
	f.analyze(t, false, "")
	before := f.e.Storage.RelationMetadata[f.parent.Address]
	retained := f.retained(t)
	f.wrapper.sources["mixed_foreign"].sample = catalog.ForeignSample{TotalRows: 17.5, TotalPages: 9}
	f.analyze(t, true, "(a)")
	after := f.e.Storage.RelationMetadata[f.parent.Address]
	if after.InheritedTuples != 17.5 || after.AnalyzeCount != before.AnalyzeCount+1 {
		t.Fatal("empty acquisition lost genuine population/success")
	}
	for key, fact := range before.Statistics.Columns {
		if key.Inherited && after.Statistics.Columns[key] != fact {
			t.Fatal("empty sample replaced compatible typed backing", key)
		}
	}
	retained()
}

// Match the actual authority caller, not a check count or source line. This is
// a test-owned Context seam; production has no publication observer.
type mixedPassInstallContext struct {
	context.Context
	cancel context.CancelFunc
	owner  string
	fired  bool
}

func (c *mixedPassInstallContext) Err() error {
	var pcs [2]uintptr
	if runtime.Callers(2, pcs[:]) == len(pcs) {
		frames := runtime.CallersFrames(pcs[:])
		checkpoint, _ := frames.Next()
		owner, _ := frames.Next()
		if checkpoint.Function == "stuntgres/internal/engine/tablecmds.(*analyzeRun).authorityLocked" &&
			owner.Function == "stuntgres/internal/engine/tablecmds.(*analyzeRun)."+c.owner {
			c.fired = true
			c.cancel()
		}
	}
	return c.Context.Err()
}

func TestAnalyzeInheritedForeignInstallCancellation(t *testing.T) {
	for _, phase := range []string{"publishLocal", "publishFinal"} {
		for _, partition := range []bool{false, true} {
			if partition && phase == "publishLocal" {
				continue // A real partition root has no such pass.
			}
			t.Run(phase+map[bool]string{false: "/ordinary", true: "/partition"}[partition], func(t *testing.T) {
				f := newInheritedForeignFixture(t, 10, partition)
				f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 30)
				f.analyze(t, false, "")
				enginetest.SessionSQL(t, f.s, "CREATE STATISTICS mixed_install(ndistinct,dependencies,mcv) ON a,b FROM mixed_parent")
				enginetest.SessionSQL(t, f.s, "CREATE STATISTICS mixed_install_expression ON (a+b) FROM mixed_parent")
				f.analyze(t, false, "")
				before := f.e.Storage.RelationMetadata[f.parent.Address]
				retained := f.retained(t)
				enginetest.SessionSQL(t, f.s, "INSERT INTO mixed_parent VALUES(1,11)")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, err := f.s.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				checkpoint := &mixedPassInstallContext{Context: op.Ctx, cancel: cancel, owner: phase}
				op.Ctx = checkpoint
				_, err = f.e.Execute(op, enginetest.Statement(t, "ANALYZE ONLY mixed_parent"), 0, nil)
				op.Done()
				if !checkpoint.fired || !errors.Is(err, context.Canceled) {
					t.Fatal("actual mixed pass install checkpoint missed", checkpoint.fired, err)
				}
				after := f.e.Storage.RelationMetadata[f.parent.Address]
				if after.Statistics != before.Statistics || after.InheritedTuples != 40 {
					t.Fatal("preinstall cancellation leaked complete typed candidate")
				}
				if partition || phase == "publishLocal" {
					if after != before {
						t.Fatal("uncompleted pass changed maintenance authority")
					}
				} else if after.AnalyzeCount != before.AnalyzeCount+1 || !after.LastAnalyze.After(before.LastAnalyze) || f.e.Storage.Relations[f.parent.Address].Maintenance.Tuples != 11 {
					t.Fatal("final gate cancellation retracted completed local maintenance")
				}
				retained()
				enginetest.LockEmpty(t, f.e)
			})
		}
	}
}

func TestAnalyzeInheritedForeignStandaloneBoundary(t *testing.T) {
	for _, partition := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "ordinary", true: "partition"}[partition]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				f := newInheritedForeignFixture(t, 10, partition)
				f.wrapper.sources["mixed_foreign"] = mixedForeignSource(1, 30, 30)
				for _, query := range []string{"CREATE TABLE mixed_completed(a integer)", "INSERT INTO mixed_completed VALUES(1),(1)", "ANALYZE mixed_completed", "CREATE STATISTICS mixed_standalone(ndistinct,dependencies,mcv) ON a,b FROM mixed_parent"} {
					enginetest.SessionSQL(t, f.s, query)
				}
				f.analyze(t, false, "")
				first := enginetest.CatalogRef(t, f.e, f.e.CatalogRead(), "mixed_completed")
				firstBefore := f.e.Storage.RelationMetadata[first.Address]
				before := f.e.Storage.RelationMetadata[f.parent.Address]
				retained := f.retained(t)
				enginetest.SessionSQL(t, f.s, "INSERT INTO mixed_completed VALUES(2),(2)")
				failure := errors.New("later standalone inherited target failure")
				f.wrapper.sources["mixed_foreign"].failure = failure
				if err := f.execute(t, context.Background(), "ANALYZE mixed_completed, ONLY mixed_parent", prepared); !errors.Is(err, failure) {
					t.Fatal("later target failure missed", err)
				}
				firstAfter := f.e.Storage.RelationMetadata[first.Address]
				if firstAfter.AnalyzeCount != firstBefore.AnalyzeCount+1 || !firstAfter.LastAnalyze.After(firstBefore.LastAnalyze) || firstAfter.Statistics == firstBefore.Statistics || f.e.Storage.Relations[first.Address].Maintenance.Tuples != 4 {
					t.Fatal("later target failure retracted completed standalone target")
				}
				after := f.e.Storage.RelationMetadata[f.parent.Address]
				if after.Statistics != before.Statistics || after.InheritedTuples != 40 {
					t.Fatal("failed later target published typed/inherited products")
				}
				if partition {
					if after != before {
						t.Fatal("partition later failure fabricated completion")
					}
				} else if after.AnalyzeCount != before.AnalyzeCount+1 {
					t.Fatal("ordinary later failure retracted completed local pass")
				}
				retained()
				enginetest.LockEmpty(t, f.e)
			})
		}
	}
}
