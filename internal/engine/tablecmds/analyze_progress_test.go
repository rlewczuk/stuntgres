package tablecmds_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
)

type analyzeBoundary struct {
	pgcatalog.AnalyzeProgressFact
	caller, stack string
}

// Like analyzeInstallContext, observe real command/collector fences, never a
// progress setter. All execution is serial. Only authorityLocked holds Engine.Mu
// at the selected callers; detached traversal/collector callbacks acquire it.
type analyzeTraceContext struct {
	context.Context
	e       *core.Engine
	events  []analyzeBoundary
	onEvent func(analyzeBoundary)
}

func (c *analyzeTraceContext) Err() error {
	var pcs [40]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	first, more := frames.Next()
	name := first.Function
	selected := strings.Contains(name, "storage.CollectStatistics") ||
		strings.Contains(name, "storage.(*statisticsCollector).finish") ||
		strings.Contains(name, "tablecmds.(*analyzeSegment).eachNative") ||
		strings.Contains(name, "tablecmds.(*analyzeForeignSample).each") ||
		strings.Contains(name, "tablecmds.(*analyzeRun).collect.func1") ||
		strings.Contains(name, "tablecmds.(*analyzeForeignPass).buildExtended") ||
		strings.Contains(name, "tablecmds.(*analyzeRun).authorityLocked")
	if !selected {
		return c.Context.Err()
	}
	stack := name
	for more {
		frame, next := frames.Next()
		stack += "\n" + frame.Function
		more = next
	}
	held := strings.Contains(name, "tablecmds.(*analyzeRun).authorityLocked")
	if !held {
		c.e.Mu.Lock()
	}
	auth := enginetest.CatalogAuthorization(c.e, c.e.Catalog.Current())
	rt := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: c.e, Auth: auth})
	var event *analyzeBoundary
	err := rt.VisitAnalyzeProgress(context.Background(), func(f pgcatalog.AnalyzeProgressFact) error {
		v := analyzeBoundary{AnalyzeProgressFact: f, caller: name, stack: stack}
		event = &v
		return nil
	})
	if !held {
		c.e.Mu.Unlock()
	}
	if err != nil {
		panic(err)
	}
	if event != nil {
		c.events = append(c.events, *event)
		if c.onEvent != nil {
			c.onEvent(*event)
		}
	}
	return c.Context.Err()
}

func analyzeTrace(t *testing.T, e *core.Engine, s *session.Session, query string, callback func(analyzeBoundary, context.CancelFunc)) ([]analyzeBoundary, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	trace := &analyzeTraceContext{Context: op.Context(), e: e}
	if callback != nil {
		trace.onEvent = func(f analyzeBoundary) { callback(f, cancel) }
	}
	op.Ctx = trace
	result, err := e.Execute(op, enginetest.Statement(t, query), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	e.Mu.Lock()
	auth := enginetest.CatalogAuthorization(e, e.Catalog.Current())
	remaining := 0
	captureErr := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth}).VisitAnalyzeProgress(context.Background(), func(pgcatalog.AnalyzeProgressFact) error { remaining++; return nil })
	e.Mu.Unlock()
	if captureErr != nil || remaining != 0 {
		t.Fatal("target exit retained membership", remaining, captureErr)
	}
	return trace.events, err
}

func analyzeRequireEvent(t *testing.T, events []analyzeBoundary, description string, match func(analyzeBoundary) bool) analyzeBoundary {
	t.Helper()
	for _, event := range events {
		if match(event) {
			return event
		}
	}
	t.Fatalf("actual boundary not observed: %s (%d observations)", description, len(events))
	return analyzeBoundary{}
}

func TestAnalyzeProgressSegments(t *testing.T) {
	t.Run("mixed_actual_visits", analyzeForeignSegments)
	for _, tc := range []struct {
		name   string
		setup  []string
		visits int64
		last   string
	}{
		{"ordinary", []string{"CREATE TABLE ap(a INT)", "CREATE TABLE ac1() INHERITS(ap)", "CREATE TABLE ac2() INHERITS(ap)", "INSERT INTO ap VALUES(1)", "INSERT INTO ac1 VALUES(2)", "INSERT INTO ac2 VALUES(3)"}, 3, "ac2"},
		{"partition", []string{"CREATE TABLE ap(a INT) PARTITION BY RANGE(a)", "CREATE TABLE ai PARTITION OF ap FOR VALUES FROM(0) TO(10) PARTITION BY RANGE(a)", "CREATE TABLE ac1 PARTITION OF ai FOR VALUES FROM(0) TO(5)", "CREATE TABLE ac2 PARTITION OF ai FOR VALUES FROM(5) TO(10)", "INSERT INTO ap VALUES(1),(7)"}, 2, "ac2"},
		{"diamond", []string{"CREATE TABLE ap(a INT)", "CREATE TABLE ac1() INHERITS(ap)", "CREATE TABLE ac2() INHERITS(ap)", "CREATE TABLE ad() INHERITS(ac1,ac2)", "INSERT INTO ad VALUES(1)"}, 4, "ac2"},
		{"zero", []string{"CREATE TABLE ap(a INT)", "CREATE TABLE ac1() INHERITS(ap)", "CREATE TABLE ac2() INHERITS(ap)"}, 3, "ac2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range tc.setup {
				enginetest.SessionSQL(t, s, q)
			}
			last := enginetest.CatalogRef(t, e, e.CatalogRead(), tc.last).Address.ObjectID
			events, err := analyzeTrace(t, e, s, "ANALYZE ONLY ap", nil)
			if err != nil {
				t.Fatal(err)
			}
			analyzeRequireEvent(t, events, "all retained visits genuinely complete", func(f analyzeBoundary) bool {
				return f.Phase == "finalizing analyze" && f.ChildTablesTotal == tc.visits && f.ChildTablesDone == tc.visits && f.CurrentChild == last
			})
			for _, f := range events {
				if f.ChildTablesDone > f.ChildTablesTotal || f.SampleBlocksScanned > f.SampleBlocksTotal || f.DelayTime != 0 {
					t.Fatal("invalid segment estimate", f)
				}
			}
		})
	}
	for _, n := range []int{0, 1, 127, 128, 129} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE ap(a INT)")
			if n != 0 {
				enginetest.SessionSQL(t, s, fmt.Sprintf("INSERT INTO ap SELECT i FROM generate_series(1,%d) i", n))
			}
			events, err := analyzeTrace(t, e, s, "ANALYZE ap", nil)
			if err != nil {
				t.Fatal(err)
			}
			want := int64(n / 128)
			if n%128 != 0 {
				want++
			}
			analyzeRequireEvent(t, events, "completed actual record blocks", func(f analyzeBoundary) bool {
				return f.Phase == "finalizing analyze" && f.SampleBlocksTotal == want && f.SampleBlocksScanned == want
			})
			if n == 129 {
				analyzeRequireEvent(t, events, "prefix128 of129", func(f analyzeBoundary) bool {
					return f.SampleBlocksTotal == 2 && f.SampleBlocksScanned == 1 && f.Phase == "acquiring sample rows"
				})
			}
		})
	}
	t.Run("failed_visit", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE TABLE ap(a INT)", "CREATE TABLE ac() INHERITS(ap)", "INSERT INTO ap VALUES(1)", "INSERT INTO ac VALUES(2)", "ANALYZE ONLY ap"} {
			enginetest.SessionSQL(t, s, q)
		}
		ref, store := vacuumStore(t, e, "ap")
		old := e.Storage.RelationMetadata[ref.Address]
		backing := old.Statistics
		count := old.AnalyzeCount
		child := enginetest.CatalogRef(t, e, e.CatalogRead(), "ac").Address.ObjectID
		events, err := analyzeTrace(t, e, s, "ANALYZE ONLY ap", func(f analyzeBoundary, cancel context.CancelFunc) {
			if f.CurrentChild == child && f.ChildTablesDone == 1 {
				cancel()
			}
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatal("visit cancellation missed", err)
		}
		analyzeRequireEvent(t, events, "child reset before traversal", func(f analyzeBoundary) bool {
			return f.CurrentChild == child && f.ChildTablesDone == 1 && f.SampleBlocksTotal == 0 && f.SampleBlocksScanned == 0
		})
		fresh := e.Storage.RelationMetadata[ref.Address]
		if fresh.Statistics != backing || fresh.AnalyzeCount != count+1 || !store.Maintenance.Scanned {
			t.Fatal("failed inherited visit lost local completion or published unfinished backing")
		}
		for _, f := range events {
			if f.CurrentChild == child && f.ChildTablesDone != 1 {
				t.Fatal("failed visit counted done", f)
			}
		}
	})
}
