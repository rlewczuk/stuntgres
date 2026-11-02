package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

func freezeRetentionFixture(t *testing.T) (*core.Engine, *session.Session, ids.ObjectRef, *storage.Relation) {
	t.Helper()
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE freeze_retention(n integer,s text)")
	enginetest.Run(t, e, "INSERT INTO freeze_retention VALUES(1,'committed')")
	ref := enginetest.RoleTableRef(t, e, "freeze_retention")
	return e, enginetest.StartSession(t, e, nil), ref, e.Storage.Relations[ref.Address]
}

func freezeRetentionScope(t *testing.T, e *core.Engine, sess *session.Session) (*core.StatementScope, func(error) error) {
	t.Helper()
	op := enginetest.TransactionOperation(t, sess)
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	finished := false
	finish := func(primary error) error {
		err := scope.Finish(primary)
		if !finished {
			finished = true
			op.Done()
		}
		return err
	}
	t.Cleanup(func() {
		if !finished {
			_ = finish(errors.New("retention unit cleanup"))
		}
	})
	return scope, finish
}

func freezeRetentionExecute(t *testing.T, scope *core.StatementScope, sql string) {
	t.Helper()
	result, err := scope.Execute(enginetest.Statement(t, sql), 0, nil)
	if result.Rows != nil {
		defer result.Rows.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
}

func freezeRetentionAssert(t *testing.T, store *storage.Relation, expected ...storage.RowVersion) {
	t.Helper()
	remaining := make(map[ids.VersionID]storage.RowVersion, len(expected))
	for _, row := range expected {
		remaining[row.Version] = row
	}
	for prior := store.Discarded; prior != nil; prior = prior.Next {
		row := prior.Version
		want, ok := remaining[row.Version]
		if !ok {
			t.Fatalf("unexpected or duplicated discarded version %d", row.Version)
		}
		delete(remaining, row.Version)
		if row.Row != want.Row || row.Born != 0 || row.InsertFullXID != want.InsertFullXID || row.DeleteFullXID != want.DeleteFullXID || row.Base != want.Base || row.PrivateHistory != nil || row.IndexAdmission != nil || row.IndexOutputs != nil || len(row.Values) != len(want.Values) || len(row.Values) != 0 && &row.Values[0] != &want.Values[0] {
			t.Fatal("discard did not preserve exact identity/header and shared immutable payload", row, want)
		}
	}
	if len(remaining) != 0 {
		t.Fatal("genuine created versions disappeared", remaining)
	}
}

func TestPgsurgeryFreezeRetention(t *testing.T) {
	freezeRetentionComposed(t)
	t.Run("explicit_rollback_private_history", func(t *testing.T) {
		_, sess, ref, store := freezeRetentionFixture(t)
		before := *store
		for _, sql := range []string{"BEGIN", "UPDATE freeze_retention SET s='first'", "UPDATE freeze_retention SET s='second'", "INSERT INTO freeze_retention VALUES(2,'insert')"} {
			enginetest.SessionSQL(t, sess, sql)
		}
		rows := sess.Transactions.Writes[ref.Address]
		if len(rows) != 2 || rows[0].PrivateHistory == nil || rows[0].PrivateHistory.Version.Born != 0 {
			t.Fatal("missing genuine intermediate successor")
		}
		middle := rows[0].PrivateHistory.Version
		history := rows[0].PrivateHistory
		enginetest.TransactionEnd(t, sess, sess.Transactions.Ref, false)
		freezeRetentionAssert(t, store, rows[0], middle, rows[1])
		if store.Generation <= before.Generation || before.Discarded != nil || !storage.SameVersionRoot(store.Versions, before.Versions) || rows[0].PrivateHistory != history || history.Version.Version != middle.Version {
			t.Fatal("rollback mutated a captured root or failed to invalidate physical capture")
		}
		root, generation := store.Discarded, store.Generation
		sess.RequestEnd(session.CleanClose)
		if err := sess.WaitEnd(t.Context()); err != nil {
			t.Fatal(err)
		}
		if store.Discarded != root || store.Generation != generation {
			t.Fatal("empty session disposal retained versions twice")
		}
	})

	for _, explicit := range []bool{false, true} {
		name := "implicit_statement_abort"
		if explicit {
			name = "explicit_statement_abort"
		}
		t.Run(name, func(t *testing.T) {
			e, sess, ref, store := freezeRetentionFixture(t)
			var owned storage.RowVersion
			if explicit {
				enginetest.SessionSQL(t, sess, "BEGIN")
				enginetest.SessionSQL(t, sess, "INSERT INTO freeze_retention VALUES(2,'owned')")
				owned = sess.Transactions.Writes[ref.Address][0]
			}
			scope, finish := freezeRetentionScope(t, e, sess)
			freezeRetentionExecute(t, scope, "INSERT INTO freeze_retention VALUES(3,'stage')")
			stage := scope.Stage.Rows[ref.Address][0]
			before := *store
			primary := errors.New("failed after actual tuple creation")
			if err := finish(primary); err != primary {
				t.Fatal("Finish replaced primary error", err)
			}
			if explicit {
				freezeRetentionAssert(t, store, owned, stage)
				if sess.TransactionState() != session.TxAborted || sess.Transactions.Writes != nil {
					t.Fatal("explicit abort did not dispose owner")
				}
			} else {
				freezeRetentionAssert(t, store, stage)
				if sess.TransactionState() != session.TxIdle {
					t.Fatal("implicit abort did not end owner")
				}
			}
			root, generation := store.Discarded, store.Generation
			if scope.Finish(nil) != primary || store.Discarded != root || store.Generation != generation || before.Discarded != nil {
				t.Fatal("repeated Finish changed retained roots or old capture")
			}
		})
	}

	for _, failed := range []bool{false, true} {
		name := "savepoint_rewind"
		if failed {
			name = "savepoint_failed_statement"
		}
		t.Run(name, func(t *testing.T) {
			e, sess, ref, store := freezeRetentionFixture(t)
			for _, sql := range []string{"BEGIN", "UPDATE freeze_retention SET s='parent'", "SAVEPOINT retained", "UPDATE freeze_retention SET s='discarded'"} {
				enginetest.SessionSQL(t, sess, sql)
			}
			child := sess.Transactions.Writes[ref.Address][0]
			parent := sess.Transactions.Savepoints[0].Writes[ref.Address][0]
			e.Mu.Lock()
			ownerXID := e.Visibility.FullXIDLocked(sess.Transactions.Ref)
			parentHeaders := store.Headers
			e.Mu.Unlock()
			if ownerXID == 0 || parent.InsertFullXID != ownerXID || child.InsertFullXID != ownerXID || parent.DeleteFullXID != 0 || child.PrivateHistory.Version.DeleteFullXID != 0 || parentHeaders.Header(parent.Version).Xmax != ownerXID || parentHeaders.Header(parent.Version).Successor != child.Version {
				t.Fatal("accepted child lacks independent owner provenance or changed old parent capture")
			}
			before := *store
			var stage storage.RowVersion
			if failed {
				scope, finish := freezeRetentionScope(t, e, sess)
				freezeRetentionExecute(t, scope, "INSERT INTO freeze_retention VALUES(9,'failed suffix')")
				stage = scope.Stage.Rows[ref.Address][0]
				primary := errors.New("failed child")
				if finish(primary) != primary {
					t.Fatal("lost child error")
				}
				freezeRetentionAssert(t, store, child, stage)
			} else {
				enginetest.SessionSQL(t, sess, "ROLLBACK TO retained")
				freezeRetentionAssert(t, store, child)
			}
			if got := sess.Transactions.Writes[ref.Address][0]; got.Version != parent.Version || got.PrivateHistory != parent.PrivateHistory || &got.Values[0] != &parent.Values[0] || before.Discarded != nil {
				t.Fatal("restored parent/history was aborted, copied or mutated")
			}
			if failed {
				enginetest.SessionSQL(t, sess, "ROLLBACK TO retained")
			}
			enginetest.SessionSQL(t, sess, "UPDATE freeze_retention SET s='after rewind'")
			last := sess.Transactions.Writes[ref.Address][0]
			if child.PrivateHistory.Version.Version != parent.Version || last.PrivateHistory.Version.Version != parent.Version || child.PrivateHistory.Version.DeleteFullXID != 0 || last.PrivateHistory.Version.DeleteFullXID != ownerXID || parent.DeleteFullXID != 0 || parentHeaders.Header(parent.Version).Successor != child.Version || store.Headers.Header(parent.Version).Xmax != ownerXID || store.Headers.Header(parent.Version).Successor != last.Version {
				t.Fatal("reappend overwrote immutable history")
			}
			// The accepted reappend captured the actual raw end owner. The
			// savepoint's older scalar stays zero; rollback does not undo xmax.
			discardedParent := parent
			discardedParent.DeleteFullXID = ownerXID
			enginetest.SessionSQL(t, sess, "ROLLBACK")
			if failed {
				freezeRetentionAssert(t, store, child, stage, discardedParent, last)
			} else {
				freezeRetentionAssert(t, store, child, discardedParent, last)
			}
			if parent.DeleteFullXID != 0 || child.PrivateHistory.Version.DeleteFullXID != 0 || last.PrivateHistory.Version.DeleteFullXID != ownerXID {
				t.Fatal("disposal mutated immutable predecessor captures")
			}
		})
	}

	t.Run("session_end", func(t *testing.T) {
		e, sess, ref, store := freezeRetentionFixture(t)
		enginetest.SessionSQL(t, sess, "BEGIN")
		enginetest.SessionSQL(t, sess, "INSERT INTO freeze_retention VALUES(2,'session')")
		row := sess.Transactions.Writes[ref.Address][0]
		before := *store
		sess.RequestEnd(session.CleanClose)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		if err := sess.WaitEnd(ctx); err != nil {
			t.Fatal(err)
		}
		freezeRetentionAssert(t, store, row)
		if sess.Transactions.Writes != nil || before.Discarded != nil || e.Storage.Relations[ref.Address] != store {
			t.Fatal("session end lost canonical identity or changed capture")
		}
	})

	t.Run("prepare_transfers_abort_retains_after_preflight", func(t *testing.T) {
		e, sess, ref, store := freezeRetentionFixture(t)
		enginetest.SessionSQL(t, sess, "BEGIN")
		enginetest.SessionSQL(t, sess, "UPDATE freeze_retention SET s='prepared'")
		row := sess.Transactions.Writes[ref.Address][0]
		before := *store
		if err := preparedUnitControl(t, e, sess, parser.KindPrepareTransaction, "freeze_retention"); err != nil {
			t.Fatal(err)
		}
		entry := core.EnginePreparedRef(e).Lookup("freeze_retention")
		if entry == nil || entry.Writes[ref.Address][0].Version != row.Version || entry.Writes[ref.Address][0].PrivateHistory != row.PrivateHistory || store.Discarded != before.Discarded || store.Generation != before.Generation {
			t.Fatal("PREPARE aborted or copied the transferred tuple")
		}
		peer := enginetest.StartSession(t, e, nil)
		ctx, cancel := context.WithCancel(t.Context())
		op, err := peer.Acquire(ctx)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
		err = core.EngineCompletePrepared(e, op, "freeze_retention", false)
		op.Done()
		if !errors.Is(err, context.Canceled) || entry.Claimed || core.EnginePreparedRef(e).Lookup("freeze_retention") != entry || store.Discarded != before.Discarded || store.Generation != before.Generation {
			t.Fatal("rejected prepared abort published tuple retention", err)
		}
		if err := preparedUnitControl(t, e, peer, parser.KindRollbackPrepared, "freeze_retention"); err != nil {
			t.Fatal(err)
		}
		freezeRetentionAssert(t, store, row)
		if core.EnginePreparedRef(e).Lookup("freeze_retention") != nil || row.PrivateHistory == nil || before.Discarded != nil {
			t.Fatal("prepared abort kept ownership or mutated transferred history")
		}
	})

	t.Run("preinsert_failure_is_not_a_tuple", func(t *testing.T) {
		e, sess, _, store := freezeRetentionFixture(t)
		before := *store
		scope, finish := freezeRetentionScope(t, e, sess)
		_, err := scope.Execute(enginetest.Statement(t, "INSERT INTO freeze_retention VALUES(1/0,'rejected')"), 0, nil)
		if err == nil {
			t.Fatal("invalid input unexpectedly reached creation")
		}
		if len(scope.Stage.Rows) != 0 {
			t.Fatal("preinsert failure already created a tuple")
		}
		_ = finish(err)
		if store.Discarded != before.Discarded || store.Generation != before.Generation {
			t.Fatal("bound/rejected input became a retained physical tuple")
		}
	})

	t.Run("private_relation_is_not_installed", func(t *testing.T) {
		e, sess, _, store := freezeRetentionFixture(t)
		enginetest.SessionSQL(t, sess, "BEGIN")
		enginetest.SessionSQL(t, sess, "CREATE TABLE freeze_private(n integer)")
		enginetest.SessionSQL(t, sess, "INSERT INTO freeze_private VALUES(1)")
		var address ids.ObjectAddress
		for a, change := range sess.Transactions.Catalog.Storage {
			if change.Relation != nil && change.Relation.Name() == "freeze_private" {
				address = a
			}
		}
		if address == (ids.ObjectAddress{}) || len(sess.Transactions.Writes[address]) != 1 {
			t.Fatal("missing genuine private relation/write")
		}
		enginetest.SessionSQL(t, sess, "ROLLBACK")
		if e.Storage.Relations[address] != nil || e.CatalogRead().Get(address) != nil || store.Discarded != nil {
			t.Fatal("rollback exported private-only heap/catalog into installed storage")
		}
	})
}
