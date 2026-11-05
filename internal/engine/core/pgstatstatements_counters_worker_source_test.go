package core_test

import (
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

// Real admitted source/cache owners distinguish private worker outcomes from
// shared-frame writes, delayed cache touches and premature capture-pin release.
func TestPgStatStatementsCountersWorkerSource(t *testing.T) {
	for _, counts := range []string{"on", "off"} {
		t.Run(counts, func(t *testing.T) {
			e := bufferUnitEngine(t)
			s := enginetest.StartSession(t, e, nil)
			observer := enginetest.StartSession(t, e, nil)
			pgssOwnerExec(t, e, observer, "SET pg_stat_statements.track='none'")
			for _, q := range []string{"SET pg_stat_statements.track='none'", "CREATE TABLE counter_parallel_source(k integer)", "INSERT INTO counter_parallel_source VALUES(1),(2)", "SET track_counts=" + counts, "SET pg_stat_statements.track_utility=off", "SET pg_stat_statements.track='top'"} {
				pgssOwnerExec(t, e, s, q)
			}
			portal, command := pgssCounterPortal(t, e, s, "SELECT k FROM counter_parallel_source AS counter_parallel_owner")
			producer := core.PortalProducer(portal)
			source := producer.Source
			lease := source.ReadLease
			parallel, ok := lease.(exec.ParallelSourceReadLease)
			if !ok {
				t.Fatal("actual source lacks worker outcome owner")
			}
			// Probe from the real projection evaluator, not the presentation
			// sink, which deliberately runs with detached producer authority.
			probe := func(scope *core.StatementScope) error {
					before := core.PortalStatementCounters(portal)
					if before != (statistics.StatementCounters{SharedRead: 1}) {
						t.Fatal("initial genuine source Open", before)
					}
					// Retire the initial request's pin/residency to expose the
					// independent cold/warm outcomes of the two worker Opens.
					retired := parallel.CaptureWorker()
					if retired == nil {
						t.Fatal("live coordinator admission rejected")
					}
					lease.Close()
					retired.Open()
					retired.Close()
					parallel.MergeWorker(retired)
					evictionUnitResult(t, evictionUnitAction(t, e, observer, functions.EvictAll, 0), 1, 0)
					workers := [2]exec.SourceReadLease{parallel.CaptureWorker(), parallel.CaptureWorker()}
					unused, invalid := parallel.CaptureWorker(), parallel.CaptureWorker()
					for _, worker := range []exec.SourceReadLease{workers[0], workers[1], unused, invalid} {
						if worker == nil {
							t.Fatal("live coordinator admission rejected")
						}
					}
					e.Statistics.Reset(time.Now()) // Captured cumulative generation is obsolete.
					// Replace actual storage with an incompatible boundary, not
					// an invented invalid result or output-based request count.
					e.Mu.Lock()
					stored := e.Storage.Relations[source.Ref.Address]
					successor := *stored
					successor.TruncateBoundary++
					e.Storage.Relations[source.Ref.Address] = &successor
					e.Mu.Unlock()
					invalid.Open()
					e.Mu.Lock()
					e.Storage.Relations[source.Ref.Address] = stored
					e.Mu.Unlock()
					// Workers run while real nested lexical scratch is active.
					// They must not consult or change that child/ancestor chain.
					restore := scope.ActivateSQLRoutine(nil, e.CatalogRead())
					child, childErr := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
					if child.Rows != nil {
						closeErr := child.Rows.Close()
						if childErr == nil {
							childErr = closeErr
						}
					}
					childBefore := core.ScopeStatementCounters(scope)
					done := make(chan struct{}, len(workers))
					for _, worker := range workers {
						go func(worker exec.SourceReadLease) {
							worker.Open()
							worker.Close()
							done <- struct{}{}
						}(worker)
					}
					for range workers {
						<-done
					}
					childAfter := core.ScopeStatementCounters(scope)
					childErr = restore(childErr)
					if childErr != nil {
						return childErr
					}
					if childAfter != childBefore || core.PortalStatementCounters(portal) != before {
						t.Fatal("worker touched lexical frame before coordinator merge", childBefore, childAfter)
					}
					// Actual cache requests already happened, although neither
					// private outcome has been delivered to the pgss sink yet.
					evictionUnitResult(t, evictionUnitAction(t, e, observer, functions.EvictAll, 0), 0, 1)
					for _, worker := range []exec.SourceReadLease{unused, invalid, workers[0], workers[1]} {
						parallel.MergeWorker(worker)
						parallel.MergeWorker(worker)
						worker.Open() // A drained outcome has no remaining authority.
					}
					want := before
					want.SharedRead++
					want.SharedHit++
					if got := core.PortalStatementCounters(portal); got != want {
						t.Fatal("cold/warm private outcomes or once-only merge", got, want)
					}
					return nil
			}
			var activeScope *core.StatementScope
			originalExpression := producer.Root.Expressions[0]
			producer.Root.Expressions[0] = enginetest.QueryProbe(originalExpression.ResultUsage(), func(ctx *exec.Context) (sqltype.Value, error) {
				if err := probe(activeScope); err != nil {
					return sqltype.Value{}, err
				}
				return originalExpression.Eval(ctx)
			})
			command(func(scope *core.StatementScope) error {
				activeScope = scope
				_, err := scope.ExecuteProtocolPortal("counter_portal", 1, func([]session.ProtocolField) error {
					if parallel.CaptureWorker() != nil {
						t.Fatal("presentation sink admitted a detached worker")
					}
					return nil
				}, nil, nil, core.CopyProtocolHooks{})
				return err
			})
			if parallel.CaptureWorker() != nil {
				t.Fatal("detached source admitted another worker")
			}
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("counter_portal") })
			lease.Close()
			lease.Close()
			evictionUnitResult(t, evictionUnitAction(t, e, observer, functions.EvictAll, 0), 1, 0)
			entry := pgssPhaseEntry(t, e, "counter_parallel_owner")
			if entry.Execution.Count != 1 || entry.Counters.SharedRead != 2 || entry.Counters.SharedHit != 1 {
				t.Fatal("coordinator terminal lost actual worker source work", entry)
			}
			if parallel.CaptureWorker() != nil {
				t.Fatal("completed source retained worker admission")
			}
		})
	}
}
