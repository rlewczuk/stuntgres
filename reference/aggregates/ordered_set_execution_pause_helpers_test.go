package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"runtime/pprof"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func orderedSetPauseUnadmitted(t *testing.T, ctx context.Context, pause *stuntgres.ExecutionPause) {
	t.Helper()
	pause.Resume()
	pause.Resume()
	err := pause.Wait(ctx)
	if err == nil || err.Error() != "stuntgres: execution pause resumed before admission" {
		t.Fatalf("noncomparing work admitted sort pause: %v", err)
	}
}

func orderedSetPauseUnadmittedBeforeLaunch(t *testing.T, ctx context.Context, pause *stuntgres.ExecutionPause) {
	t.Helper()
	observe, cancel := context.WithCancel(ctx)
	cancel()
	fixture.LexicalOrdinary(t, pause.Wait(observe), context.Canceled)
}

// All receives, including failure cleanup, share ONE terminal five-second join.
// Construct, defer close, then start: no launched SQL lacks its cleanup owner.
// Successful rows transfer to RunStep, which owns metadata, raw cells and close.
type orderedSetPauseWorker struct {
	ctx       context.Context
	cancel    context.CancelFunc
	pause     *stuntgres.ExecutionPause
	done      chan fixture.RelationOutcome
	joinOwner orderedSetCancellationJoin
	out       fixture.RelationOutcome
	joined    bool
	started   bool
	worker    string
}

func newOrderedSetPauseWorker(parent context.Context, pause *stuntgres.ExecutionPause) *orderedSetPauseWorker {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	return &orderedSetPauseWorker{ctx: ctx, cancel: cancel, pause: pause, done: make(chan fixture.RelationOutcome, 1)}
}

func (w *orderedSetPauseWorker) start(source fixture.Executor, q string, args []any) {
	w.started = true
	go pprof.Do(w.ctx, pprof.Labels(orderedSetWorkerLabel, w.worker), func(ctx context.Context) {
		rows, err := source.QueryContext(ctx, q, args...)
		w.done <- fixture.RelationOutcome{Rows: rows, Err: err}
	})
}

func (w *orderedSetPauseWorker) join(t *testing.T) {
	t.Helper()
	if w.joined || w.joinOwner.attempted || !w.started {
		return
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	w.out, w.joined = w.joinOwner.receive(w.done, timer.C)
	if !w.joined {
		t.Error("small ordered aggregate terminal join exceeded 5s")
	}
}

func (w *orderedSetPauseWorker) close(t *testing.T) {
	t.Helper()
	w.cancel()
	w.pause.Resume()
	w.join(t)
	if w.out.Rows != nil {
		if err := w.out.Rows.Close(); err != nil {
			t.Error("small ordered aggregate rows close:", err)
		}
	}
	if w.out.Stmt != nil {
		if err := w.out.Stmt.Close(); err != nil {
			t.Error("small ordered aggregate statement close:", err)
		}
	}
}

func (w *orderedSetPauseWorker) pending(t *testing.T) {
	t.Helper()
	if !w.joined {
		select {
		case w.out = <-w.done:
			w.joined = true
		default:
		}
	}
	if w.joined {
		t.Fatalf("sort hold did not retain unfinished request: error=%v rows=%t", w.out.Err, w.out.Rows != nil)
	}
	if err := w.ctx.Err(); err != nil {
		t.Fatal("sort hold without live request budget:", err)
	}
}

func (w *orderedSetPauseWorker) admission(t *testing.T) {
	t.Helper()
	observe, cancel := context.WithCancel(w.ctx)
	waiting := make(chan error, 1)
	// Observer cleanup is installed BEFORE starting the waiter, and completes
	// before the SQL cleanup owner, regardless of which boundary wins.
	defer func() {
		cancel()
		select {
		case <-waiting:
		case <-time.After(5 * time.Second):
			t.Error("sort pause observer join exceeded 5s")
		}
	}()
	go func() { waiting <- w.pause.Wait(observe) }()
	select {
	case err := <-waiting:
		// Put the terminal result back for the one observer-cleanup receiver.
		waiting <- err
		if err != nil {
			t.Fatal("sort pause admission:", err)
		}
		w.pending(t)
	case w.out = <-w.done:
		w.joined = true
		t.Fatalf("new sort-specific hold missing: small request completed without admission; error=%v rows=%t", w.out.Err, w.out.Rows != nil)
	case <-w.ctx.Done():
		t.Fatal("small sort admission budget:", w.ctx.Err())
	}
}

func (w *orderedSetPauseWorker) canceled(t *testing.T) {
	t.Helper()
	w.join(t)
	if !w.joined {
		t.Fatal("canceled small sort did not join")
	}
	if w.out.Rows != nil || w.out.Result != nil || w.out.Stmt != nil {
		t.Fatal("canceled small sort published output")
	}
	fixture.LexicalOrdinary(t, w.out.Err, context.Canceled)
}

func (w *orderedSetPauseWorker) profiles(t *testing.T, parent context.Context, count int) {
	t.Helper()
	var sample bytes.Buffer
	for i := range count {
		w.pending(t)
		sample.Reset()
		if err := pprof.Lookup("goroutine").WriteTo(&sample, 1); err != nil {
			t.Fatal(err)
		}
		if !orderedSetWorkerFrame(sample.Bytes(), w.worker, orderedSetSortFrame) {
			t.Fatalf("held small sort profile %d lacks exact owned live comparator", i+1)
		}
		w.pending(t)
		if parent.Err() != nil || w.ctx.Err() != nil {
			t.Fatal("small sort profile without live parent and child budgets")
		}
	}
}

// Interpose coordination only, never substitute a SQL result. The real cursor
// returns directly to RunStep on its caller goroutine, with its recorder context
// intact; no second query, result replay or goroutine-local assertions are used.
type orderedSetPauseExecutor struct {
	fixture.Executor
	T      *testing.T
	Pause  *stuntgres.ExecutionPause
	Held   func(*orderedSetPauseWorker)
	Worker *orderedSetPauseWorker
}

func (x *orderedSetPauseExecutor) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	w := newOrderedSetPauseWorker(ctx, x.Pause)
	x.Worker = w
	w.worker = x.T.Name()
	w.start(x.Executor, q, args)
	w.admission(x.T)
	if x.Held != nil {
		x.Held(w)
	}
	w.pending(x.T)
	x.Pause.Resume()
	w.join(x.T)
	if !w.joined {
		x.T.Fatal("resumed small sort did not join")
	}
	rows := w.out.Rows
	w.out.Rows = nil // RunStep now owns the actual cursor, including failure close.
	return rows, w.out.Err
}

func (x *orderedSetPauseExecutor) close(t *testing.T) {
	if x.Worker != nil {
		x.Worker.close(t)
	}
}
