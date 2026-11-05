package core_test

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
)

// This is a real context with a test-owned barrier at an existing context
// observation, not an alternate host or production observer. The controller
// acquires Engine.Mu before resuming this still-live observation. Cancellation
// happens only after the worker is witnessed parked on that engine mutex.
// Subsequent Err/Done observations retain the ordinary context contract.
//
// Buffer/all gate in Active's initial validation, hence witness the initial
// engine acquisition after admission. Relation gates in the real lock-manager
// admission after the first engine window, hence witnesses the final engine
// reacquisition. No second-acquisition claim is made for buffer/all: there is
// no intervening callback on those routes. All three require cancellation to
// prevent any mutation at the final cache-publication boundary.
type evictionCancellationContext struct {
	context.Context
	phase string
	entered chan struct{}
	resume chan struct{}
	gated atomic.Bool
}

func (c *evictionCancellationContext) Err() error {
	err := c.Context.Err()
	if err != nil || c.gated.Load() { return err }
	var pcs [32]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	installed, phase := false, false
	for {
		frame, more := frames.Next()
		installed = installed || strings.Contains(frame.Function, "(*contribHost).PgBuffercacheEviction")
		phase = phase || strings.Contains(frame.Function, c.phase)
		if !more { break }
	}
	if installed && phase && c.gated.CompareAndSwap(false, true) {
		close(c.entered)
		<-c.resume
	}
	return err
}

type evictionCancellationOutcome struct {
	counts [3]int64
	err error
}

func evictionCancellationInvoke(scope *core.StatementScope, n plan.InstalledPgBuffercacheEvictionExpr, input int64, done chan<- evictionCancellationOutcome) {
	counts, err := scope.WriteHost().Contrib().PgBuffercacheEviction(n, input)
	done <- evictionCancellationOutcome{counts: counts, err: err}
}

// Exact occupied slot IDs plus every exposed storage/dirty/usage/pin field.
// Omitting unused rows does not lose occupancy: any removed/replaced slot
// changes this map. Observation uses the genuine installed provider cursor.
func evictionCancellationSnapshot(t *testing.T, e *core.Engine, observer *session.Session) map[int64][8]int64 {
	t.Helper()
	cursor := bufferUnitCursor(t, e, observer)
	defer cursor.Close()
	out := make(map[int64][8]int64)
	for {
		row, present, err := cursor.Next(t.Context())
		if err != nil { t.Fatal(err) }
		if !present { return out }
		if row.CompositeField(1).IsNull() { continue }
		slot, err := row.CompositeField(0).Integer()
		if err != nil { t.Fatal(err) }
		var fields [8]int64
		for i := 1; i <= 8; i++ {
			value := row.CompositeField(i)
			if i <= 3 {
				oid, failure := value.OID()
				if failure != nil { t.Fatal(failure) }
				fields[i-1] = int64(oid)
			} else if i == 6 {
				dirty, failure := value.Boolean()
				if failure != nil { t.Fatal(failure) }
				if dirty { fields[i-1] = 1 }
			} else {
				number, failure := value.Integer()
				if failure != nil { t.Fatal(failure) }
				fields[i-1] = number
			}
		}
		out[slot] = fields
	}
}

func TestPgBuffercacheEvictionCancellationReacquire(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind functions.PgBuffercacheEvictionKind
		phase string
	}{
		{"buffer_initial_acquire", functions.EvictBuffer, ".ValidateOperation"},
		{"all_initial_acquire", functions.EvictAll, ".ValidateOperation"},
		{"relation_final_reacquire", functions.EvictRelation, "(*Manager).startAcquire"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := bufferUnitEngine(t)
			for _, query := range []string{
				"CREATE TABLE eviction_cancel_target(k integer)", "INSERT INTO eviction_cancel_target VALUES(1),(2)",
				"CREATE TABLE eviction_cancel_pinned(k integer)", "INSERT INTO eviction_cancel_pinned VALUES(1),(2)",
				"SELECT k FROM eviction_cancel_target", "SELECT k FROM eviction_cancel_target",
			} { enginetest.Run(t, e, query) }
			pinOwner := enginetest.StartSession(t, e, nil)
			_, source, command := bufferUnitPortal(t, e, pinOwner, "eviction_cancel_pin", "SELECT k FROM eviction_cancel_pinned")
			defer func() {
				command(func(s *core.StatementScope) error { return s.CloseProtocolPortal("eviction_cancel_pin") })
				bufferUnitReleased(t, source)
				enginetest.SessionSQL(t, pinOwner, "ROLLBACK")
			}()
			observer, caller := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			n := evictionUnitCarrier(t, e, tc.kind)
			op, err := caller.Acquire(t.Context())
			if err != nil { t.Fatal(err) }
			scope, err := e.StartStatement(op)
			if err != nil { op.Done(); t.Fatal(err) }
			setup, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
			if setup.Rows != nil { setup.Rows.Close() }
			if err != nil { scope.Finish(err); op.Done(); t.Fatal(err) }
			base, cancel := context.WithCancel(t.Context())
			active := &evictionCancellationContext{Context: base, phase: tc.phase, entered: make(chan struct{}), resume: make(chan struct{})}
			op.Ctx = active
			done := make(chan evictionCancellationOutcome, 1)
			var outcome evictionCancellationOutcome
			started, joined, released, locked := false, false, false, false
			defer func() {
				cancel()
				if !released { close(active.resume) }
				if locked { e.Mu.Unlock() }
				if started && !joined {
					select {
					case outcome = <-done:
					case <-time.After(enginetest.Liveness):
						t.Error("canceled eviction worker did not join")
						return
					}
				}
				scope.Finish(outcome.err)
				op.Done()
			}()
			before := evictionCancellationSnapshot(t, e, observer)
			targetNode := int64(bufferUnitNode(t, e, "eviction_cancel_target"))
			pinnedNode := int64(bufferUnitNode(t, e, "eviction_cancel_pinned"))
			targetRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "eviction_cancel_target")
			var slot int64
			pinned := false
			for id, fields := range before {
				if fields[0] == targetNode && fields[6] == 2 && fields[7] == 0 { slot = id }
				if fields[0] == pinnedNode && fields[6] == 1 && fields[7] == 1 { pinned = true }
			}
			if len(before) != 2 || slot == 0 || !pinned { t.Fatal("fixture lacks genuine used/unpinned and pinned entries", before) }
			var input int64
			if tc.kind == functions.EvictBuffer { input = slot }
			if tc.kind == functions.EvictRelation { input = int64(targetRef.Address.ObjectID) }
			started = true
			go evictionCancellationInvoke(scope, n, input, done)
			select {
			case <-active.entered:
			case outcome = <-done:
				joined = true
				t.Fatal("installed call ended before admitted barrier", outcome)
			case <-time.After(enginetest.Liveness): t.Fatal("installed call missed genuine context barrier")
			}
			e.Mu.Lock()
			locked = true
			close(active.resume)
			released = true
			deadline := time.Now().Add(enginetest.Liveness)
			stacks := make([]byte, 64<<10)
			for {
				select {
				case outcome = <-done:
					joined = true
					t.Fatal("worker bypassed held engine mutex", outcome)
				default:
				}
				count := runtime.Stack(stacks, true)
				if count == len(stacks) { stacks = make([]byte, len(stacks)*2); continue }
				parked := false
				for _, stack := range bytes.Split(stacks[:count], []byte("\n\n")) {
					if bytes.Contains(stack, []byte("evictionCancellationInvoke")) && bytes.Contains(stack, []byte("(*contribHost).PgBuffercacheEviction")) && (bytes.Contains(stack, []byte("(*Mutex).Lock")) || bytes.Contains(stack, []byte("(*Mutex).lockSlow"))) {
						parked = true
						break
					}
				}
				if parked { break }
				if time.Now().After(deadline) { t.Fatal("admitted eviction worker never parked on Engine.Mu") }
				runtime.Gosched()
			}
			if tc.kind == functions.EvictRelation {
				if !e.Locks.HasRelationMode(scope.LockTicket, lock.Resource{ObjectRef: targetRef}, lock.AccessShare) { t.Fatal("relation did not pass genuine contribution admission before final engine wait") }
			}
			cancel()
			e.Mu.Unlock()
			locked = false
			select {
			case outcome = <-done: joined = true
			case <-time.After(enginetest.Liveness): t.Fatal("canceled engine waiter did not terminate")
			}
			if !errors.Is(outcome.err, context.Canceled) || outcome.counts != [3]int64{} { t.Error("admitted cancellation failed to reject mutation", outcome.counts, outcome.err) }
			after := evictionCancellationSnapshot(t, e, observer)
			if !maps.Equal(before, after) { t.Error("canceled eviction changed exact occupancy/storage/dirty/usage/pins", "before", before, "after", after) }
		})
	}
}
