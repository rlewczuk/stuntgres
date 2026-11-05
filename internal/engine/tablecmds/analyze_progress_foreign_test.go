package tablecmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

type analyzeProgressWrapper struct {
	pages                 float64
	sample                catalog.ForeignSample
	onAcquire             func(context.Context, catalog.ForeignRequest, int) (catalog.ForeignSample, error)
	acquisitions, imports int
	importPhases          []string
}

func (*analyzeProgressWrapper) GetForeignRelSize(context.Context, catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	return catalog.ForeignRelSize{}, errors.New("unexpected query size request")
}
func (*analyzeProgressWrapper) GetForeignPaths(context.Context, catalog.ForeignRequest, catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	return nil, errors.New("unexpected query paths request")
}
func (*analyzeProgressWrapper) GetForeignPlan(context.Context, catalog.ForeignRequest, catalog.ForeignPath) (catalog.ForeignPlan, error) {
	return catalog.ForeignPlan{}, errors.New("unexpected query plan request")
}
func (*analyzeProgressWrapper) BeginForeignScan(context.Context, catalog.ForeignRequest, catalog.ForeignPlan) (catalog.ForeignScan, error) {
	return nil, errors.New("unexpected query scan request")
}
func (w *analyzeProgressWrapper) AnalyzeForeignTable(context.Context, catalog.ForeignRequest) (catalog.ForeignAnalyzeInfo, bool, error) {
	return catalog.ForeignAnalyzeInfo{TotalPages: w.pages, AcquireSampleRows: func(ctx context.Context, request catalog.ForeignRequest, target int) (catalog.ForeignSample, error) {
		w.acquisitions++
		if w.onAcquire != nil {
			return w.onAcquire(ctx, request, target)
		}
		return w.sample, nil
	}}, true, nil
}

func analyzeCurrent(e *core.Engine) []pgcatalog.AnalyzeProgressFact {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth := enginetest.CatalogAuthorization(e, e.Catalog.Current())
	var facts []pgcatalog.AnalyzeProgressFact
	if err := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth}).VisitAnalyzeProgress(context.Background(), func(f pgcatalog.AnalyzeProgressFact) error { facts = append(facts, f); return nil }); err != nil {
		panic(err)
	}
	return facts
}

func analyzeForeignFixture(t *testing.T) (*core.Engine, *session.Session, *analyzeProgressWrapper) {
	t.Helper()
	e := engine.New()
	w := &analyzeProgressWrapper{pages: 1}
	definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "ap_handler"}, Wrapper: w})
	if err != nil {
		t.Fatal(err)
	}
	err = e.InstallForeignDefinitions(nil, []fdw.WrapperDefinition{definition}, nil, func(_ context.Context, value driver.Value, usage sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
		w.imports++
		facts := analyzeCurrent(e)
		if len(facts) != 1 {
			return sqltype.Value{}, fmt.Errorf("import without admitted command")
		}
		w.importPhases = append(w.importPhases, facts[0].Phase)
		if value == nil {
			return sqltype.Null(usage.Type), nil
		}
		return sqltype.Integer(usage.Type, value.(int64))
	}, func(_ context.Context, value sqltype.Value, _ sqltype.OutputEnvironment) (driver.Value, error) {
		return enginetest.Export(value), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE FOREIGN DATA WRAPPER ap_fdw HANDLER ap_handler", "CREATE SERVER ap_server FOREIGN DATA WRAPPER ap_fdw", "CREATE USER MAPPING FOR CURRENT_USER SERVER ap_server"} {
		enginetest.SessionSQL(t, s, q)
	}
	return e, s, w
}

func analyzeForeignPhaseBoundaries(t *testing.T) {
	e, s, w := analyzeForeignFixture(t)
	for _, q := range []string{"CREATE FOREIGN TABLE ap(a INT,b INT,c INT) SERVER ap_server", "CREATE STATISTICS ap_joint(ndistinct,dependencies,mcv) ON a,b FROM ap", "CREATE STATISTICS ap_expr ON (a+1) FROM ap", "CREATE STATISTICS ap_skip(ndistinct) ON a,c FROM ap"} {
		enginetest.SessionSQL(t, s, q)
	}
	w.sample = catalog.ForeignSample{Rows: []catalog.ForeignRow{{int64(1), int64(1), int64(9)}, {int64(1), int64(1), int64(8)}, {int64(2), int64(2), int64(7)}}, TotalRows: 1000, TotalPages: 8}
	w.onAcquire = func(_ context.Context, _ catalog.ForeignRequest, _ int) (catalog.ForeignSample, error) {
		facts := analyzeCurrent(e)
		if len(facts) != 1 || facts[0].Phase != "acquiring sample rows" || facts[0].SampleBlocksTotal != 0 || facts[0].SampleBlocksScanned != 0 {
			t.Fatal("acquisition did not expose admitted unknown sample", facts)
		}
		return w.sample, nil
	}
	events, err := analyzeTrace(t, e, s, "ANALYZE ap(a,b)", nil)
	if err != nil {
		t.Fatal(err)
	}
	if w.acquisitions != 1 || w.imports != 9 {
		t.Fatal("sample reacquired or cells reimported", w.acquisitions, w.imports)
	}
	for _, phase := range w.importPhases {
		if phase != "acquiring sample rows" {
			t.Fatal("foreign import happened after scalar phase", phase)
		}
	}
	analyzeRequireEvent(t, events, "direct real scalar Finish", func(f analyzeBoundary) bool {
		return f.Phase == "computing statistics" && f.SampleBlocksTotal == 1 && f.SampleBlocksScanned == 1
	})
	analyzeRequireEvent(t, events, "direct expressions run only in postscalar extended phase", func(f analyzeBoundary) bool {
		return strings.Contains(f.caller, "analyzeForeignPass).buildExtended") && f.Phase == "computing extended statistics" && f.ExtendedStatsTotal == 3
	})
	analyzeRequireEvent(t, events, "direct fetched/accepted whole definitions", func(f analyzeBoundary) bool {
		return f.Phase == "finalizing analyze" && f.ExtendedStatsTotal == 3 && f.ExtendedStatsComputed == 2
	})
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "ap")
	metadata := e.Storage.RelationMetadata[ref.Address]
	if metadata.ForeignAnalyze.SampleRows != 3 || metadata.ForeignAnalyze.TotalRows != 1000 || len(metadata.Statistics.Extended) != 2 || e.Storage.Relations[ref.Address] != nil {
		t.Fatal("direct real products/population missing or fake heap")
	}
	for _, fact := range metadata.Statistics.Columns {
		if fact.Rows != 3 {
			t.Fatal("sample multiplicity changed to estimate")
		}
	}
	// Provider rows may be reused immediately. Previously accepted typed products
	// retain their own distinct/MCV/expression backing through the next import.
	for _, row := range w.sample.Rows {
		row[0], row[1] = int64(9), int64(9)
	}
	if _, err := analyzeTrace(t, e, s, "ANALYZE ap(a,b)", nil); err != nil {
		t.Fatal(err)
	}
	fresh := e.Storage.RelationMetadata[ref.Address].Statistics
	for key, fact := range metadata.Statistics.Extended {
		if fresh.Extended[key] == fact {
			t.Fatal("real changed sample did not replace selected backing")
		}
		if fact.ExpressionLen() != 0 {
			if fact.Expression(0).Rows != 3 || fact.Expression(0).DistinctCount != 2 || fresh.Extended[key].Expression(0).DistinctCount != 1 {
				t.Fatal("expression sample or saved backing changed")
			}
		} else {
			if len(fact.Items) != 1 || fact.Items[0].Value != 2 || len(fact.DependencyItems) != 2 || fact.MCVData == nil || fact.MCVData.Rows != 3 || fact.MCVData.Items[0].Count != 2 || fresh.Extended[key].Items[0].Value != 1 {
				t.Fatal("whole definition products or old immutable backing changed")
			}
		}
	}
}

func analyzeForeignSegments(t *testing.T) {
	for _, mode := range []string{"positive", "zero_weight", "failure"} {
		t.Run(mode, func(t *testing.T) {
			e, s, w := analyzeForeignFixture(t)
			for _, q := range []string{"CREATE TABLE ap(a INT,b INT)", "CREATE FOREIGN TABLE ac(extra INT,b INT,a INT) SERVER ap_server", "ALTER FOREIGN TABLE ac INHERIT ap", "CREATE STATISTICS ap_joint(ndistinct,dependencies,mcv) ON a,b FROM ap", "CREATE STATISTICS ap_expr ON (a+1) FROM ap"} {
				enginetest.SessionSQL(t, s, q)
			}
			if mode != "zero_weight" {
				enginetest.SessionSQL(t, s, "INSERT INTO ap SELECT i,i FROM generate_series(1,129) i")
			} else {
				w.pages = 0
			}
			for i := range 129 {
				w.sample.Rows = append(w.sample.Rows, catalog.ForeignRow{int64(99), int64(i), int64(i)})
			}
			w.sample.TotalRows = 1000
			child := enginetest.CatalogRef(t, e, e.CatalogRead(), "ac").Address.ObjectID
			failure := errors.New("real acquisition failure")
			w.onAcquire = func(_ context.Context, _ catalog.ForeignRequest, _ int) (catalog.ForeignSample, error) {
				facts := analyzeCurrent(e)
				if len(facts) != 1 || facts[0].Phase != "acquiring inherited sample rows" || facts[0].CurrentChild != child || facts[0].ChildTablesDone != 1 || facts[0].SampleBlocksTotal != 0 || facts[0].SampleBlocksScanned != 0 {
					t.Fatal("acquisition preceded child/reset or falsely completed visit", facts)
				}
				if mode == "failure" {
					return catalog.ForeignSample{}, failure
				}
				return w.sample, nil
			}
			events, err := analyzeTrace(t, e, s, "ANALYZE ONLY ap", nil)
			if mode == "failure" {
				if !errors.Is(err, failure) {
					t.Fatal(err)
				}
				for _, f := range events {
					if f.CurrentChild == child && f.ChildTablesDone != 1 {
						t.Fatal("failed visit completed", f)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantBlocks, wantAcquires, wantImports := int64(2), 1, 387
			if mode == "zero_weight" {
				wantBlocks, wantAcquires, wantImports = 0, 0, 0
			}
			if w.acquisitions != wantAcquires || w.imports != wantImports {
				t.Fatal("zero quota acquired or positive visit reimported", w.acquisitions, w.imports)
			}
			analyzeRequireEvent(t, events, "foreign visit completion retained final child", func(f analyzeBoundary) bool {
				return f.Phase == "finalizing analyze" && f.ChildTablesTotal == 2 && f.ChildTablesDone == 2 && f.CurrentChild == child && f.SampleBlocksTotal == wantBlocks && f.SampleBlocksScanned == wantBlocks
			})
			if mode == "positive" {
				analyzeRequireEvent(t, events, "foreign prefix128 of129", func(f analyzeBoundary) bool {
					return f.CurrentChild == child && f.SampleBlocksTotal == 2 && f.SampleBlocksScanned == 1
				})
				ref, _ := vacuumStore(t, e, "ap")
				metadata := e.Storage.RelationMetadata[ref.Address]
				if metadata.InheritedTuples != 1129 || len(metadata.Statistics.Extended) != 4 {
					t.Fatal("actual population pointer or projected products lost", metadata.InheritedTuples)
				}
			}
		})
	}
}

func analyzeForeignRetainedCounts(t *testing.T) {
	e, s, w := analyzeForeignFixture(t)
	for _, q := range []string{"CREATE FOREIGN TABLE ap(a INT,b INT,c INT) SERVER ap_server", "CREATE TABLE ac() INHERITS(ap)", "CREATE STATISTICS ap_joint(ndistinct,dependencies,mcv) ON a,b FROM ap", "CREATE STATISTICS ap_expr ON (a+1) FROM ap", "CREATE STATISTICS ap_skip(ndistinct) ON a,c FROM ap"} {
		enginetest.SessionSQL(t, s, q)
	}
	w.onAcquire = func(_ context.Context, _ catalog.ForeignRequest, _ int) (catalog.ForeignSample, error) {
		if w.acquisitions == 1 {
			return catalog.ForeignSample{Rows: []catalog.ForeignRow{{int64(1), int64(1), int64(1)}, {int64(2), int64(2), int64(2)}}, TotalRows: 10}, nil
		}
		return catalog.ForeignSample{TotalRows: 10}, nil
	}
	events, err := analyzeTrace(t, e, s, "ANALYZE ONLY ap(a,b)", nil)
	if err != nil {
		t.Fatal(err)
	}
	analyzeRequireEvent(t, events, "zero inherited actual sample retains completed local2", func(f analyzeBoundary) bool {
		return f.Phase == "finalizing analyze" && f.ChildTablesDone == 2 && f.ExtendedStatsTotal == 3 && f.ExtendedStatsComputed == 2 && f.SampleBlocksTotal == 0 && f.SampleBlocksScanned == 0
	})
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "ap")
	metadata := e.Storage.RelationMetadata[ref.Address]
	if metadata.AnalyzeCount != 1 || metadata.InheritedTuples != 10 || len(metadata.Statistics.Extended) != 2 || w.imports != 6 || w.acquisitions != 2 {
		t.Fatal("empty inherited work fabricated products or lost real local work")
	}
}
