package exec

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func TestFDWAsyncCleanup(t *testing.T) {
	for _, boundary := range []string{"pending", "ready", "completed"} {
		t.Run(boundary, func(t *testing.T) {
			var events []string
			owner := &ForeignScanOwner{}
			requests := make([]*asyncUnitRequest, 2)
			scans := make([]*asyncUnitScan, 2)
			for i := range 2 {
				name := []string{"a", "b"}[i]
				r := &asyncUnitRequest{ready: make(chan struct{})}
				requests[i] = r
				r.cancelHook = func() { events = append(events, "cancel "+name) }
				r.closeHook = func() {
					if requests[0].cancels != 1 || requests[1].cancels != 1 {
						t.Error("joined before canceling every sibling")
					}
					events = append(events, "join "+name)
				}
				scan := &asyncUnitScan{request: r, endHook: func(ctx context.Context) {
					if ctx.Err() != nil {
						t.Error("End received canceled cleanup context")
					}
					if requests[0].closes != 1 || requests[1].closes != 1 {
						t.Error("End preceded sibling join")
					}
					events = append(events, "end "+name)
				}}
				scans[i] = scan
				e := asyncUnitExecution(scan)
				f := &ForeignQuerySource{Executions: []*ForeignExecution{e}}
				owner.Add(f)
				if err := e.requestAsync(context.Background()); err != nil {
					t.Fatal(err)
				}
				if boundary != "pending" {
					close(r.ready)
				}
				if boundary == "completed" {
					// Completion already joined this request; it needs no terminal signal.
					r.present = true
					r.closeHook = func() { events = append(events, "complete "+name) }
					if err := e.settleAsync(context.Background(), false); err != nil {
						t.Fatal(err)
					}
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := owner.Finish(ctx); err != nil {
				t.Fatal(err)
			}
			if err := owner.Finish(ctx); err != nil {
				t.Fatal(err)
			}
			for i := range 2 {
				if scans[i].ends != 1 || requests[i].closes != 1 {
					t.Fatal("duplicate cleanup", events)
				}
			}
			if boundary != "completed" && !reflect.DeepEqual(events, []string{"cancel a", "cancel b", "join a", "join b", "end a", "end b"}) {
				t.Fatal("cleanup order", events)
			}
		})
	}
	t.Run("cleanup_failure_vetoes_success", func(t *testing.T) {
		failure := errors.New("join failed")
		request := &asyncUnitRequest{ready: make(chan struct{}), closeFailure: failure}
		e := asyncUnitExecution(&asyncUnitScan{request: request})
		if err := e.requestAsync(context.Background()); err != nil {
			t.Fatal(err)
		}
		owner := &ForeignScanOwner{}
		owner.Add(&ForeignQuerySource{Executions: []*ForeignExecution{e}})
		if err := owner.Finish(context.Background()); err != failure {
			t.Fatal("cleanup failure lost", err)
		}
	})
}

func TestFDWAsyncCursorBoundary(t *testing.T) {
	t.Run("settle_once_then_current_context", func(t *testing.T) {
		request := asyncUnitReady(nil, true, nil)
		scan := &asyncUnitScan{request: request}
		e := asyncUnitExecution(scan)
		imports := 0
		e.async.importRow = func(ctx context.Context, row catalog.ForeignRow) ([]sqltype.Value, error) {
			imports++
			return []sqltype.Value{}, ctx.Err()
		}
		owner := &ForeignScanOwner{}
		owner.Add(&ForeignQuerySource{Executions: []*ForeignExecution{e}})
		first, cancel := context.WithCancel(context.Background())
		if err := e.requestAsync(first); err != nil {
			t.Fatal(err)
		}
		if err := owner.Quiesce(first); err != nil {
			t.Fatal(err)
		}
		cancel()
		if imports != 1 || scan.requests != 1 || request.completes != 1 || request.closes != 1 || e.async.request != nil || !e.async.buffered {
			t.Fatal("motion retained work or advanced frontier")
		}
		if err := owner.Quiesce(context.Background()); err != nil || imports != 1 {
			t.Fatal("buffered row reimported", err)
		}
		e.async.values, e.async.buffered = nil, false
		scan.request = asyncUnitReady(nil, false, nil)
		if err := e.requestAsync(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := owner.Quiesce(context.Background()); err != nil || scan.requests != 2 || !e.async.eof {
			t.Fatal("later demand lost frontier", err)
		}
		if err := owner.Finish(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("failed_batch_cancels_siblings", func(t *testing.T) {
		primary := errors.New("remote fetch failure")
		bad := asyncUnitExecution(&asyncUnitScan{request: asyncUnitReady(nil, false, primary)})
		pending := &asyncUnitRequest{ready: make(chan struct{})}
		sibling := asyncUnitExecution(&asyncUnitScan{request: pending})
		owner := &ForeignScanOwner{}
		owner.Add(&ForeignQuerySource{Executions: []*ForeignExecution{bad, sibling}})
		for _, e := range []*ForeignExecution{bad, sibling} {
			if err := e.requestAsync(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		if err := owner.Quiesce(context.Background()); err != primary {
			t.Fatal("batch failure lost", err)
		}
		if err := owner.Finish(context.Background()); err != nil || pending.cancels != 1 || pending.closes != 1 {
			t.Fatal("failed batch leaked sibling", err)
		}
	})
	t.Run("cancel_pending_motion", func(t *testing.T) {
		pending := &asyncUnitRequest{ready: make(chan struct{})}
		e := asyncUnitExecution(&asyncUnitScan{request: pending})
		owner := &ForeignScanOwner{}
		owner.Add(&ForeignQuerySource{Executions: []*ForeignExecution{e}})
		ctx, cancel := context.WithCancel(context.Background())
		if err := e.requestAsync(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
		if err := owner.Quiesce(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if err := owner.Finish(ctx); err != nil || pending.cancels != 1 || pending.closes != 1 {
			t.Fatal("motion cancellation leaked request", err)
		}
	})
}
