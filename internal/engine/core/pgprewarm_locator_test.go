package core_test

import (
 "context"
 "errors"
 "reflect"
 "runtime"
 "strings"
 "sync/atomic"
 "testing"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/storage"
)

// Cancel at the final overlay check after private storage preparation, not at
// ordinary operation preflight. The actual production frame owns this boundary.
type prewarmOverlayCancel struct { context.Context; cancel context.CancelFunc; reached atomic.Bool }
func (c *prewarmOverlayCancel) Err() error {
 if err := c.Context.Err(); err != nil { return err }
 var pcs [64]uintptr
 frames := runtime.CallersFrames(pcs[:runtime.Callers(1, pcs[:])])
 nestedCore := false
 for {
  frame, more := frames.Next()
  if strings.HasSuffix(frame.Function, "core.(*Engine).StageCatalogOverlay") {
   if !nestedCore { c.reached.Store(true); c.cancel(); return c.Context.Err() }
   break
  }
  nestedCore = nestedCore || strings.Contains(frame.Function, "/engine/core.")
  if !more { break }
 }
 return c.Context.Err()
}

type prewarmPrivateIncarnation struct {
 node ids.OID
 generation uint64
 store *storage.Relation
 version *storage.RowVersion
 length int
 headers *storage.HeapHeaderRoot
 ends *storage.DeletionRoot
 visibility *storage.VisibilityRoot
 auxiliary storage.AuxiliaryForks
}
func prewarmPrivateIdentity(t *testing.T, e *core.Engine, actor *session.Session, address ids.ObjectAddress) prewarmPrivateIncarnation {
 t.Helper()
 actor.Transactions.Mu.Lock()
 defer actor.Transactions.Mu.Unlock()
 e.Mu.Lock()
 defer e.Mu.Unlock()
 journal := actor.Transactions.Catalog
 view := e.PrivateCatalogLocked(journal)
 relation := view.MustRelation(address)
 store := e.PrivateStorageLocked(journal, address)
 result := prewarmPrivateIncarnation{node: relation.EffectiveStorageLocator(view), generation: store.Generation, store: store, length: len(store.Versions), headers: store.Headers, ends: store.Ends, visibility: store.Visibility, auxiliary: store.Auxiliary}
 if len(store.Versions) != 0 { result.version = &store.Versions[0] }
 return result
}
func prewarmLocatorFailure(t *testing.T, e *core.Engine, actor *session.Session, ctx context.Context, sql string) error {
 t.Helper()
 // Acquire owns its own effective context envelope. Install the observer on
 // that admitted operation, like the existing publication-boundary fixtures;
 // a caller Err override is not the operation's effective Err method.
 gate, observed := ctx.(*prewarmOverlayCancel)
 if observed { ctx = gate.Context }
 op, err := actor.Acquire(ctx)
 if err != nil { return err }
 defer op.Done()
 if observed { gate.Context = op.Context(); op.Ctx = gate }
 scope, err := e.StartStatement(op)
 if err != nil { return err }
 result, err := scope.Execute(enginetest.Statement(t, sql), 0, nil)
 if result.Rows != nil { result.Rows.Close() }
 return scope.Finish(err)
}

func TestPgPrewarmPrivateLocator(t *testing.T) {
 e, ref, _ := prewarmOwnerEngine(t)
 actor, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
 enginetest.SessionSQL(t, actor, "VACUUM (FREEZE) prewarm_heap")
 enginetest.SessionSQL(t, actor, "SELECT pg_prewarm('prewarm_heap','buffer','main',1,2)")
 for _, fork := range []string{"fsm", "vm"} {
  enginetest.TransactionRows(t, actor, "SELECT pg_prewarm('prewarm_heap','buffer','"+fork+"',0,0)", 1)
 }
 committed := prewarmPrivateIdentity(t, e, actor, ref.Address)
 committedView := e.CatalogRead()
 committedDescriptor := committedView.MustRelation(ref.Address)
 before := prewarmOwnerCoordinates(t, e, observer, committed.node)
 if len(before) != 4 || before[[2]int64{1,0}] != ([2]int64{1,0}) || before[[2]int64{2,0}] != ([2]int64{1,0}) { t.Fatal("missing genuine committed main/auxiliary preload", before) }
 enginetest.SessionSQL(t, actor, "BEGIN")
 defer func() { if actor.TransactionState() != session.TxIdle { enginetest.SessionSQL(t, actor, "ROLLBACK") } }()
 enginetest.SessionSQL(t, actor, "TRUNCATE prewarm_heap")
 enginetest.SessionSQL(t, actor, "INSERT INTO prewarm_heap VALUES(1)")
 first := prewarmPrivateIdentity(t, e, actor, ref.Address)
 if first.node == 0 || first.node != committed.node || first.store == committed.store || first.generation <= committed.generation || first.length != 0 || !first.auxiliary.Valid() || first.auxiliary.FSM || first.auxiliary.VM { t.Fatal("rewrite did not create distinct private storage with reset fork facts", committed, first) }
 enginetest.SessionSQL(t, actor, "SELECT pg_prewarm('prewarm_heap')")
 if got := prewarmOwnerCoordinates(t, e, observer, committed.node); !reflect.DeepEqual(got, before) { t.Fatal("private rewrite retired/touched committed occupancy", got, before) }
 enginetest.SessionSQL(t, actor, "ALTER TABLE prewarm_heap ALTER COLUMN k SET DEFAULT 1")
 if metadata := prewarmPrivateIdentity(t, e, actor, ref.Address); metadata != first { t.Fatal("metadata reminted private incarnation", metadata, first) }
 enginetest.SessionSQL(t, actor, "SAVEPOINT first_rewrite")
 enginetest.SessionSQL(t, actor, "TRUNCATE prewarm_heap")
 second := prewarmPrivateIdentity(t, e, actor, ref.Address)
 if second.node != first.node || second.store == first.store || second.store == committed.store || second.generation <= first.generation || second.length != 0 { t.Fatal("later rewrite reused prior private storage/root authority", first, second) }
 enginetest.SessionSQL(t, actor, "ROLLBACK TO first_rewrite")
 if restored := prewarmPrivateIdentity(t, e, actor, ref.Address); restored != first { t.Fatal("savepoint lost exact private descriptor/storage/provenance", restored, first) }
 enginetest.SessionSQL(t, actor, "SAVEPOINT canceled_rewrite")
 cancelContext, cancel := context.WithCancel(t.Context())
 defer cancel()
 cancellation := &prewarmOverlayCancel{Context: cancelContext, cancel: cancel}
 if err := prewarmLocatorFailure(t, e, actor, cancellation, "TRUNCATE prewarm_heap"); !errors.Is(err, context.Canceled) || !cancellation.reached.Load() { t.Fatal("actual final private publication cancellation", cancellation.reached.Load(), err) }
 enginetest.SessionSQL(t, actor, "ROLLBACK TO canceled_rewrite")
 if restored := prewarmPrivateIdentity(t, e, actor, ref.Address); restored != first { t.Fatal("canceled candidate changed admitted incarnation", restored, first) }
 enginetest.SessionSQL(t, actor, "SAVEPOINT failed_rewrite")
 if err := prewarmLocatorFailure(t, e, actor, t.Context(), "TRUNCATE prewarm_heap,prewarm_missing_heap"); err == nil { t.Fatal("missing target did not reject entire rewrite") }
 enginetest.SessionSQL(t, actor, "ROLLBACK TO failed_rewrite")
 if restored := prewarmPrivateIdentity(t, e, actor, ref.Address); restored != first { t.Fatal("failed candidate changed private incarnation", restored, first) }
 if got := prewarmOwnerCoordinates(t, e, observer, committed.node); !reflect.DeepEqual(got, before) { t.Fatal("failed/canceled publication changed incumbent cache", got, before) }
 e.Mu.Lock()
 incumbent := e.Storage.Relations[ref.Address]
 incumbentUnchanged := e.Catalog.Current().MustRelation(ref.Address) == committedDescriptor && incumbent == committed.store && incumbent.Generation == committed.generation && len(incumbent.Versions) == committed.length && &incumbent.Versions[0] == committed.version && incumbent.Headers == committed.headers && incumbent.Ends == committed.ends && incumbent.Visibility == committed.visibility && incumbent.Auxiliary == committed.auxiliary
 e.Mu.Unlock()
 if !incumbentUnchanged { t.Fatal("failed/canceled private publication changed committed descriptor/storage/forks") }
 // Generation fences row/end vectors, not physical locator identity.
 // This controlled candidate has one pending INSERT and an empty private
 // root; canonical commit publishes that one row and advances Generation once.
 actor.Transactions.Mu.Lock()
 e.Mu.Lock()
 pending := actor.Transactions.Writes[ref.Address]
 pendingCount, privateRows, privateGeneration := len(pending), len(first.store.Versions), first.store.Generation
 var pendingRow storage.RowVersion
 if pendingCount == 1 { pendingRow = pending[0] }
 frontier, insertionXID := e.Visibility.CommitSeq(), e.Visibility.FullXIDLocked(actor.Transactions.Ref)
 staged := actor.Transactions.Catalog.Storage[ref.Address]
 e.Mu.Unlock()
 actor.Transactions.Mu.Unlock()
 if pendingCount != 1 || privateRows != 0 || privateGeneration != first.generation || !staged.Rewritten || staged.Replacement != first.store || staged.Original != committed.store { t.Fatal("missing exact admitted storage/generation and pending-row authority", pendingCount, privateRows, privateGeneration, staged) }
 enginetest.SessionSQL(t, actor, "COMMIT")
 published := prewarmPrivateIdentity(t, e, actor, ref.Address)
 if published.node == 0 || published.node == committed.node || published.store != first.store || published.store != staged.Replacement { t.Fatal("commit failed fresh locator or exact accepted store retention", published, first) }
 if published.generation != privateGeneration+1 { t.Fatal("pending INSERT did not advance row-root generation exactly once", published.generation, privateGeneration) }
 actor.Transactions.Mu.Lock()
 journalCleared := actor.Transactions.Catalog == nil
 actor.Transactions.Mu.Unlock()
 if !journalCleared { t.Fatal("commit retained active private journal") }
 if len(published.store.Versions) != 1 { t.Fatal("commit did not publish exactly the pending INSERT", published.store.Versions) }
 committedRow := published.store.Versions[0]
 if committedRow.Row != pendingRow.Row || committedRow.Version != pendingRow.Version || !reflect.DeepEqual(committedRow.Values, pendingRow.Values) || committedRow.Born != frontier+1 || committedRow.InsertFullXID != insertionXID || committedRow.Base != 0 { t.Fatal("row publication lost actual reserved identity/payload/commit provenance", committedRow, pendingRow) }
 if got := prewarmOwnerCoordinates(t, e, observer, committed.node); len(got) != 0 { t.Fatal("commit retained old incarnation coordinates", got) }
 if got := prewarmOwnerCoordinates(t, e, observer, published.node); len(got) != 0 { t.Fatal("commit exported private preload occupancy", got) }
 enginetest.SessionSQL(t, actor, "SELECT pg_prewarm('prewarm_heap')")
 warmed := prewarmOwnerCoordinates(t, e, observer, published.node)
 if !reflect.DeepEqual(warmed, map[[2]int64][2]int64{{0,0}: {1,0}}) { t.Fatal("fresh committed invocation missed published locator", warmed) }
 enginetest.SessionSQL(t, actor, "BEGIN")
 enginetest.SessionSQL(t, actor, "TRUNCATE prewarm_heap")
 abandoned := prewarmPrivateIdentity(t, e, actor, ref.Address)
 if abandoned.node != published.node || abandoned.store == published.store || abandoned.generation <= published.generation { t.Fatal("next transaction did not isolate actual private replacement", abandoned, published) }
 enginetest.SessionSQL(t, actor, "ROLLBACK")
 if restored := prewarmPrivateIdentity(t, e, actor, ref.Address); restored != published { t.Fatal("rollback changed committed incarnation/backing", restored, published) }
 if got := prewarmOwnerCoordinates(t, e, observer, published.node); !reflect.DeepEqual(got, warmed) { t.Fatal("rollback retired committed cache", got, warmed) }
}
