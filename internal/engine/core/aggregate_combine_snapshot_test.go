package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
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
	held               atomic.Bool
	parked, release    chan struct{}
	releaseOnce        sync.Once
}

func (h *aggregateSnapshotHost) Environments() (sqltype.InputEnvironment, sqltype.OutputEnvironment) {
	if h.active.Load() {
		h.activeReads.Add(1)
	}
	return h.StatementHost.Environments()
}

func (h *aggregateSnapshotHost) FormatRegclass(id uint32) (string, error) {
	if h.active.Load() {
		h.activeReads.Add(1)
	}
	return h.StatementHost.FormatRegclass(id)
}

func (h *aggregateSnapshotHost) CTEInvocation() *exec.CTEInvocation {
	h.reads.Add(1)
	if h.active.Load() {
		h.activeReads.Add(1)
	}
	return h.StatementHost.CTEInvocation()
}

func (h *aggregateSnapshotHost) InvokeAggregateRoutine(ctx context.Context, d *catalog.Procedure, frame *proc.Frame, usage sqltype.Usage, owner *catalog.Procedure, collation sqltype.CollationRef) (sqltype.Value, error) {
	value, err := h.StatementHost.InvokeAggregateRoutine(&aggregateSnapshotContext{Context: ctx, host: h}, d, frame, usage, owner, collation)
	if h.active.Load() {
		h.restoreOnce.Do(func() { close(h.restored) })
	}
	return value, err
}

// This observes scheduling at the real provider's cancellation boundary. It
// neither replaces SQL support nor supplies a result. Channels, not duration,
// keep the actual runner and lexical scratch active while its sibling evaluates.
type aggregateSnapshotContext struct {
	context.Context
	host *aggregateSnapshotHost
}

func (c *aggregateSnapshotContext) Err() error {
	if err := c.Context.Err(); err != nil {
		return err
	}
	if !c.host.held.Load() {
		var pcs [128]uintptr
		n := runtime.Callers(2, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		sleep, support, partition := false, false, false
		for {
			frame, more := frames.Next()
			sleep = sleep || strings.Contains(frame.Function, "datetimeSleepWaitRemaining")
			support = support || strings.Contains(frame.Function, "InvokeAggregateRoutine")
			partition = partition || strings.Contains(frame.Function, "runAggregatePartition")
			if !more {
				break
			}
		}
		if sleep && support && partition && c.host.held.CompareAndSwap(false, true) {
			close(c.host.parked)
			select {
			case <-c.host.release:
			case <-c.Context.Done():
			}
		}
	}
	return c.Context.Err()
}

func (h *aggregateSnapshotHost) releaseSupport() {
	h.releaseOnce.Do(func() { close(h.release) })
}

// Hold the second real worker's state initializer until its sibling is inside
// actual pg_sleep/SQL support. Repeated Evaluation during initialization must
// retain the captured nil CTE both before and after that support restores scratch.
type aggregateSnapshotInitializer struct {
	*exec.TransitionAggregate
	acl                           *exec.TransitionAggregate
	host                          *aggregateSnapshotHost
	deadline                      context.Context
	allow, initialized, rechecked chan struct{}
	inits                         atomic.Int32
	capturesMu                    sync.Mutex
	captures                      []*exec.EvaluationCapture
	states                        []*exec.State
	pins                          []any
	finals, combines              atomic.Int32
}

func (p *aggregateSnapshotInitializer) InitWithContext(ctx context.Context, evaluation expr.Context) (any, error) {
	c := evaluation.(*exec.Context)
	if exec.InParallelWorker(ctx) {
		if !exec.InParallelOperation(ctx) || c.ClosedEvaluation == nil || c.Presentation() == p.host {
			return nil, fmt.Errorf("worker lost closed evaluation ownership")
		}
		p.capturesMu.Lock()
		p.captures = append(p.captures, c.ClosedEvaluation)
		p.states = append(p.states, c.Query)
		p.pins = append(p.pins, c.Query.Source.View)
		p.capturesMu.Unlock()
	}
	if p.inits.Add(1) == 2 {
		select {
		case <-p.allow:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		state := evaluation.(*exec.Context).Query
		for range 3 {
			local := state.Evaluation(ctx)
			if err := exerciseParallelPresentation(&local, uint32(p.Support.Address().ObjectID), p.acl); err != nil {
				return nil, err
			}
		}
		close(p.initialized)
		select {
		case <-p.host.restored:
		case <-p.deadline.Done():
			return nil, p.deadline.Err()
		}
		for range 3 {
			local := state.Evaluation(ctx)
			// Cleanup after cancellation still permits immutable presentation.
			local.Context = p.deadline
			if err := exerciseParallelPresentation(&local, uint32(p.Support.Address().ObjectID), p.acl); err != nil {
				return nil, err
			}
		}
		p.host.active.Store(false) // Subsequent leader finalization is synchronous.
		close(p.rechecked)
	}
	return p.TransitionAggregate.InitWithContext(ctx, evaluation)
}

func (p *aggregateSnapshotInitializer) Combine(ctx context.Context, destination, incoming any, evaluation expr.Context) error {
	if !exec.InParallelOperation(ctx) || exec.InParallelWorker(ctx) {
		return fmt.Errorf("leader combine lost operation identity")
	}
	p.combines.Add(1)
	return p.TransitionAggregate.Combine(ctx, destination, incoming, evaluation)
}

func (p *aggregateSnapshotInitializer) FinalizeWithContext(ctx context.Context, state any, direct []sqltype.Value, evaluation expr.Context) (sqltype.Value, error) {
	if !exec.InParallelOperation(ctx) || exec.InParallelWorker(ctx) {
		return sqltype.Value{}, fmt.Errorf("leader final lost operation identity")
	}
	p.finals.Add(1)
	return p.TransitionAggregate.FinalizeWithContext(ctx, state, direct, evaluation)
}

func aggregateCombineWorkerInitializationSnapshot(t *testing.T) {
	aggregateParallelInvocationOutcome(t, "cancel")
}

func TestAggregateParallelInvocationOwnership(t *testing.T) {
	for _, outcome := range []string{"success", "error", "cancel", "definer_cancel", "definer_child_cancel"} {
		t.Run(outcome, func(t *testing.T) { aggregateParallelInvocationOutcome(t, outcome) })
	}
	t.Run("nested_admission", aggregateParallelNestedAdmission)
}

func aggregateParallelInvocationOutcome(t *testing.T, outcomeKind string) {
	cancelRequest := outcomeKind == "definer_cancel"
	definer := cancelRequest || outcomeKind == "definer_child_cancel"
	environment := ""
	if definer {
		outcomeKind = "cancel"
		environment = " SECURITY DEFINER"
	}
	sleep := "0.2"
	if outcomeKind == "cancel" {
		sleep = "'Infinity'::float8"
	}
	// Reach the real provider before the large nested scan: the scheduling
	// observer walks cancellation stacks only until that first provider parks.
	// The nested scan then runs under the same actual SQL invocation ownership,
	// without adding a stack walk to every input row's cancellation checks.
	body := "SELECT pg_sleep(CASE WHEN $2=0 THEN 0 ELSE " + sleep + " END); SELECT sum(y) FROM ac_snapshot_nested; SELECT CASE WHEN $2 IS NULL THEN $1 ELSE coalesce($1,0)+$2::bigint*$2::bigint+ac_snapshot_identity(0) END"
	if outcomeKind == "error" {
		body += "/CASE WHEN $2=0 THEN 1 ELSE 0 END"
	}
	e := engine.New()
	for _, sql := range []string{
		"CREATE TABLE ac_snapshot_input(x integer)",
		"INSERT INTO ac_snapshot_input VALUES(0),(1),(2),(3),(4)",
		"CREATE TABLE ac_snapshot_nested(y integer,padding text)",
		"INSERT INTO ac_snapshot_nested SELECT generate_series(1,16384),repeat('x',1024)",
		"CREATE FUNCTION ac_snapshot_identity(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1$$",
		"CREATE FUNCTION ac_snapshot_step(bigint,integer) RETURNS bigint LANGUAGE SQL" + environment + " AS $$" + body + "$$",
		"CREATE FUNCTION ac_snapshot_merge(bigint,bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT sum(y) FROM ac_snapshot_nested; SELECT coalesce($1,0)+coalesce($2,0)+ac_snapshot_identity(0)$$",
		"CREATE FUNCTION ac_snapshot_final(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT sum(y) FROM ac_snapshot_nested; SELECT $1+ac_snapshot_identity(0)$$",
		"CREATE AGGREGATE ac_snapshot(integer) (SFUNC=ac_snapshot_step,STYPE=bigint,COMBINEFUNC=ac_snapshot_merge,FINALFUNC=ac_snapshot_final,PARALLEL=SAFE)",
		"CREATE FUNCTION ac_snapshot_acl_step(aclitem,integer) RETURNS aclitem LANGUAGE SQL AS $$SELECT $1$$",
		"CREATE FUNCTION ac_snapshot_acl_merge(aclitem,aclitem) RETURNS aclitem LANGUAGE SQL AS $$SELECT coalesce($1,$2)$$",
		"CREATE AGGREGATE ac_snapshot_acl(integer) (SFUNC=ac_snapshot_acl_step,STYPE=aclitem,COMBINEFUNC=ac_snapshot_acl_merge,INITCOND='=r',PARALLEL=SAFE)",
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
	operationContext, cancelOperation := context.WithCancel(context.Background())
	t.Cleanup(cancelOperation)
	op, finish, err := enginetest.Operation(e, operationContext)
	if err != nil {
		t.Fatal(err)
	}
	op.ParallelWorkers = 2 // Nested physical SELECT would otherwise qualify for eager fanout.
	t.Cleanup(finish)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := scope.Finish(nil); err != nil && !(cancelRequest && errors.Is(err, context.Canceled)) {
			t.Error(err)
		}
	})
	sql := "SELECT ac_snapshot(x) AS v FROM ac_snapshot_input"
	if definer {
		sql += " WHERE x>0"
	}
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
		if err == nil {
			err = e.CaptureCommentSnapshotLocked(scope)
		}
	}
	if err == nil {
		err = e.CaptureElementInput(auth, op, &scope.Input)
		scope.Input.XMLInput = session.XMLInput{Op: op}
		scope.Input.FTS = session.FTSInput{Op: op}
		scope.Input.ACLItem = e.CaptureACLItemInput(auth, op)
	}
	var recoveryQuery *plan.Query
	if err == nil {
		recovery := enginetest.Statement(t, "SELECT ac_snapshot(x) AS v FROM ac_snapshot_input WHERE x=0")
		binding, failure := core.Describe(op.Context(), e.AnalysisHost(op), recovery, auth, source.Ref, scope.Input, nil, nil)
		err = failure
		recoveryQuery = binding.Query
	}
	var aclQuery *plan.Query
	if err == nil {
		rawACL := enginetest.Statement(t, "SELECT ac_snapshot_acl(x) AS v FROM ac_snapshot_input")
		binding, failure := core.Describe(op.Context(), e.AnalysisHost(op), rawACL, auth, source.Ref, scope.Input, nil, nil)
		err = failure
		aclQuery = binding.Query
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	host := &aggregateSnapshotHost{StatementHost: core.HostOf(scope).(*core.StatementHost), restored: make(chan struct{}), parked: make(chan struct{}), release: make(chan struct{})}
	if host.CTEInvocation() != nil {
		t.Fatal("fixture must exercise an actually captured nil CTE owner")
	}
	source.Host = host
	node, eligible := exec.ParallelAggregatePlan(query.Root, source)
	if !eligible {
		t.Fatal("real scalar partial query was disabled")
	}
	aclNode, eligible := exec.ParallelAggregatePlan(aclQuery.Root, source)
	if !eligible {
		t.Fatal("real ACL-state partial query was disabled")
	}
	acl := aclNode.Calls[0].Provider.(*exec.TransitionAggregate)
	if !acl.HasInitial || acl.Initial != "=r" || !acl.PartialCapable() {
		t.Fatal("ACL initializer did not retain selected partial seed")
	}
	recoverySources := make([]*exec.Source, 2)
	for i := range recoverySources {
		recoverySources[i], err = exec.CaptureSource(op.Context(), source.View, source.Ref)
		if err != nil {
			t.Fatal(err)
		}
		recoverySources[i].Host = host
		t.Cleanup(recoverySources[i].Close)
	}
	assertRestored := core.CaptureAggregateScratchOwnership(scope)
	definition := node.Calls[0].Provider.(*exec.TransitionAggregate).Support.SQL
	formals := append([]sqltype.Usage(nil), definition.Inputs...)
	formalsOwner := &definition.Inputs[0]
	deadline, end := context.WithTimeout(context.Background(), 15*time.Second)
	defer end()
	ctx, cancel := context.WithCancel(deadline)
	defer cancel()
	probe := &aggregateSnapshotInitializer{TransitionAggregate: node.Calls[0].Provider.(*exec.TransitionAggregate), acl: acl, host: host, deadline: deadline, allow: make(chan struct{}), initialized: make(chan struct{}), rechecked: make(chan struct{})}
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
		host.releaseSupport()
		if !joined {
			// Execution may already have exhausted its deadline. Cancellation
			// and observer release still get their own bounded join interval.
			joinDeadline := time.NewTimer(5 * time.Second)
			defer joinDeadline.Stop()
			select {
			case result := <-done:
				if result.rows != nil {
					result.rows.Close()
				}
			case <-joinDeadline.C:
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
			if strings.Contains(stack, "datetimeSleepWaitRemaining") && strings.Contains(stack, "InvokeAggregateRoutine") && strings.Contains(stack, "runAggregatePartition") {
				active = true
				break
			}
		}
		if !active {
			runtime.Gosched()
		}
	}
	host.active.Store(true)
	select {
	case <-host.parked:
	case <-deadline.Done():
		t.Fatal("actual support scheduling boundary was not parked")
	}
	close(probe.allow)
	select {
	case <-probe.initialized:
	case <-deadline.Done():
		t.Fatal("sibling initialization did not overlap actual SQL support")
	}
	if len(op.Notices) != 0 {
		t.Fatal("worker diagnostics escaped into the live operation before join", op.Notices)
	}
	if outcomeKind == "cancel" {
		if cancelRequest {
			cancelOperation()
		}
		cancel()
	}
	host.releaseSupport()
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
	if outcomeKind == "success" {
		if outcomeRows := outcome.rows; outcomeRows == nil || outcome.count != 1 || outcome.err != nil {
			t.Fatal("successful real partial query", outcome.count, outcome.err)
		} else {
			values := make([]sqltype.Value, 1)
			present, err := outcomeRows.Next(deadline, values)
			if err != nil || !present {
				t.Fatal(present, err)
			}
			n, err := values[0].Integer()
			if err != nil || n != 30 || values[0].Type().ID() != sqltype.Int8 {
				t.Fatal(n, err)
			}
			outcomeRows.Close()
		}
	} else if outcome.rows != nil || outcome.count != 0 {
		if outcome.rows != nil {
			outcome.rows.Close()
		}
		t.Fatal("failed real partial query published output", outcome.count, outcome.err)
	} else if outcomeKind == "cancel" && !errors.Is(outcome.err, context.Canceled) {
		t.Fatal(outcome.err)
	} else if outcomeKind == "error" {
		var sqlErr *sqlstate.Error
		if !errors.As(outcome.err, &sqlErr) || sqlErr.Code != "22012" || sqlErr.Message != "division by zero" || sqlErr.Context != "SQL function \"ac_snapshot_step\" statement 3" || sqlErr.Severity != "ERROR" || sqlErr.SeverityUnlocalized != "ERROR" {
			t.Fatal(outcome.err)
		}
	}
	wantInits := int32(2)
	if outcomeKind == "success" {
		wantInits++
	} // Leader merge has its own state.
	if probe.inits.Load() != wantInits || host.activeReads.Load() != 0 || source.View != nil {
		t.Fatal("worker reread lexical host or retained source", probe.inits.Load(), host.activeReads.Load(), source.View)
	}
	if core.ScopeActiveSQLRoutineDepth(scope) != 0 || core.ScopeAggregateInvocationContext(scope) != nil {
		t.Fatal("real support lexical frame/gate context leaked")
	}
	if err := assertRestored(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(formals, definition.Inputs) || formalsOwner != &definition.Inputs[0] {
		t.Fatal("published definition formals/backing mutated")
	}
	if len(probe.captures) != 2 || probe.captures[0] == probe.captures[1] || probe.captures[0].Presentation == probe.captures[1].Presentation {
		t.Fatal("workers shared evaluator presentation/cache owners")
	}
	if err := core.CheckAggregatePresentationOwners(probe.captures[0], probe.captures[1]); err != nil {
		t.Fatal(err)
	}
	if probe.states[0] == probe.states[1] || probe.states[0].Source == probe.states[1].Source || probe.pins[0] == probe.pins[1] || probe.states[0].Source.View != nil || probe.states[1].Source.View != nil {
		t.Fatal("workers shared or retained state/source pins")
	}
	if outcomeKind == "success" && (probe.finals.Load() != 1 || probe.combines.Load() != 2) {
		t.Fatal("leader support invocation counts", probe.finals.Load(), probe.combines.Load())
	}
	if outcomeKind != "success" && (probe.finals.Load() != 0 || probe.combines.Load() != 0) {
		t.Fatal("failed workers reached leader support")
	}
	notices := op.DetachNotices()
	if len(notices) != 30 {
		t.Fatal("evaluator diagnostics lost/duplicated across join", len(notices), notices)
	}
	for i, notice := range notices {
		wantCode := []string{"01000", "00000", "42622", "0L000", "0L000"}[i%5]
		wantPosition := []string{"31", "37", "", "43", ""}[i%5]
		if notice.Code != wantCode || notice.Position != wantPosition {
			t.Fatal("evaluator diagnostic provenance/order", i, notice)
		}
		if i%5 >= 3 {
			wantContext := ""
			if i%5 == 4 {
				wantContext = "unnamed portal parameter $2 = '...'"
			}
			if notice.Severity != "WARNING" || notice.SeverityUnlocalized != "WARNING" || notice.Message != "defaulting grantor to user ID 10" || notice.Context != wantContext {
				t.Fatal("ACL initializer diagnostic provenance", i, notice)
			}
		}
	}
	for _, capture := range probe.captures {
		capture.FlushDiagnostics()
	}
	if residual := op.DetachNotices(); len(residual) != 0 {
		t.Fatal("joined evaluator diagnostics were delivered twice", residual)
	}
	// Ordinary/nested query states still inherit their current lexical owner.
	before := host.reads.Load()
	ordinary := &exec.State{Source: &exec.Source{Host: host}}
	ordinary.Evaluation(op.Context())
	if host.reads.Load() != before+1 || ordinary.Ctes != nil {
		t.Fatal("ordinary lexical inheritance changed")
	}
	local := ordinary.Evaluation(op.Context())
	mode := scope.Input.XMLMode
	scope.Input.XMLMode = sqltype.XMLDocument
	if local.FunctionInput().XMLMode != sqltype.XMLDocument {
		t.Fatal("ordinary live XML overlay was frozen")
	}
	scope.Input.XMLMode = mode
	if local.ClosedEvaluation != nil || exec.InParallelOperation(local.Context) || exec.InParallelWorker(local.Context) {
		t.Fatal("ordinary evaluation retained parallel ownership")
	}
	recoveryOp, recoveryScope := op, scope
	if cancelRequest {
		// A canceled request cannot be revived. Finish it, then reuse the same
		// physical session with a fresh operation and its ordinary authority.
		if err := scope.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		op.Done()
		recoveryOp, err = op.Session.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		recoveryScope, err = e.StartStatement(recoveryOp)
		if err != nil {
			recoveryOp.Done()
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := recoveryScope.Finish(nil); err != nil {
				t.Error(err)
			}
			recoveryOp.Done()
		})
		raw := enginetest.Statement(t, "SELECT ac_snapshot(x) AS v FROM ac_snapshot_input WHERE x=0")
		if err := recoveryScope.Gate(raw); err != nil {
			t.Fatal(err)
		}
		if err := recoveryScope.AcquireRelation(raw, true); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, failure := e.AuthorizationLocked(recoveryOp)
		if failure == nil {
			binding, bindErr := core.Describe(recoveryOp.Context(), e.AnalysisHost(recoveryOp), raw, auth, recoverySources[0].Ref, recoveryScope.Input, nil, nil)
			failure = bindErr
			if failure == nil {
				recoveryQuery = binding.Query
				recoveryScope.RoutineAuth, recoveryScope.RoutineDependencies = auth, binding.Dependencies
			}
		}
		e.Mu.Unlock()
		if failure != nil {
			t.Fatal(failure)
		}
		for _, source := range recoverySources {
			source.Host = core.HostOf(recoveryScope)
		}
		ordinary := (&exec.State{Source: recoverySources[0]}).Evaluation(recoveryOp.Context())
		if ordinary.ClosedEvaluation != nil || exec.InParallelOperation(ordinary.Context) || exec.InParallelWorker(ordinary.Context) || exec.WithinSQLInvocation(ordinary.Context) {
			t.Fatal("fresh operation inherited completed invocation")
		}
	}
	for i, reuseSource := range recoverySources {
		var rows exec.RowResult
		var count int
		var err error
		if i == 0 {
			rows, count, err = exec.Finalize(recoveryOp.Context(), recoveryQuery.Root, reuseSource, nil)
		} else {
			partial, admitted := exec.ParallelAggregatePlan(recoveryQuery.Root, reuseSource)
			if !admitted {
				t.Fatal("recovery lost real partial admission")
			}
			rows, count, err = exec.FinalizeParallelAggregate(recoveryOp.Context(), recoveryQuery.Root, partial, reuseSource, nil, 2)
		}
		if err != nil || rows == nil || count != 1 {
			t.Fatal("same-owner recovery", i, count, err)
		}
		values := make([]sqltype.Value, 1)
		present, err := rows.Next(recoveryOp.Context(), values)
		rows.Close()
		n, valueErr := values[0].Integer()
		if err != nil || !present || valueErr != nil || n != 0 {
			t.Fatal("same-owner recovery value", i, n, err, valueErr)
		}
		if core.ScopeActiveSQLRoutineDepth(recoveryScope) != 0 || core.ScopeAggregateInvocationContext(recoveryScope) != nil {
			t.Fatal("reuse retained invocation ownership")
		}
	}
}
