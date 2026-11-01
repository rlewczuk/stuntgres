package core_test

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// A directly constructed physical helper keeps this a runtime ownership unit.
func deferredUnitHelper(t *testing.T, e *core.Engine, source *catalog.Relation) *catalog.Relation {
	t.Helper()
	return deferredUnitNamedHelper(t, e, source, "deferred_lock_helper", 9500020)
}

func deferredUnitNamedHelper(t *testing.T, e *core.Engine, source *catalog.Relation, name string, oid ids.OID) *catalog.Relation {
	t.Helper()
	helper := *source
	helper.ObjectAddress.ObjectID, helper.ObjectName = oid, name
	helper.Triggers = nil
	shape, err := sqltype.Composite(sqltype.ID(oid+1), helper.Name(), "public."+helper.Name(), 0, []sqltype.CompositeMember{
		{Attribute: 1, Name: "id", Usage: helper.Columns[0].Usage}, {Attribute: 2, Name: "v", Usage: helper.Columns[1].Usage},
	})
	if err != nil {
		t.Fatal(err)
	}
	helper.RowType = shape.ID()
	typ := &catalog.Type{SQLType: shape, ObjectNamespace: helper.Namespace(), ObjectRevision: 1, Owner: helper.Owner, OwningRelation: helper.Address()}
	e.Mu.Lock()
	next := catalog.CloneState(e.Catalog.State)
	next.SetDescriptor(helper.Address(), &helper)
	next.BindName(catalog.DescriptorNameKey(&helper), helper.Address())
	next.SetDescriptor(typ.Address(), typ)
	next.BindName(catalog.DescriptorNameKey(typ), typ.Address())
	e.Catalog.PublishLocked(next)
	e.Storage.Relations[helper.Address()] = &storage.Relation{Generation: 1}
	e.Mu.Unlock()
	return &helper
}

func TestTriggerDeferredCancellation(t *testing.T) {
	for _, isolation := range []session.Isolation{session.ReadCommitted, session.RepeatableRead} {
		t.Run(session.IsolationText(isolation), func(t *testing.T) {
			deferredCancellation(t, isolation)
		})
	}
}

func deferredCancellation(t *testing.T, isolation session.Isolation) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e, s, read, relation, procedure := triggerUnitFixtureContext(t, ctx,
		"BEGIN INSERT INTO deferred_progress_helper VALUES(NEW.id,NEW.v); INSERT INTO deferred_lock_helper VALUES(NEW.id,NEW.v); RETURN NEW; END")
	helper := deferredUnitHelper(t, e, relation)
	progress := deferredUnitNamedHelper(t, e, relation, "deferred_progress_helper", 9500022)
	relation = triggerUnitInstall(t, e, relation, deferredUnitDefinition(t, read, relation, procedure))
	triggerUnitAdmit(t, s, relation)
	s.Owner.Isolation = isolation
	s.Owner.State = session.TxActive
	// Direct physical acceptance bypasses SQL dispatch. Admit its actual data
	// snapshot through the same phase used by execution and terminal settlement.
	if err := core.ScopeSnapshotDataPhase(s); err != nil {
		t.Fatal(err)
	}
	deferredUnitAccept(t, s, read, relation, 1)
	acceptedSnapshot := core.ScopeCurrentSnapshot(s)
	if acceptedSnapshot == nil {
		t.Fatal("accepted mutation lacks its own command snapshot")
	}
	ref := s.TxRef
	if err := s.Finish(nil); err != nil {
		t.Fatal(err)
	}
	payload := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Owner)).Events[0].UserTrigger
	holder := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, holder, "BEGIN")
	defer enginetest.SessionSQL(t, holder, "ROLLBACK")
	enginetest.SessionSQL(t, holder, "LOCK TABLE deferred_lock_helper IN ACCESS EXCLUSIVE MODE")
	helperRef := e.Catalog.Domain.Ref(read.Epoch(), helper.Address())
	lease, sqlFinished := s.Op.Lease, s.Op.SQLFinished
	done := make(chan error, 1)
	go func() { _, err := e.Commit(s.Op, ref); done <- err }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("terminal worker did not join")
			}
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	stacks := make([]byte, 64<<10)
	for {
		select {
		case err := <-done:
			joined = true
			t.Fatal("terminal body ended before real helper admission", err)
		default:
		}
		pending := false
		state := e.Locks.Inspect().Resources[lock.Resource{ObjectRef: helperRef}]
		if state != nil {
			for _, waiter := range state.Pending {
				if waiter.Mode == lock.RowExclusive && !waiter.Terminal {
					pending = true
					break
				}
			}
		}
		n := runtime.Stack(stacks, true)
		if n == len(stacks) {
			stacks = make([]byte, len(stacks)*2)
			continue
		}
		parked := false
		for _, stack := range bytes.Split(stacks[:n], []byte("\n\n")) {
			if bytes.Contains(stack, []byte("(*relationRequest).wait")) && bytes.Contains(stack, []byte("(*Engine).Commit")) {
				parked = true
				break
			}
		}
		if pending && parked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal body never reached the admitted helper wait")
		}
		runtime.Gosched()
	}
	// The actual trigger has staged its first command and is now parked at the
	// next command's relation admission. No runtime mutex may span that wait.
	if !e.Mu.TryLock() {
		t.Fatal("terminal evaluator held Engine.Mu")
	}
	e.Mu.Unlock()
	if !s.Owner.Mu.TryLock() {
		t.Fatal("terminal evaluator held owner mutex")
	}
	s.Owner.Mu.Unlock()
	if !s.Op.Session.Mu.TryLock() {
		t.Fatal("terminal evaluator held session mutex")
	}
	terminal := core.ActiveScopeOfLease(s.Op.Lease)
	s.Op.Session.Mu.Unlock()
	if terminal == nil {
		t.Fatal("executing terminal body has no active operation lease")
	}
	if terminal == s || terminal.Owner != s.Owner || terminal.TxRef != ref || terminal.CatalogAuthority == nil {
		t.Fatalf("terminal ownership mismatch: terminal=%p original=%p ownerMatch=%t txMatch=%t authority=%t", terminal, s, terminal.Owner == s.Owner, terminal.TxRef == ref, terminal.CatalogAuthority != nil)
	}
	if rows := terminal.Stage.Rows[progress.Address()]; len(rows) != 1 || enginetest.Export(rows[0].Values[0]) != int64(1) || enginetest.Export(rows[0].Values[1]) != int64(10) {
		t.Fatal("terminal body did not stage its earlier admitted command", rows)
	}
	// currentSnapshot is lexical command scratch, not ambient terminal state.
	// Volatile RC commands capture only after target admission; stable commands
	// must inherit exactly the transaction's original immutable snapshot.
	current := core.ScopeCurrentSnapshot(terminal)
	if isolation == session.ReadCommitted {
		if current != nil {
			t.Fatal("RC nested target wait selected a pre-admission snapshot", current)
		}
	} else if current == nil || current != acceptedSnapshot || current != s.Owner.CurrentSnapshot {
		t.Fatal("stable nested target wait lost the terminal snapshot", current, acceptedSnapshot, s.Owner.CurrentSnapshot)
	}
	if len(e.Storage.Relations[relation.Address()].Versions) != 0 || len(e.Storage.Relations[helper.Address()].Versions) != 0 || len(e.Storage.Relations[progress.Address()].Versions) != 0 {
		t.Fatal("blocked terminal body exposed original or helper writes")
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatal("terminal cancellation lost", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled terminal worker did not unwind")
	}
	if s.Op.Lease != lease || s.Op.SQLFinished != sqlFinished || !terminal.Finished || terminal.CatalogAuthority != nil || core.ScopeIndexesSlot(terminal) != nil {
		t.Fatal("canceled terminal retained or failed to restore operation ownership")
	}
	if s.Owner.State != session.TxIdle || s.Owner.Ref == ref || !core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(s.Owner)).Zero() || s.Op.EffectiveUser != nil {
		t.Fatal("canceled terminal retained old incarnation, event or authority")
	}
	if n, _ := payload.Next[1].Integer(); n != 10 {
		t.Fatal("unwind mutated accepted payload backing", n)
	}
	enginetest.SessionSQL(t, holder, "ROLLBACK")
	enginetest.LockEmpty(t, e)
	if len(e.Storage.Relations[relation.Address()].Versions) != 0 || len(e.Storage.Relations[helper.Address()].Versions) != 0 || len(e.Storage.Relations[progress.Address()].Versions) != 0 {
		t.Fatal("canceled terminal published a partial frontier")
	}
	// Check the same physical owner is reusable with a fresh live operation.
	s.Op.Done()
	op, err := s.Op.Session.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	next, err := e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if next == ref || next == (ids.TxRef{}) {
		t.Fatal("did not install fresh incarnation")
	}
	if _, err := e.Rollback(op, next); err != nil {
		t.Fatal(err)
	}
}

// The borrowed native scope is disposed before Commit returns. Use the actual
// terminal-settlement entry on a retained live scope to observe restoration of
// its exact ambient frontier after volatile child commands, on both exits.
func TestTriggerDeferredTerminalSnapshotUnwind(t *testing.T) {
	for _, isolation := range []session.Isolation{session.ReadCommitted, session.RepeatableRead} {
		t.Run(session.IsolationText(isolation), func(t *testing.T) {
			for _, outcome := range []string{"success", "error"} {
				t.Run(outcome, func(t *testing.T) {
					body := "BEGIN INSERT INTO deferred_lock_helper VALUES(NEW.id,NEW.v); "
					if outcome == "error" {
						body += "PERFORM 1/0; "
					}
					body += "RETURN NEW; END"
					e, s, read, relation, procedure := triggerUnitFixture(t, body)
					helper := deferredUnitHelper(t, e, relation)
					relation = triggerUnitInstall(t, e, relation, deferredUnitDefinition(t, read, relation, procedure))
					triggerUnitAdmit(t, s, relation)
					s.Owner.Isolation = isolation
					s.Owner.State = session.TxActive
					if err := core.ScopeSnapshotDataPhase(s); err != nil {
						t.Fatal(err)
					}
					deferredUnitAccept(t, s, read, relation, 1)
					snapshot, frontier := core.ScopeCurrentSnapshot(s), core.ScopeSnapshotFrontier(s)
					if snapshot == nil {
						t.Fatal("terminal opening lacks an admitted snapshot")
					}
					err := core.ScopeSettleTriggerTerminalTest(s)
					if outcome == "error" {
						var diagnostic interface{ SQLState() string }
						if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "22012" {
							t.Fatal("terminal did not propagate the actual child failure", err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
					if core.ScopeCurrentSnapshot(s) != snapshot || core.ScopeSnapshotFrontier(s) != frontier || s.CatalogAuthority == nil {
						t.Fatal("child unwind replaced the terminal's ambient snapshot or authority")
					}
					if outcome == "error" {
						if len(s.Stage.Rows[relation.Address()]) != 1 || len(s.Stage.Rows[helper.Address()]) != 1 {
							t.Fatal("failed child lost the terminal's private rollback frontier")
						}
					} else if len(s.Owner.Writes[relation.Address()]) != 1 || len(s.Owner.Writes[helper.Address()]) != 1 || len(s.Stage.Rows) != 0 {
						t.Fatal("successful settlement failed to transfer both real row effects once")
					}
					if len(e.Storage.Relations[relation.Address()].Versions) != 0 || len(e.Storage.Relations[helper.Address()].Versions) != 0 {
						t.Fatal("snapshot restoration published the private terminal frontier")
					}
					if err := s.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
						t.Fatal("terminal cleanup replaced cancellation", err)
					}
					if core.ScopeCurrentSnapshot(s) != nil || s.CatalogAuthority != nil || core.ScopeIndexesSlot(s) != nil {
						t.Fatal("finished terminal retained snapshot or executable authority")
					}
				})
			}
		})
	}
}
