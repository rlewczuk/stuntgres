package engine

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func serializableUnitAcquisition(t *testing.T, e *Engine, parent context.Context, readOnly, deferrable bool) (*StatementScope, *visibility.SerializableTx) {
	t.Helper()
	sess := sessionStart(t, e, nil)
	op, err := sess.Acquire(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	ref, err := e.Begin(op, session.TxOptions{Isolation: session.Serializable, ReadOnly: readOnly, Deferrable: deferrable, DeferrableSet: true})
	if err != nil {
		t.Fatal(err)
	}
	return &StatementScope{Owner: sess.Transactions, Op: op, TxRef: ref}, e.Visibility.Serializable.Nodes[ref]
}
func serializableUnitAwaitCandidate(t *testing.T, e *Engine, node *visibility.SerializableTx) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		e.Mu.Lock()
		ready := node.Ready != nil && !node.Resolved && len(node.Possible) > 0
		e.Mu.Unlock()
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
			scope.Op.Session.RequestEnd(session.CleanClose)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("safe capture cleanup did not join")
			}
		}
	})
	go func() {
		scope.Owner.Mu.Lock()
		err := scope.AcquireSerializableSnapshot()
		scope.Owner.Mu.Unlock()
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
		scope.Owner.Mu.Lock()
		err := scope.AcquireSerializableSnapshot()
		scope.Owner.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if !scope.Owner.SnapshotSet || node.ReadOnly != readOnly || node.Deferrable != true || node.Exempt != readOnly || node.Ready != nil {
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
			e.Mu.Lock()
			candidate, frontier := scope.Owner.CurrentSnapshot, scope.Owner.Frontier
			if commit {
				e.Visibility.SerializableCommittedLocked(writer.Ref, e.Visibility.CommitSeq())
			}
			e.endSerializableLocked(writer.Ref, commit)
			e.Mu.Unlock()
			if err := join(); err != nil {
				t.Fatal(err)
			}
			if !node.Exempt || !scope.Owner.SnapshotSet || len(node.Possible) != 0 || candidate == nil || scope.Owner.CurrentSnapshot != candidate || scope.Owner.Frontier != frontier {
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
	e.Mu.Lock()
	if err := e.AssignTransactionXIDLocked(writer.Ref); err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	if err := e.AssignTransactionXIDLocked(out.Ref); err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	writerID := e.Visibility.FullXIDLocked(writer.Ref)
	e.Visibility.EndTransactionIdentityLocked(out.Ref, true)
	writer.DidWrite = true
	writer.Outgoing[out] = struct{}{}
	out.Incoming[writer] = struct{}{}
	e.Visibility.SerializableCommittedLocked(out.Ref, e.Visibility.CommitSeq())
	old := e.Storage.Relations[ref.Address]
	e.Mu.Unlock()
	scope, node := serializableUnitAcquisition(t, e, context.Background(), true, true)
	join := serializableUnitCaptureWorker(t, scope)
	serializableUnitAwaitCandidate(t, e, node)
	e.Mu.Lock()
	candidate := scope.Owner.CurrentSnapshot
	if candidate == nil || candidate.Visible(writerID) {
		e.Mu.Unlock()
		t.Fatal("candidate public snapshot did not include its active writer")
	}
	if scope.Owner.Roots[ref.Address].Storage != old {
		t.Fatal("candidate did not capture old root")
	}
	next := &storage.Relation{Generation: old.Generation + 1}
	e.Storage.InstallRelationLocked(ref.Address, next)
	e.Visibility.AdvanceCommitSeqLocked()
	e.Visibility.SerializableCommittedLocked(writer.Ref, e.Visibility.CommitSeq())
	e.Visibility.EndTransactionIdentityLocked(writer.Ref, true)
	e.endSerializableLocked(writer.Ref, true)
	e.Mu.Unlock()
	if err := join(); err != nil {
		t.Fatal(err)
	}
	if scope.Owner.Roots[ref.Address].Storage != next || scope.Owner.Frontier != e.Visibility.CommitSeq() || !node.Exempt || node.Unsafe || scope.Owner.CurrentSnapshot == nil || scope.Owner.CurrentSnapshot == candidate || !scope.Owner.CurrentSnapshot.Visible(writerID) || candidate.Visible(writerID) {
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
	e.Mu.Lock()
	writer.DidWrite = true
	writer.Outgoing[out] = struct{}{}
	out.Incoming[writer] = struct{}{}
	e.Visibility.SerializableCommittedLocked(out.Ref, e.Visibility.CommitSeq())
	e.Visibility.SerializableCommittedLocked(writer.Ref, e.Visibility.CommitSeq())
	e.endSerializableLocked(writer.Ref, true)
	e.Mu.Unlock()
	if err := join(); err != nil {
		t.Fatal(err)
	}
	if !node.Exempt || node.Unsafe {
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
			writerScope.Owner.Mu.Lock()
			err := writerScope.AcquireSerializableSnapshot()
			writerScope.Owner.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope, node := serializableUnitAcquisition(t, e, ctx, true, true)
			join := serializableUnitCaptureWorker(t, scope)
			serializableUnitAwaitCandidate(t, e, node)
			e.Mu.Lock()
			ready := node.Ready
			oldEpoch := e.Catalog.State.Epoch()
			oldRoot := scope.Owner.Roots[relation.Address]
			before := serializableSnapshotPublication(e)
			_, containsWriter := node.Possible[writer]
			blocked := containsWriter && len(node.Possible) == 1 && !node.Resolved && !scope.Owner.SnapshotSet && !writer.Ended && !writer.Committed && oldRoot.Storage != nil
			e.Mu.Unlock()
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
				replacement, replacementSource := e.CatalogRead(), e
				if mode == "foreign_owner" {
					other := New()
					otherSession := sessionStart(t, other, nil)
					sessionSQL(t, otherSession, "CREATE TABLE replacement_rows(v INT UNIQUE)")
					sessionSQL(t, otherSession, "INSERT INTO replacement_rows VALUES(22)")
					replacement, replacementSource = other.CatalogRead(), other
				}
				// Reach a real post-copy capacity failure, not a fabricated
				// cancellation or invalid input. Failed preparation must leave
				// both committed authority and the unresolved candidate intact.
				e.Mu.Lock()
				nextRow := e.Storage.RowFrontier()
				e.Storage.SetFrontierLocked(^uint64(0), e.Storage.VersionFrontier())
				e.Mu.Unlock()
				changes, err := e.ReplaceCatalog(context.Background(), replacementSource, replacement)
				e.Mu.Lock()
				e.Storage.SetFrontierLocked(nextRow, e.Storage.VersionFrontier())
				e.Mu.Unlock()
				if err == nil || err.Error() != "stuntgres: row identity exhausted" {
					t.Fatal("replacement preparation failure", changes, err)
				}
				if changes.Replacement || len(changes.Definitions) != 0 || len(changes.Statistics) != 0 || len(changes.Namespaces) != 0 {
					t.Fatal("failed replacement emitted changes", changes)
				}
				func() {
					e.Mu.Lock()
					defer e.Mu.Unlock()
					serializableRequirePublicationUnchanged(t, e, before)
					if e.Visibility.Serializable.Nodes[writer.Ref] != writer || e.Visibility.Serializable.Nodes[node.Ref] != node || node.Ready != ready || node.Resolved || node.Aborted || writer.Aborted || scope.Owner.Roots[relation.Address].Storage != oldRoot.Storage {
						t.Fatal("failed replacement fenced or discarded acquisition")
					}
				}()
				select {
				case <-ready:
					t.Fatal("failed replacement woke unresolved candidate")
				default:
				}
				changes, err = e.ReplaceCatalog(context.Background(), replacementSource, replacement)
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
				want = ids.ErrCatalogReplaced
			}
			if !errors.Is(err, want) {
				t.Fatal("safe wait remapped ordinary cause", err, want)
			}
			func() {
				e.Mu.Lock()
				defer e.Mu.Unlock()
				if scope.Owner.Roots != nil || scope.Owner.Frontier != 0 || scope.Owner.CurrentSnapshot != nil || scope.currentSnapshot != nil || scope.Owner.SnapshotSet || len(node.Possible) != 0 || !node.Resolved {
					t.Fatal("candidate roots/wait not released")
				}
				if writer.Ended || writer.Committed {
					t.Fatal("writer resolution supplied the observed wake")
				}
				if !reflect.DeepEqual(oldRoot.Storage.Versions[:cap(oldRoot.Storage.Versions)], before.storage[relation.Address].rows) || oldRoot.Ends != before.storage[relation.Address].ends || (oldRoot.Ends != nil && !reflect.DeepEqual(oldRoot.Ends.Rows, before.storage[relation.Address].endRows)) {
					t.Fatal("replacement mutated immutable old snapshot")
				}
				if mode != "cancel" && (e.Catalog.State.Epoch() != oldEpoch+1 || !node.Aborted || !writer.Aborted || len(e.Visibility.Serializable.Nodes) != 0 || len(e.Visibility.Serializable.Creators) != 0) {
					t.Fatal("replacement retained old epoch SSI participation")
				}
			}()
			if mode != "cancel" {
				freshScope, fresh := serializableUnitAcquisition(t, e, context.Background(), true, true)
				freshScope.Owner.Mu.Lock()
				err := freshScope.AcquireSerializableSnapshot()
				freshScope.Owner.Mu.Unlock()
				if err != nil {
					t.Fatal("new epoch acquisition", err)
				}
				if fresh.Ref.Epoch() != oldEpoch+1 || !fresh.Exempt || !freshScope.Owner.SnapshotSet || len(fresh.Possible) != 0 || freshScope.Owner.Frontier != e.Visibility.CommitSeq() {
					t.Fatal("new epoch inherited replaced cohort")
				}
				freshRelation := catalogRef(t, e, e.CatalogRead(), "replacement_rows")
				if freshScope.Owner.Roots[freshRelation.Address].Storage == oldRoot.Storage {
					t.Fatal("new epoch reused replaced storage root")
				}
				e.endSerializable(fresh.Ref, false)
			}
			e.endSerializable(writer.Ref, false)
			e.endSerializable(node.Ref, false)
		})
	}
}
func TestSerializableSafeSnapshotTerminalSessionDrain(t *testing.T) {
	e := New()
	writer := serializableUnitNode(t, e, false)
	scope, node := serializableUnitAcquisition(t, e, context.Background(), true, true)
	join := serializableUnitCaptureWorker(t, scope)
	serializableUnitAwaitCandidate(t, e, node)
	sess := scope.Op.Session
	sess.RequestEnd(session.CleanClose)
	if err := join(); err == nil {
		t.Fatal("terminal capture succeeded")
	}
	scope.Op.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sess.WaitEnd(ctx); err != nil {
		t.Fatal(err)
	}
	e.endSerializable(writer.Ref, false)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if len(e.Visibility.Serializable.Nodes) != 0 {
		t.Fatal("terminal drain retained cohort")
	}
}
func TestSerializableSafeSnapshotResolutionBeforeSleepCannotLoseWake(t *testing.T) {
	e := New()
	writer := serializableUnitNode(t, e, false)
	reader := serializableUnitNode(t, e, true)
	e.Mu.Lock()
	reader.Possible = map[*visibility.SerializableTx]struct{}{writer: {}}
	reader.Ready = make(chan struct{})
	e.endSerializableLocked(writer.Ref, false)
	ready := reader.Ready
	e.Mu.Unlock()
	select {
	case <-ready:
	default:
		t.Fatal("resolution before sleep lost notification")
	}
	if !reader.Resolved || !reader.Exempt {
		t.Fatal("early wake missing safe state")
	}
}
func TestSerializablePublicationOrdinaryFailureCannotDoomPeers(t *testing.T) {
	e := New()
	a := serializableUnitNode(t, e, false)
	b := serializableUnitNode(t, e, false)
	e.Mu.Lock()
	e.Visibility.SerializableEdgeLocked(a, a, b)
	e.Visibility.SerializableEdgeLocked(b, b, a)
	frontier := e.Visibility.CommitSeq()
	e.Mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	op, err := session.TxOwnerOf(a.Ref).Session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	cancel()
	err = e.publishRows(op, a.Ref, nil, nil, nil, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("wrong precondition cause", err)
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if a.Committed || b.Committed || a.Doomed || b.Doomed || e.Visibility.CommitSeq() != frontier {
		t.Fatal("failed ordinary preflight published or selected a victim")
	}
}

func TestSerializableSafeSnapshotAttemptedWriterRemainsCandidate(t *testing.T) {
	e := New()
	writer := serializableUnitNode(t, e, true)
	e.Mu.Lock()
	// A later SQL READ ONLY assignment and discarded child writes do not
	// turn attempted-write history into a genuinely read-only participant.
	writer.DidWrite = true
	e.Mu.Unlock()
	scope, reader := serializableUnitAcquisition(t, e, context.Background(), true, true)
	join := serializableUnitCaptureWorker(t, scope)
	serializableUnitAwaitCandidate(t, e, reader)
	e.Mu.Lock()
	e.endSerializableLocked(writer.Ref, false)
	e.Mu.Unlock()
	if err := join(); err != nil {
		t.Fatal(err)
	}
	if !reader.Exempt || !scope.Owner.SnapshotSet {
		t.Fatal("writer resolution did not release safe acquisition")
	}
}

func TestSerializableGoBirthDeferrableOverridePresence(t *testing.T) {
	for _, override := range []bool{false, true} {
		e := New()
		sess := sessionStart(t, e, nil)
		sessionSQL(t, sess, "SET default_transaction_deferrable=on")
		op := transactionOperation(t, sess)
		ref, err := e.Begin(op, session.TxOptions{Isolation: session.Serializable, ReadOnly: true, DeferrableSet: override, Deferrable: false})
		if err != nil {
			t.Fatal(err)
		}
		if sess.Transactions.Deferrable == override || !sess.Transactions.EntryDeferrable {
			t.Fatal("Go birth confused omitted and explicit deferrability overrides")
		}
		scope := &StatementScope{Owner: sess.Transactions, Op: op, TxRef: ref}
		scope.Owner.Mu.Lock()
		err = scope.AcquireSerializableSnapshot()
		scope.Owner.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		node := e.Visibility.Serializable.Nodes[ref]
		if node.Deferrable == override {
			t.Fatal("frozen acquisition classification lost inherited/explicit mode")
		}
		e.Mu.Unlock()
		if _, err := e.Rollback(op, ref); err != nil {
			t.Fatal(err)
		}
	}
}
