package engine

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func serializableUnitAcquisition(t *testing.T, e *Engine, parent context.Context, readOnly, deferrable bool) (*StatementScope, *serializableTransaction) {
	t.Helper()
	session := sessionStart(t, e, nil)
	op, err := session.Acquire(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	ref, err := e.Begin(op, TxOptions{Isolation: Serializable, ReadOnly: readOnly, Deferrable: deferrable, DeferrableSet: true})
	if err != nil {
		t.Fatal(err)
	}
	return &StatementScope{owner: session.transactions, op: op, txRef: ref}, e.serializable.nodes[ref]
}
func serializableUnitAwaitCandidate(t *testing.T, e *Engine, node *serializableTransaction) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		e.mu.Lock()
		ready := node.ready != nil && !node.resolved && len(node.possible) > 0
		e.mu.Unlock()
		if ready {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("candidate registration", err)
		}
		runtime.Gosched()
	}
}
func serializableUnitCaptureWorker(t *testing.T, scope *StatementScope) func() error {
	t.Helper()
	done := make(chan error, 1)
	joined := false
	t.Cleanup(func() {
		if !joined {
			scope.op.session.RequestEnd(CleanClose)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("safe capture cleanup did not join")
			}
		}
	})
	go func() {
		scope.owner.mu.Lock()
		err := scope.acquireSerializableSnapshot()
		scope.owner.mu.Unlock()
		done <- err
	}()
	return func() error {
		t.Helper()
		select {
		case err := <-done:
			joined = true
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("safe capture did not return")
			return nil
		}
	}
}
func TestSerializableSafeSnapshotImmediateAndInactiveDeferrable(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		e := New()
		scope, node := serializableUnitAcquisition(t, e, context.Background(), readOnly, true)
		scope.owner.mu.Lock()
		err := scope.acquireSerializableSnapshot()
		scope.owner.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if !scope.owner.snapshotSet || node.readOnly != readOnly || node.deferrable != true || node.exempt != readOnly || node.ready != nil {
			t.Fatal("immediate snapshot policy", node)
		}
	}
}
func TestSerializableSafeSnapshotWriterAbortOrNoWriteCommitWakes(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "abort", true: "empty_commit"}[commit], func(t *testing.T) {
			e := New()
			writer := serializableUnitNode(t, e, false)
			scope, node := serializableUnitAcquisition(t, e, context.Background(), true, true)
			join := serializableUnitCaptureWorker(t, scope)
			serializableUnitAwaitCandidate(t, e, node)
			e.mu.Lock()
			if commit {
				e.serializableCommittedLocked(writer.ref, e.commitSeq)
			}
			e.endSerializableLocked(writer.ref, commit)
			e.mu.Unlock()
			if err := join(); err != nil {
				t.Fatal(err)
			}
			if !node.exempt || !scope.owner.snapshotSet || len(node.possible) != 0 {
				t.Fatal("safe resolution did not preserve TX snapshot")
			}
		})
	}
}
func TestSerializableSafeSnapshotUnsafeRejectsRootsAndRecaptures(t *testing.T) {
	e := New()
	ref := serializableUnitRelation(t, e)
	writer := serializableUnitNode(t, e, false)
	out := serializableUnitNode(t, e, false)
	e.mu.Lock()
	writer.didWrite = true
	writer.outgoing[out] = struct{}{}
	out.incoming[writer] = struct{}{}
	e.serializableCommittedLocked(out.ref, e.commitSeq)
	old := e.storage[ref.Address]
	e.mu.Unlock()
	scope, node := serializableUnitAcquisition(t, e, context.Background(), true, true)
	join := serializableUnitCaptureWorker(t, scope)
	serializableUnitAwaitCandidate(t, e, node)
	e.mu.Lock()
	if scope.owner.roots[ref.Address].storage != old {
		t.Fatal("candidate did not capture old root")
	}
	next := &relationStorage{generation: old.generation + 1}
	e.storage[ref.Address] = next
	e.commitSeq++
	e.serializableCommittedLocked(writer.ref, e.commitSeq)
	e.endSerializableLocked(writer.ref, true)
	e.mu.Unlock()
	if err := join(); err != nil {
		t.Fatal(err)
	}
	if scope.owner.roots[ref.Address].storage != next || scope.owner.frontier != e.commitSeq || !node.exempt || node.unsafe {
		t.Fatal("unsafe candidate escaped or was not recaptured")
	}
}
func TestSerializableSafeSnapshotLateOutgoingCommitIsSafe(t *testing.T) {
	e := New()
	writer := serializableUnitNode(t, e, false)
	out := serializableUnitNode(t, e, false)
	scope, node := serializableUnitAcquisition(t, e, context.Background(), true, true)
	join := serializableUnitCaptureWorker(t, scope)
	serializableUnitAwaitCandidate(t, e, node)
	e.mu.Lock()
	writer.didWrite = true
	writer.outgoing[out] = struct{}{}
	out.incoming[writer] = struct{}{}
	e.serializableCommittedLocked(out.ref, e.commitSeq)
	e.serializableCommittedLocked(writer.ref, e.commitSeq)
	e.endSerializableLocked(writer.ref, true)
	e.mu.Unlock()
	if err := join(); err != nil {
		t.Fatal(err)
	}
	if !node.exempt || node.unsafe {
		t.Fatal("late outgoing commit made old candidate unsafe")
	}
}
func TestSerializableSafeSnapshotCancelAndEpochFence(t *testing.T) {
	for _, mode := range []string{"cancel", "same_owner", "foreign_owner"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			setup := sessionStart(t, e, nil)
			sessionSQL(t, setup, "CREATE TABLE replacement_rows(v INT UNIQUE)")
			sessionSQL(t, setup, "INSERT INTO replacement_rows VALUES(11)")
			relation := catalogRef(t, e, e.CatalogRead(), "replacement_rows")
			writerScope, writer := serializableUnitAcquisition(t, e, context.Background(), false, false)
			writerScope.owner.mu.Lock()
			err := writerScope.acquireSerializableSnapshot()
			writerScope.owner.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope, node := serializableUnitAcquisition(t, e, ctx, true, true)
			join := serializableUnitCaptureWorker(t, scope)
			serializableUnitAwaitCandidate(t, e, node)
			e.mu.Lock()
			ready := node.ready
			oldEpoch := e.state.epoch
			oldRoot := scope.owner.roots[relation.Address]
			before := serializableSnapshotPublication(e)
			_, containsWriter := node.possible[writer]
			blocked := containsWriter && len(node.possible) == 1 && !node.resolved && !scope.owner.snapshotSet && !writer.ended && !writer.committed && oldRoot.storage != nil
			e.mu.Unlock()
			if !blocked {
				t.Fatal("candidate was not blocked on the actual unresolved captured writer")
			}
			select {
			case <-ready:
				t.Fatal("candidate readiness closed before resolution or replacement")
			default:
			}
			if mode == "cancel" {
				cancel()
			} else {
				replacement := e.CatalogRead()
				if mode == "foreign_owner" {
					other := New()
					otherSession := sessionStart(t, other, nil)
					sessionSQL(t, otherSession, "CREATE TABLE replacement_rows(v INT UNIQUE)")
					sessionSQL(t, otherSession, "INSERT INTO replacement_rows VALUES(22)")
					replacement = other.CatalogRead()
				}
				// Reach a real post-copy capacity failure, not a fabricated
				// cancellation or invalid input. Failed preparation must leave
				// both committed authority and the unresolved candidate intact.
				e.mu.Lock()
				nextRow := e.nextRow
				e.nextRow = ^uint64(0)
				e.mu.Unlock()
				changes, err := e.ReplaceCatalog(context.Background(), replacement)
				e.mu.Lock()
				e.nextRow = nextRow
				e.mu.Unlock()
				if err == nil || err.Error() != "stuntgres: row identity exhausted" {
					t.Fatal("replacement preparation failure", changes, err)
				}
				if changes.Replacement || len(changes.Definitions) != 0 || len(changes.Statistics) != 0 || len(changes.Namespaces) != 0 {
					t.Fatal("failed replacement emitted changes", changes)
				}
				func() {
					e.mu.Lock()
					defer e.mu.Unlock()
					serializableRequirePublicationUnchanged(t, e, before)
					if e.serializable.nodes[writer.ref] != writer || e.serializable.nodes[node.ref] != node || node.ready != ready || node.resolved || node.aborted || writer.aborted || scope.owner.roots[relation.Address].storage != oldRoot.storage {
						t.Fatal("failed replacement fenced or discarded acquisition")
					}
				}()
				select {
				case <-ready:
					t.Fatal("failed replacement woke unresolved candidate")
				default:
				}
				changes, err = e.ReplaceCatalog(context.Background(), replacement)
				if err != nil || !changes.Replacement {
					t.Fatal("successful replacement", changes, err)
				}
				// The callback-free installation must itself close readiness.
				// No writer end, context cancellation or timing inference can
				// supply this wake, even if the worker has not yet entered select.
				select {
				case <-ready:
				default:
					t.Fatal("successful replacement did not close candidate readiness")
				}
			}
			err = join()
			want := error(context.Canceled)
			if mode != "cancel" {
				want = ErrCatalogReplaced
			}
			if !errors.Is(err, want) {
				t.Fatal("safe wait remapped ordinary cause", err, want)
			}
			func() {
				e.mu.Lock()
				defer e.mu.Unlock()
				if scope.owner.roots != nil || scope.owner.frontier != 0 || scope.owner.snapshotSet || len(node.possible) != 0 || !node.resolved {
					t.Fatal("candidate roots/wait not released")
				}
				if writer.ended || writer.committed {
					t.Fatal("writer resolution supplied the observed wake")
				}
				if !reflect.DeepEqual(oldRoot.storage.versions[:cap(oldRoot.storage.versions)], before.storage[relation.Address].rows) || oldRoot.ends != before.storage[relation.Address].ends || (oldRoot.ends != nil && !reflect.DeepEqual(oldRoot.ends.rows, before.storage[relation.Address].endRows)) {
					t.Fatal("replacement mutated immutable old snapshot")
				}
				if mode != "cancel" && (e.state.epoch != oldEpoch+1 || !node.aborted || !writer.aborted || len(e.serializable.nodes) != 0 || len(e.serializable.creators) != 0) {
					t.Fatal("replacement retained old epoch SSI participation")
				}
			}()
			if mode != "cancel" {
				freshScope, fresh := serializableUnitAcquisition(t, e, context.Background(), true, true)
				freshScope.owner.mu.Lock()
				err := freshScope.acquireSerializableSnapshot()
				freshScope.owner.mu.Unlock()
				if err != nil {
					t.Fatal("new epoch acquisition", err)
				}
				if fresh.ref.Epoch() != oldEpoch+1 || !fresh.exempt || !freshScope.owner.snapshotSet || len(fresh.possible) != 0 || freshScope.owner.frontier != e.commitSeq {
					t.Fatal("new epoch inherited replaced cohort")
				}
				freshRelation := catalogRef(t, e, e.CatalogRead(), "replacement_rows")
				if freshScope.owner.roots[freshRelation.Address].storage == oldRoot.storage {
					t.Fatal("new epoch reused replaced storage root")
				}
				e.endSerializable(fresh.ref, false)
			}
			e.endSerializable(writer.ref, false)
			e.endSerializable(node.ref, false)
		})
	}
}
func TestSerializableSafeSnapshotTerminalSessionDrain(t *testing.T) {
	e := New()
	writer := serializableUnitNode(t, e, false)
	scope, node := serializableUnitAcquisition(t, e, context.Background(), true, true)
	join := serializableUnitCaptureWorker(t, scope)
	serializableUnitAwaitCandidate(t, e, node)
	session := scope.op.session
	session.RequestEnd(CleanClose)
	if err := join(); err == nil {
		t.Fatal("terminal capture succeeded")
	}
	scope.op.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := session.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
	e.endSerializable(writer.ref, false)
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.serializable.nodes) != 0 {
		t.Fatal("terminal drain retained cohort")
	}
}
func TestSerializableSafeSnapshotResolutionBeforeSleepCannotLoseWake(t *testing.T) {
	e := New()
	writer := serializableUnitNode(t, e, false)
	reader := serializableUnitNode(t, e, true)
	e.mu.Lock()
	reader.possible = map[*serializableTransaction]struct{}{writer: {}}
	reader.ready = make(chan struct{})
	e.endSerializableLocked(writer.ref, false)
	ready := reader.ready
	e.mu.Unlock()
	select {
	case <-ready:
	default:
		t.Fatal("resolution before sleep lost notification")
	}
	if !reader.resolved || !reader.exempt {
		t.Fatal("early wake missing safe state")
	}
}
func TestSerializablePublicationOrdinaryFailureCannotDoomPeers(t *testing.T) {
	e := New()
	a := serializableUnitNode(t, e, false)
	b := serializableUnitNode(t, e, false)
	e.mu.Lock()
	e.serializableEdgeLocked(a, a, b)
	e.serializableEdgeLocked(b, b, a)
	frontier := e.commitSeq
	e.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	op, err := txOwnerOf(a.ref).session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	cancel()
	err = e.publishRows(op, a.ref, nil, nil, nil, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("wrong precondition cause", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if a.committed || b.committed || a.doomed || b.doomed || e.commitSeq != frontier {
		t.Fatal("failed ordinary preflight published or selected a victim")
	}
}

func TestSerializableSafeSnapshotAttemptedWriterRemainsCandidate(t *testing.T) {
	e := New()
	writer := serializableUnitNode(t, e, true)
	e.mu.Lock()
	// A later SQL READ ONLY assignment and discarded child writes do not
	// turn attempted-write history into a genuinely read-only participant.
	writer.didWrite = true
	e.mu.Unlock()
	scope, reader := serializableUnitAcquisition(t, e, context.Background(), true, true)
	join := serializableUnitCaptureWorker(t, scope)
	serializableUnitAwaitCandidate(t, e, reader)
	e.mu.Lock()
	e.endSerializableLocked(writer.ref, false)
	e.mu.Unlock()
	if err := join(); err != nil {
		t.Fatal(err)
	}
	if !reader.exempt || !scope.owner.snapshotSet {
		t.Fatal("writer resolution did not release safe acquisition")
	}
}

func TestSerializableGoBirthDeferrableOverridePresence(t *testing.T) {
	for _, override := range []bool{false, true} {
		e := New()
		session := sessionStart(t, e, nil)
		sessionSQL(t, session, "SET default_transaction_deferrable=on")
		op := transactionOperation(t, session)
		ref, err := e.Begin(op, TxOptions{Isolation: Serializable, ReadOnly: true, DeferrableSet: override, Deferrable: false})
		if err != nil {
			t.Fatal(err)
		}
		if session.transactions.deferrable == override || !session.transactions.entryDeferrable {
			t.Fatal("Go birth confused omitted and explicit deferrability overrides")
		}
		scope := &StatementScope{owner: session.transactions, op: op, txRef: ref}
		scope.owner.mu.Lock()
		err = scope.acquireSerializableSnapshot()
		scope.owner.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		node := e.serializable.nodes[ref]
		if node.deferrable == override {
			t.Fatal("frozen acquisition classification lost inherited/explicit mode")
		}
		e.mu.Unlock()
		if _, err := e.Rollback(op, ref); err != nil {
			t.Fatal(err)
		}
	}
}
