package tablecmds_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func analyzeDefinitionFixture(t *testing.T) (*core.Engine, *session.Session) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE ap(a INT,b INT,c INT)",
		"CREATE TABLE ac() INHERITS(ap)",
		"INSERT INTO ap VALUES(1,1,1),(1,1,2),(2,2,3)",
		"INSERT INTO ac VALUES(3,3,4)",
		"CREATE STATISTICS ap_joint(ndistinct,dependencies,mcv) ON a,b FROM ap",
		"CREATE STATISTICS ap_expr ON (a+1) FROM ap",
		"CREATE STATISTICS ap_missing(ndistinct) ON a,c FROM ap",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	return e, s
}

func TestAnalyzeProgressPhaseBoundaries(t *testing.T) {
	t.Run("foreign_import_once", analyzeForeignPhaseBoundaries)
	t.Run("scalar_then_expression_then_publication", func(t *testing.T) {
		e, s := analyzeDefinitionFixture(t)
		events, err := analyzeTrace(t, e, s, "ANALYZE ONLY ap(a,b)", nil)
		if err != nil {
			t.Fatal(err)
		}
		analyzeRequireEvent(t, events, "scalar Finish is computing", func(f analyzeBoundary) bool {
			return strings.Contains(f.caller, "statisticsCollector).finish") && f.Phase == "computing statistics"
		})
		extended := 0
		for _, f := range events {
			if strings.Contains(f.stack, "tablecmds.(*analyzeRun).buildExtended") {
				extended++
				if f.Phase != "computing extended statistics" || f.ExtendedStatsTotal != 3 {
					t.Fatal("extended replay ran during scalar collection", f)
				}
			}
			if strings.Contains(f.stack, "tablecmds.(*analyzeRun).publishLocal") || strings.Contains(f.stack, "tablecmds.(*analyzeRun).publishFinal") {
				if f.Phase != "finalizing analyze" {
					t.Fatal("publication mislabeled", f)
				}
			}
		}
		if extended == 0 {
			t.Fatal("real extended replay was not exercised")
		}
		ref, _ := vacuumStore(t, e, "ap")
		facts := e.Storage.RelationMetadata[ref.Address].Statistics
		if len(facts.Extended) != 4 {
			t.Fatal("local/inherited whole typed definitions not published", len(facts.Extended))
		}
		for key, fact := range facts.Extended {
			if fact == nil || key.Inherited && fact.Hierarchy == nil {
				t.Fatal("missing completed hierarchy authority")
			}
		}
	})
	for _, boundary := range []string{"publishLocal", "publishFinal"} {
		t.Run("cancel_"+boundary, func(t *testing.T) {
			e, s := analyzeDefinitionFixture(t)
			enginetest.SessionSQL(t, s, "ANALYZE ONLY ap(a,b)")
			ref, _ := vacuumStore(t, e, "ap")
			before := e.Storage.RelationMetadata[ref.Address]
			backing := before.Statistics
			old := make(map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics)
			for key, fact := range backing.Extended {
				old[key] = fact
			}
			enginetest.SessionSQL(t, s, "INSERT INTO ap VALUES(4,4,4)")
			fired := false
			_, err := analyzeTrace(t, e, s, "ANALYZE ONLY ap(a,b)", func(f analyzeBoundary, cancel context.CancelFunc) {
				if strings.Contains(f.stack, "tablecmds.(*analyzeRun)."+boundary) && f.Phase == "finalizing analyze" {
					fired = true
					cancel()
				}
			})
			if !fired || !errors.Is(err, context.Canceled) {
				t.Fatal("real finalization cancellation missed", fired, err)
			}
			fresh := e.Storage.RelationMetadata[ref.Address]
			if fresh.Statistics != backing {
				t.Fatal("unfinished typed products published")
			}
			wantCount := before.AnalyzeCount
			if boundary == "publishFinal" {
				wantCount++
			}
			if fresh.AnalyzeCount != wantCount {
				t.Fatal("local completion fence changed", fresh.AnalyzeCount, wantCount)
			}
			for key, fact := range old {
				if backing.Extended[key] != fact {
					t.Fatal("saved immutable backing changed")
				}
			}
		})
	}
}

func TestAnalyzeProgressDefinitionCounts(t *testing.T) {
	t.Run("zero_inherited_retained_local", analyzeForeignRetainedCounts)
	e, s := analyzeDefinitionFixture(t)
	events, err := analyzeTrace(t, e, s, "ANALYZE ONLY ap(a,b)", nil)
	if err != nil {
		t.Fatal(err)
	}
	analyzeRequireEvent(t, events, "fetched3 eligible2 local", func(f analyzeBoundary) bool {
		return f.Phase == "finalizing analyze" && f.ChildTablesTotal == 0 && f.ExtendedStatsTotal == 3 && f.ExtendedStatsComputed == 2
	})
	analyzeRequireEvent(t, events, "second pass retains old2 with total3", func(f analyzeBoundary) bool {
		return f.Phase == "computing extended statistics" && f.ChildTablesDone == 2 && f.ExtendedStatsTotal == 3 && f.ExtendedStatsComputed == 2 && strings.Contains(f.stack, "buildExtended")
	})
	analyzeRequireEvent(t, events, "second pass first accepted whole definition writes1", func(f analyzeBoundary) bool {
		return f.Phase == "computing extended statistics" && f.ChildTablesDone == 2 && f.ExtendedStatsComputed == 1 && strings.Contains(f.stack, "buildExtended")
	})
	analyzeRequireEvent(t, events, "second pass completes2 definitions not kinds", func(f analyzeBoundary) bool {
		return f.Phase == "finalizing analyze" && f.ChildTablesDone == 2 && f.ExtendedStatsComputed == 2
	})
	t.Run("fetched_skip_only", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE TABLE ap(a INT,b INT)", "INSERT INTO ap VALUES(1,2),(2,3)", "CREATE STATISTICS ap_skip(ndistinct) ON a,b FROM ap", "ANALYZE ap"} {
			enginetest.SessionSQL(t, s, q)
		}
		ref, _ := vacuumStore(t, e, "ap")
		old := e.Storage.RelationMetadata[ref.Address].Statistics
		events, err := analyzeTrace(t, e, s, "ANALYZE ap(a)", nil)
		if err != nil {
			t.Fatal(err)
		}
		analyzeRequireEvent(t, events, "skip-only fetched inventory", func(f analyzeBoundary) bool {
			return f.Phase == "finalizing analyze" && f.ExtendedStatsTotal == 1 && f.ExtendedStatsComputed == 0
		})
		fresh := e.Storage.RelationMetadata[ref.Address].Statistics
		for key, fact := range old.Extended {
			if fresh.Extended[key] != fact {
				t.Fatal("eligibility skip rebuilt old backing")
			}
		}
	})
}
