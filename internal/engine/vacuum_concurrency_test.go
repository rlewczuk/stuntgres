package engine

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"sync"
	"testing"
	"time"
)

// A test-owned Context parks its actual caller at a factored maintenance step.
// Production has no arbitrary hook/worker/queue-observation API for these tests.
type vacuumBarrierContext struct {
	context.Context
	function          string
	arrived, release  chan struct{}
	once, releaseOnce sync.Once
}

func (c *vacuumBarrierContext) Err() error {
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, c.function) {
			c.once.Do(func() {
				close(c.arrived)
				select {
				case <-c.release:
				case <-c.Context.Done():
				}
			})
			break
		}
		if !more {
			break
		}
	}
	return c.Context.Err()
}
func (c *vacuumBarrierContext) resume() { c.releaseOnce.Do(func() { close(c.release) }) }

type vacuumTask struct {
	barrier *vacuumBarrierContext
	done    <-chan error
	cancel  context.CancelFunc
	joined  bool
}

func startVacuumBarrier(t *testing.T, e *Engine, s *session.Session, q, function string) *vacuumTask {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	op, err := s.Acquire(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	barrier := &vacuumBarrierContext{Context: op.Context(), function: function, arrived: make(chan struct{}), release: make(chan struct{})}
	// Wrap the actual admitted operation context, not its parent: Session's
	// cancelable child otherwise intentionally does not delegate Err to parents.
	op.Ctx = barrier
	done := make(chan error, 1)
	task := &vacuumTask{barrier: barrier, cancel: cancel, done: done}
	go func() {
		result, err := e.Execute(op, mustLockStatement(q), 0, nil)
		if result.Rows != nil {
			result.Rows.Close()
		}
		op.Done()
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		barrier.resume()
		if !task.joined {
			select {
			case <-task.done:
				task.joined = true
			case <-time.After(5 * time.Second):
				t.Error("maintenance worker did not join")
			}
		}
	})
	select {
	case <-barrier.arrived:
	case err := <-task.done:
		task.joined = true
		t.Fatal("maintenance completed before actual step", function, err)
	case <-time.After(5 * time.Second):
		t.Fatal("maintenance step not entered", function)
	}
	return task
}
func (task *vacuumTask) join(t *testing.T, want error) {
	t.Helper()
	task.barrier.resume()
	select {
	case err := <-task.done:
		task.joined = true
		if !errors.Is(err, want) {
			t.Fatalf("maintenance err=%v want=%v", err, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("maintenance did not finish")
	}
}

func TestVacuumPublicationFences(t *testing.T) {
	t.Run("late_completion", vacuumLateCompletion)
	t.Run("replacement_wait", vacuumReplacementWait)
	for _, mutation := range []string{"append", "update", "delete", "snapshot", "comment", "comment_wait", "cancel", "replace"} {
		t.Run(mutation, func(t *testing.T) {
			e := New()
			writer, worker, peer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE fence(id INT)", "INSERT INTO fence VALUES(1),(2),(3)", "DELETE FROM fence WHERE id=1"} {
				sessionSQL(t, writer, q)
			}
			ref, store := vacuumStore(t, e, "fence")
			root, ends, generation, stats := store.Versions, store.Ends, store.Generation, store.Maintenance
			task := startVacuumBarrier(t, e, worker, "VACUUM fence", "storage.InspectVacuum")
			var commentDone <-chan error
			commentJoined := false
			switch mutation {
			case "append":
				sessionSQL(t, peer, "INSERT INTO fence VALUES(4)")
			case "update":
				sessionSQL(t, peer, "UPDATE fence SET id=4 WHERE id=3")
			case "delete":
				sessionSQL(t, peer, "DELETE FROM fence WHERE id=3")
			case "snapshot":
				sessionSQL(t, peer, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				transactionRows(t, peer, "SELECT id FROM fence ORDER BY id", 2, 3)
			case "comment":
				// SQL COMMENT needs an incompatible relation lock. Exercise a
				// genuine metadata-only host publication during the detached scan;
				// the separate comment_wait case proves SQL lock ordering.
				base := e.CatalogRead()
				next := *base.MustRelation(ref.Address)
				next.ObjectRevision++
				next.Comment = "compatible"
				stage := e.BeginCatalogStage(base)
				defer e.DiscardCatalogStage(stage)
				if err := stage.Replace(&next); err != nil {
					t.Fatal(err)
				}
				prepared, err := e.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := publishCatalog(e, prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
					t.Fatal(err)
				}
			case "comment_wait":
				ctx, cancel := context.WithCancel(context.Background())
				commentDone = lockAsync(e, peer, ctx, "COMMENT ON TABLE fence IS 'compatible'", false)
				t.Cleanup(func() {
					cancel()
					if !commentJoined {
						select {
						case <-commentDone:
							commentJoined = true
						case <-time.After(5 * time.Second):
							t.Error("queued COMMENT did not join")
						}
					}
				})
				lockPending(t, e, 1)
			case "cancel":
				task.cancel()
			case "replace":
				if err := Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			}
			switch mutation {
			case "cancel":
				task.join(t, context.Canceled)
			case "replace":
				task.join(t, ids.ErrCatalogReplaced)
			default:
				task.join(t, nil)
			}
			if commentDone != nil {
				select {
				case err := <-commentDone:
					commentJoined = true
					if err != nil {
						t.Fatal("queued COMMENT failed after maintenance release", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("queued COMMENT did not finish after maintenance release")
				}
			}
			if mutation == "cancel" {
				if !storage.SameVersionRoot(root, store.Versions) || store.Ends != ends || store.Generation != generation || store.Maintenance != stats {
					t.Fatal("canceled candidate published")
				}
			}
			if mutation == "snapshot" {
				if len(store.Versions) != 3 {
					t.Fatal("new relevant owner did not invalidate removal")
				}
				sessionSQL(t, peer, "ROLLBACK")
				sessionSQL(t, writer, "VACUUM fence")
				if len(store.Versions) != 2 {
					t.Fatal("post-snapshot quiescent retirement")
				}
			}
			if mutation == "append" && len(store.Versions) != 3 || mutation == "update" && len(store.Versions) != 2 || mutation == "delete" && len(store.Versions) != 1 {
				t.Fatal("peer DML was lost/replayed", mutation, len(store.Versions))
			}
			if mutation == "comment" || mutation == "comment_wait" {
				current, _ := e.CatalogRead().Relation(ref.Address)
				if current.Comment != "compatible" {
					t.Fatal("maintenance clobbered compatible metadata")
				}
			}
			e.Mu.Lock()
			progress := len(e.VacuumProgress)
			e.Mu.Unlock()
			if progress != 0 {
				t.Fatal("residual progress", progress)
			}
			lockEmpty(t, e)
		})
	}
	t.Run("final_preinstall_cancel", func(t *testing.T) {
		e := New()
		s, worker := sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE boundary(id INT)")
		sessionSQL(t, s, "INSERT INTO boundary VALUES(1),(2)")
		sessionSQL(t, s, "DELETE FROM boundary WHERE id=1")
		_, store := vacuumStore(t, e, "boundary")
		root, ends, stats := store.Versions, store.Ends, store.Maintenance
		task := startVacuumBarrier(t, e, worker, "VACUUM boundary", "vacuumCompletion")
		task.cancel()
		task.join(t, context.Canceled)
		if !storage.SameVersionRoot(root, store.Versions) || store.Ends != ends || store.Maintenance != stats {
			t.Fatal("final canceled work installed")
		}
		sessionSQL(t, s, "VACUUM boundary")
		if len(store.Versions) != 1 || store.Maintenance.VacuumCount != 1 {
			t.Fatal("healthy final installation")
		}
	})
}

func TestVacuumAuthorizationLocks(t *testing.T) {
	t.Run("canonical_checkpoint", vacuumLeaseCheckpoint)
	t.Run("grant_time_recheck", vacuumGrantTimeRecheck)
	for _, mode := range []lock.Mode{lock.AccessExclusive, lock.Share} {
		t.Run(map[lock.Mode]string{lock.AccessExclusive: "selection_AS", lock.Share: "work_SUE"}[mode], func(t *testing.T) {
			e := New()
			holder, worker := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, holder, "CREATE TABLE target(id INT)")
			ref, _ := vacuumStore(t, e, "target")
			transactionBegin(t, holder)
			if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: ref}, mode); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := lockAsync(e, worker, ctx, "VACUUM target", false)
			joined := false
			defer func() {
				cancel()
				if !joined {
					lockAwait(t, done, context.Canceled)
				}
			}()
			lockPending(t, e, 1)
			snapshot := e.Locks.Inspect()
			entry := snapshot.Resources[lock.Resource{ObjectRef: ref}]
			want := lock.AccessShare
			if mode == lock.Share {
				want = lock.ShareUpdateExclusive
			}
			if entry == nil || len(entry.Pending) != 1 || entry.Pending[0].Mode != want {
				t.Fatal("wrong actual maintenance lock", entry)
			}
			e.Mu.Lock()
			active := len(e.VacuumProgress)
			e.Mu.Unlock()
			if active != 0 {
				t.Fatal("fake progress while waiting")
			}
			cancel()
			lockAwait(t, done, context.Canceled)
			joined = true
			sessionSQL(t, holder, "ROLLBACK")
			lockEmpty(t, e)
		})
	}
	t.Run("per_relation_lease_and_preserved_completion", func(t *testing.T) {
		e := New()
		owner, holder, worker := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, owner, "CREATE TABLE first_target(id INT)")
		sessionSQL(t, owner, "CREATE TABLE second_target(id INT)")
		ref, second := vacuumStore(t, e, "second_target")
		_, first := vacuumStore(t, e, "first_target")
		transactionBegin(t, holder)
		if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.Share); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := lockAsync(e, worker, ctx, "VACUUM first_target,second_target", false)
		joined := false
		defer func() {
			cancel()
			if !joined {
				lockAwait(t, done, context.Canceled)
			}
		}()
		lockPending(t, e, 1)
		e.Mu.Lock()
		firstCount, secondCount := first.Maintenance.VacuumCount, second.Maintenance.VacuumCount
		e.Mu.Unlock()
		if firstCount != 1 || secondCount != 0 {
			t.Fatal("per-target completion not visible", firstCount, secondCount)
		}
		// The completed first relation must no longer own SUE while second waits.
		firstRef := catalogRef(t, e, e.CatalogRead(), "first_target")
		if entry := e.Locks.Inspect().Resources[lock.Resource{ObjectRef: firstRef}]; entry != nil {
			for _, hold := range entry.Holders {
				if hold.Mode&lock.ShareUpdateExclusive != 0 {
					t.Fatal("first target retained SUE")
				}
			}
		}
		cancel()
		lockAwait(t, done, context.Canceled)
		joined = true
		sessionSQL(t, holder, "ROLLBACK")
		if first.Maintenance.VacuumCount != 1 || second.Maintenance.VacuumCount != 0 {
			t.Fatal("later cancellation rolled back earlier relation")
		}
		lockEmpty(t, e)
	})
}
