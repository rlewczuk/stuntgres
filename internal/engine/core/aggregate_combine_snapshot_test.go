package core_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// Only the outer physical query uses this observer. Actual SQL support execution
// and lexical scratch still belong to the real statement host and its gate.
type aggregateSnapshotHost struct {
	*core.StatementHost
	active             atomic.Bool
	reads, activeReads atomic.Int32
	restored           chan struct{}
	restoreOnce        sync.Once
}

func (h *aggregateSnapshotHost) CTEInvocation() *exec.CTEInvocation {
	h.reads.Add(1)
	if h.active.Load() {
		h.activeReads.Add(1)
	}
	return h.StatementHost.CTEInvocation()
}

func (h *aggregateSnapshotHost) InvokeAggregateSQLRoutine(ctx context.Context, d *catalog.Procedure, frame *proc.Frame, usage sqltype.Usage, owner *catalog.Procedure, collation sqltype.CollationRef) (sqltype.Value, error) {
	value, err := h.StatementHost.InvokeAggregateSQLRoutine(ctx, d, frame, usage, owner, collation)
	h.restoreOnce.Do(func() { close(h.restored) })
	return value, err
}

// Hold the second real worker's state initializer until its sibling is inside
// actual pg_sleep/SQL support. Repeated Evaluation during initialization must
// retain the captured nil CTE both before and after that support restores scratch.
type aggregateSnapshotInitializer struct {
	*exec.TransitionAggregate
	host                          *aggregateSnapshotHost
	deadline                      context.Context
	allow, initialized, rechecked chan struct{}
	inits                         atomic.Int32
}

func (p *aggregateSnapshotInitializer) InitWithContext(ctx context.Context, evaluation expr.Context) (any, error) {
	if p.inits.Add(1) == 2 {
		select {
		case <-p.allow:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		state := evaluation.(*exec.Context).Query
		for range 3 {
			state.Evaluation(ctx)
		}
		close(p.initialized)
		select {
		case <-p.host.restored:
		case <-p.deadline.Done():
			return nil, p.deadline.Err()
		}
		for range 3 {
			state.Evaluation(ctx)
		}
		close(p.rechecked)
	}
	return p.TransitionAggregate.InitWithContext(ctx, evaluation)
}

func aggregateCombineWorkerInitializationSnapshot(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE FUNCTION ac_snapshot_step(bigint,integer) RETURNS bigint LANGUAGE SQL AS $$SELECT coalesce($1,0)+$2+CASE WHEN pg_sleep('Infinity'::float8) IS NULL THEN 0 ELSE 0 END$$",
		"CREATE AGGREGATE ac_snapshot(integer) (SFUNC=ac_snapshot_step,STYPE=bigint,COMBINEFUNC=pg_catalog.int8pl,PARALLEL=SAFE)",
		"CREATE TABLE ac_snapshot_input(x integer)",
		"INSERT INTO ac_snapshot_input VALUES(1),(2),(3),(4)",
	} {
		enginetest.Run(t, e, sql)
	}
	captured := enginetest.Run(t, e, "SELECT * FROM ac_snapshot_input").Rows.(*exec.FinalRows)
	source, err := exec.CaptureSource(context.Background(), captured.Source.View, captured.Source.Ref)
	captured.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Close)
	op, finish, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(finish)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	})
	const sql = "SELECT ac_snapshot(x) AS v FROM ac_snapshot_input"
	raw := enginetest.Statement(t, sql)
	if err := scope.Gate(raw); err != nil {
		t.Fatal(err)
	}
	if err := scope.AcquireRelation(raw, true); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var query *plan.Query
	if err == nil {
		binding, failure := core.Describe(op.Context(), e.AnalysisHost(op), raw, auth, source.Ref, scope.Input, nil, nil)
		err = failure
		query = binding.Query
		scope.RoutineAuth, scope.RoutineDependencies = auth, binding.Dependencies
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	host := &aggregateSnapshotHost{StatementHost: core.HostOf(scope).(*core.StatementHost), restored: make(chan struct{})}
	if host.CTEInvocation() != nil {
		t.Fatal("fixture must exercise an actually captured nil CTE owner")
	}
	source.Host = host
	node, eligible := exec.ParallelAggregatePlan(query.Root, source)
	if !eligible {
		t.Fatal("real scalar partial query was disabled")
	}
	deadline, end := context.WithTimeout(context.Background(), 5*time.Second)
	defer end()
	ctx, cancel := context.WithCancel(deadline)
	defer cancel()
	probe := &aggregateSnapshotInitializer{TransitionAggregate: node.Calls[0].Provider.(*exec.TransitionAggregate), host: host, deadline: deadline, allow: make(chan struct{}), initialized: make(chan struct{}), rechecked: make(chan struct{})}
	node.Calls[0].Provider = probe
	type result struct {
		rows  exec.RowResult
		count int
		err   error
	}
	done := make(chan result, 1)
	go func() {
		rows, count, err := exec.FinalizeParallelAggregate(ctx, query.Root, node, source, nil, 2)
		done <- result{rows, count, err}
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case result := <-done:
				if result.rows != nil {
					result.rows.Close()
				}
			case <-deadline.Done():
				t.Error("actual SQL support workers did not join after cancellation")
			}
		}
	}()
	// Observe the live provider and actual partition on the same complete stack;
	// neither a configured delay nor a callback stands in for real SQL entry.
	buffer := make([]byte, 64<<10)
	active := false
	for !active {
		select {
		case result := <-done:
			joined = true
			if result.rows != nil {
				result.rows.Close()
			}
			t.Fatal("partial query ended before real SQL admission", result.err)
		case <-deadline.Done():
			t.Fatal("real worker SQL sleep not reached", deadline.Err())
		default:
		}
		n := runtime.Stack(buffer, true)
		if n == len(buffer) {
			buffer = make([]byte, len(buffer)*2)
			continue
		}
		for _, stack := range strings.Split(string(buffer[:n]), "\n\n") {
			if cut := strings.Index(stack, "\ncreated by "); cut >= 0 {
				stack = stack[:cut]
			}
			if strings.Contains(stack, "datetimeSleepWaitRemaining") && strings.Contains(stack, "InvokeAggregateSQLRoutine") && strings.Contains(stack, "runAggregatePartition") {
				active = true
				break
			}
		}
		if !active {
			runtime.Gosched()
		}
	}
	host.active.Store(true)
	close(probe.allow)
	select {
	case <-probe.initialized:
	case <-deadline.Done():
		t.Fatal("sibling initialization did not overlap actual SQL support")
	}
	cancel()
	select {
	case <-probe.rechecked:
	case <-deadline.Done():
		t.Fatal("sibling initialization did not observe support restoration")
	}
	var outcome result
	select {
	case outcome = <-done:
		joined = true
	case <-deadline.Done():
		t.Fatal("partial workers failed to join")
	}
	host.active.Store(false)
	if outcome.rows != nil || outcome.count != 0 || !errors.Is(outcome.err, context.Canceled) {
		if outcome.rows != nil {
			outcome.rows.Close()
		}
		t.Fatal("canceled real partial query published output", outcome.count, outcome.err)
	}
	if probe.inits.Load() != 2 || host.activeReads.Load() != 0 || source.View != nil {
		t.Fatal("worker reread lexical host or retained source", probe.inits.Load(), host.activeReads.Load(), source.View)
	}
	if core.ScopeActiveSQLRoutineDepth(scope) != 0 || core.ScopeAggregateInvocationContext(scope) != nil {
		t.Fatal("real support lexical frame/gate context leaked")
	}
	// Ordinary/nested query states still inherit their current lexical owner.
	before := host.reads.Load()
	ordinary := &exec.State{Source: &exec.Source{Host: host}}
	ordinary.Evaluation(op.Context())
	if host.reads.Load() != before+1 || ordinary.Ctes != nil {
		t.Fatal("ordinary lexical inheritance changed")
	}
}
