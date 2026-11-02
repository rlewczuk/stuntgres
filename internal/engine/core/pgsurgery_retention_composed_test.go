package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/contrib"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

func freezeRetentionNested(t *testing.T, scope *core.StatementScope, sql string) {
	t.Helper()
	restore := scope.ActivateSQLRoutine(nil, scope.Engine().CatalogRead())
	result, err := scope.Execute(enginetest.Statement(t, sql), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	err = restore(err)
	if err != nil {
		t.Fatal(err)
	}
}

func freezeRetentionProtectedStage(t *testing.T) {
	e, sess, ref, store := freezeRetentionFixture(t)
	enginetest.SessionSQL(t, sess, "BEGIN")
	scope, finish := freezeRetentionScope(t, e, sess)
	freezeRetentionExecute(t, scope, "INSERT INTO freeze_retention VALUES(2,'prefix')")
	prefix := scope.Stage.Rows[ref.Address][0]
	mark, err := scope.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	freezeRetentionNested(t, scope, "INSERT INTO freeze_retention VALUES(3,'suffix')")
	suffix := scope.Stage.Rows[ref.Address][1]
	before := *store
	if err := scope.EndPLException(mark, true, errors.New("caught body failure")); err != nil {
		t.Fatal(err)
	}
	if len(scope.Stage.Rows[ref.Address]) != 1 || scope.Stage.Rows[ref.Address][0].Version != prefix.Version || &scope.Stage.Rows[ref.Address][0].Values[0] != &prefix.Values[0] || before.Discarded != nil {
		t.Fatal("protected rollback did not restore immutable real stage prefix")
	}
	freezeRetentionAssert(t, store, suffix)
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, sess, "COMMIT")
	freezeRetentionAssert(t, store, suffix)
	if _, _, ok := store.PhysicalVersion(prefix.Version, -1); !ok {
		t.Fatal("surviving prefix did not commit through ordinary owner")
	}
}

func freezeRetentionPLRollback(t *testing.T) {
	e, sess, ref, store := freezeRetentionFixture(t)
	scope, finish := freezeRetentionScope(t, e, sess)
	if err := scope.Gate(&parser.Do{}); err != nil {
		t.Fatal(err)
	}
	_, run, rollback := transactionUnitRunOnScope(t, scope, "BEGIN ROLLBACK;RETURN 7;END")
	freezeRetentionExecute(t, scope, "INSERT INTO freeze_retention VALUES(2,'PL stage')")
	row := scope.Stage.Rows[ref.Address][0]
	oldRef := scope.TxRef
	if err := run.Host.Transaction(rollback); err != nil {
		t.Fatal(err)
	}
	freezeRetentionAssert(t, store, row)
	if scope.TxRef == oldRef || scope.Owner.State != session.TxImplicit || scope.Stage.Rows != nil || scope.Stage.DiscardedHeaps != nil || row.IndexAdmission == nil {
		t.Fatal("PL rollback failed to dispose stage/rotate owner or mutated capture")
	}
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
	freezeRetentionAssert(t, store, row)
}

func freezeRetentionDeferredFailure(t *testing.T, prepare bool) {
	e, scope, read, relation, procedure := triggerUnitFixtureContext(t, t.Context(), "BEGIN INSERT INTO freeze_terminal_audit VALUES(NEW.id); PERFORM 1/0; RETURN NEW; END")
	enginetest.Run(t, e, "CREATE TABLE freeze_terminal_audit(n integer)")
	audit := enginetest.RoleTableRef(t, e, "freeze_terminal_audit")
	store := e.Storage.Relations[audit.Address]
	before := *store
	relation = triggerUnitInstall(t, e, relation, deferredUnitDefinition(t, read, relation, procedure))
	triggerUnitAdmit(t, scope, relation)
	scope.Owner.State = session.TxActive
	deferredUnitAccept(t, scope, read, relation, 1)
	ref := scope.TxRef
	var err error
	if prepare {
		_, err = scope.Execute(enginetest.Statement(t, "PREPARE TRANSACTION 'freeze_failed_prepare'"), 0, nil)
		err = scope.Finish(err)
	} else {
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		_, err = e.Commit(scope.Op, ref)
	}
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "22012" {
		t.Fatal("terminal body failure was lost", err)
	}
	if store.Discarded == nil || store.Discarded.Next != nil || store.Discarded.Version.Version == 0 || store.Discarded.Version.InsertFullXID == 0 || store.Discarded.Version.Born != 0 || store.Discarded.Version.IndexAdmission != nil || len(store.Versions) != 0 || before.Discarded != nil || scope.Owner.State != session.TxIdle {
		t.Fatal("borrowed terminal scope lost genuine body tuple before disposal")
	}
}

func freezeRetentionRepairOutcome(t *testing.T, commit bool) {
	e, sess, ref, store := freezeRetentionFixture(t)
	enginetest.SessionSQL(t, sess, "BEGIN")
	enginetest.SessionSQL(t, sess, "INSERT INTO freeze_retention VALUES(2,'repair owner')")
	row := sess.Transactions.Writes[ref.Address][0]
	before := *store
	sess.Transactions.Mu.Lock()
	e.Mu.Lock()
	headers, err := storage.RepairHeapHeaders(store.Headers, []storage.RowVersion{row})
	if err == nil {
		store.Headers = headers
		store.Generation++
	}
	e.Mu.Unlock()
	sess.Transactions.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.TransactionEnd(t, sess, sess.Transactions.Ref, commit)
	physical, _, ok := store.PhysicalVersion(row.Version, -1)
	if !ok || physical.Row != row.Row || physical.Version != row.Version || physical.HeapXmin() != 2 || physical.HeapXmax() != 0 || &physical.Values[0] != &row.Values[0] || before.Headers != nil || row.ForcedFrozen || headers.Header(row.Version).RepairStamp == 0 {
		t.Fatal("owner completion clobbered exact persistent repair or old capture")
	}
	observer := enginetest.StartSession(t, e, nil)
	scope, finish := freezeRetentionScope(t, e, observer)
	freezeRetentionExecute(t, scope, "SELECT 1")
	host := core.HostOf(scope).(contrib.Host[core.AuthorizationView])
	owners, err := host.LockHeapStatisticsCohort()
	if err != nil {
		t.Fatal(err)
	}
	facts := host.HeapStatisticsFactsLocked(owners, store, ref.Address, nil, nil)
	host.UnlockHeapStatisticsCohortLocked(owners)
	count := 0
	for _, fact := range facts {
		if fact.Version.Version == row.Version {
			count++
			if fact.Version.Row != row.Row || &fact.Version.Values[0] != &row.Values[0] || !fact.Version.ForcedFrozen {
				t.Fatal("canonical enumeration replaced repaired version/payload")
			}
		}
	}
	if count != 1 {
		t.Fatal("canonical owner/storage cohort duplicated or lost repaired version", count)
	}
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
}

func freezeRetentionReplacement(t *testing.T) {
	e, sess, _, store := freezeRetentionFixture(t)
	enginetest.SessionSQL(t, sess, "BEGIN")
	enginetest.SessionSQL(t, sess, "INSERT INTO freeze_retention VALUES(2,'old universe')")
	enginetest.SessionSQL(t, sess, "DROP TABLE freeze_retention")
	if len(sess.Transactions.Catalog.DiscardedHeaps) == 0 {
		t.Fatal("stale-predecessor witness lacks a genuine deferred batch")
	}
	before := *store
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "CREATE TABLE freeze_retention(n integer,s text)")
	newRef := enginetest.RoleTableRef(t, e, "freeze_retention")
	newStore := e.Storage.Relations[newRef.Address]
	enginetest.TransactionEnd(t, sess, sess.Transactions.Ref, false)
	if newStore.Discarded != nil || len(newStore.Versions) != 0 || store.Discarded != before.Discarded || !storage.SameVersionRoot(store.Versions, before.Versions) {
		t.Fatal("old owner disposal rebound tuple identities into replacement heap")
	}
}

func freezeRetentionMaterialized(t *testing.T, outcome string) {
	e, sess, _, _ := freezeRetentionFixture(t)
	enginetest.Run(t, e, "CREATE MATERIALIZED VIEW freeze_mv AS SELECT n,s FROM freeze_retention")
	ref := enginetest.RoleTableRef(t, e, "freeze_mv")
	original := e.Storage.Relations[ref.Address]
	before := *original
	enginetest.SessionSQL(t, sess, "BEGIN")
	enginetest.SessionSQL(t, sess, "REFRESH MATERIALIZED VIEW freeze_mv")
	firstJournal := sess.Transactions.Catalog
	first := firstJournal.Storage[ref.Address].Replacement
	if first == nil || first == original || len(first.Versions) != 1 || first.Versions[0].Born != 0 || len(sess.Transactions.Writes[ref.Address]) != 0 {
		t.Fatal("first refresh did not reach real private replacement-only boundary")
	}
	created := first.Versions[0]
	firstCapture := *first
	enginetest.SessionSQL(t, sess, "SAVEPOINT first_refresh")
	enginetest.SessionSQL(t, sess, "REFRESH MATERIALIZED VIEW freeze_mv")
	secondJournal := sess.Transactions.Catalog
	second := secondJournal.Storage[ref.Address].Replacement
	if second == nil || second == first || len(second.Versions) != 1 || original.Generation != before.Generation || first.Generation != firstCapture.Generation || len(sess.Transactions.Writes[ref.Address]) != 0 {
		t.Fatal("double refresh changed a fenced predecessor or retained ordinary writes")
	}
	secondCreated := second.Versions[0]
	secondCapture := *second
	e.Mu.Lock()
	ownerXID := e.Visibility.FullXIDLocked(sess.Transactions.Ref)
	frontier := e.Visibility.CommitSeq() + 1
	e.Mu.Unlock()
	if ownerXID == 0 || created.InsertFullXID != ownerXID || secondCreated.InsertFullXID != ownerXID || secondCreated.Version == created.Version || secondCreated.Born != 0 {
		t.Fatal("refresh versions lack distinct identities and actual admitted owner")
	}
	found := false
	for _, batch := range secondJournal.DiscardedHeaps {
		if batch.Address == ref.Address && batch.Storage == first {
			for prior := batch.Versions; prior != nil; prior = prior.Next {
				if prior.Version.Version == created.Version && &prior.Version.Values[0] == &created.Values[0] {
					found = true
				}
			}
		}
	}
	if !found || len(firstJournal.DiscardedHeaps) != 0 || first.Discarded != nil || original.Discarded != nil {
		t.Fatal("retired private refresh tuple was lost, copied, or published before validation")
	}
	if outcome == "savepoint_commit" {
		enginetest.SessionSQL(t, sess, "ROLLBACK TO first_refresh")
		restored := sess.Transactions.Catalog.Storage[ref.Address].Replacement
		if restored != first || restored.Discarded != nil || restored.Generation != firstCapture.Generation || &restored.Versions[0].Values[0] != &created.Values[0] {
			t.Fatal("savepoint aborted or changed still-owned replacement tuple")
		}
		enginetest.SessionSQL(t, sess, "COMMIT")
		if e.Storage.Relations[ref.Address] != first || first.Discarded != nil {
			t.Fatal("restored refresh self-conflicted or duplicated survivor on commit")
		}
	} else if outcome == "commit" {
		enginetest.SessionSQL(t, sess, "COMMIT")
		if e.Storage.Relations[ref.Address] != second || second.Discarded != nil {
			t.Fatal("destructive refresh imported an inaccessible predecessor slot")
		}
	} else {
		enginetest.SessionSQL(t, sess, "ROLLBACK")
		if e.Storage.Relations[ref.Address] != original || original.Discarded != nil || original.Generation != before.Generation {
			t.Fatal("refresh abort exported inaccessible private replacement tuples")
		}
	}
	if outcome == "commit" || outcome == "savepoint_commit" {
		published, want, captured := second, secondCreated, secondCapture
		if outcome == "savepoint_commit" {
			published, want, captured = first, created, firstCapture
		}
		if len(published.Versions) != 1 {
			t.Fatal("refresh publication lost its exact survivor")
		}
		got := published.Versions[0]
		if got.Row != want.Row || got.Version != want.Version || got.Born != frontier || got.InsertFullXID != ownerXID || got.DeleteFullXID != 0 || got.Base != 0 || got.Frozen || got.ImportFrozen || got.ForcedFrozen || got.PrivateHistory != nil || got.IndexAdmission != nil || got.IndexOutputs != nil || &got.Values[0] != &want.Values[0] || storage.SameVersionRoot(published.Versions, captured.Versions) || published.Discarded != nil || published.Retained != nil || published.Ends.Len() != 0 || len(published.RepairedVersions()) != 0 {
			t.Fatal("refresh publication lost exact metadata/payload or imported stale history", got)
		}
	}
	for _, capture := range []struct {
		store storage.Relation
		row   storage.RowVersion
	}{{firstCapture, created}, {secondCapture, secondCreated}} {
		got, want := capture.store.Versions[0], capture.row
		if got.Born != 0 || got.Row != want.Row || got.Version != want.Version || got.InsertFullXID != want.InsertFullXID || got.DeleteFullXID != want.DeleteFullXID || got.Base != want.Base || got.Frozen != want.Frozen || got.ImportFrozen != want.ImportFrozen || got.ForcedFrozen != want.ForcedFrozen || got.PrivateHistory != want.PrivateHistory || got.IndexAdmission != want.IndexAdmission || &got.Values[0] != &want.Values[0] || capture.store.Discarded != nil {
			t.Fatal("refresh completion mutated an immutable private capture", got, want)
		}
	}
	if first.Discarded != nil || firstCapture.Discarded != nil || firstCapture.Versions[0].Born != 0 || firstCapture.Versions[0].Version != created.Version || &firstCapture.Versions[0].Values[0] != &created.Values[0] || before.Discarded != nil || outcome != "savepoint_commit" && !storage.SameVersionRoot(first.Versions, firstCapture.Versions) {
		t.Fatal("refresh disposal mutated immutable old private/published captures")
	}
}

func freezeRetentionDroppedHeap(t *testing.T, commit bool) {
	e, sess, ref, store := freezeRetentionFixture(t)
	enginetest.SessionSQL(t, sess, "BEGIN")
	enginetest.SessionSQL(t, sess, "INSERT INTO freeze_retention VALUES(2,'before drop')")
	row := sess.Transactions.Writes[ref.Address][0]
	before := *store
	enginetest.SessionSQL(t, sess, "DROP TABLE freeze_retention")
	if len(sess.Transactions.Writes[ref.Address]) != 0 || len(sess.Transactions.Catalog.DiscardedHeaps) == 0 || store.Discarded != nil || store.Generation != before.Generation {
		t.Fatal("validated drop lost real private tuple or published retention early")
	}
	enginetest.TransactionEnd(t, sess, sess.Transactions.Ref, commit)
	if commit {
		if e.Storage.Relations[ref.Address] != nil || store.Discarded != nil {
			t.Fatal("committed DROP exported retired heap debris")
		}
	} else {
		freezeRetentionAssert(t, store, row)
		if e.Storage.Relations[ref.Address] != store || before.Discarded != nil {
			t.Fatal("aborted DROP failed to retain tuple in exact restored heap")
		}
	}
}

func freezeRetentionJournalSavepoint(t *testing.T, prepare bool) {
	e, sess, ref, original := freezeRetentionFixture(t)
	enginetest.SessionSQL(t, sess, "BEGIN")
	enginetest.SessionSQL(t, sess, "ALTER TABLE freeze_retention ADD COLUMN extra integer")
	enginetest.SessionSQL(t, sess, "SAVEPOINT journal_parent")
	journal := sess.Transactions.Catalog
	private := journal.Storage[ref.Address].Replacement
	before := *original
	privateBefore := *private
	enginetest.SessionSQL(t, sess, "INSERT INTO freeze_retention VALUES(2,'journal child',3)")
	row := sess.Transactions.Writes[ref.Address][0]
	enginetest.SessionSQL(t, sess, "ROLLBACK TO journal_parent")
	restored := sess.Transactions.Catalog
	if restored == journal || len(restored.DiscardedHeaps) != 1 || len(journal.DiscardedHeaps) != 0 || original.Generation != before.Generation || private.Generation != privateBefore.Generation || private.Discarded != nil {
		t.Fatal("savepoint discard mutated a surviving journal's generation fence or capture")
	}
	batch := restored.DiscardedHeaps[0]
	if batch.Storage != private || batch.Versions == nil || batch.Versions.Version.Version != row.Version || &batch.Versions.Version.Values[0] != &row.Values[0] {
		t.Fatal("deferred child lost exact private heap/tuple/backing")
	}
	if prepare {
		if err := preparedUnitControl(t, e, sess, parser.KindPrepareTransaction, "freeze_journal"); err != nil {
			t.Fatal(err)
		}
		entry := core.EnginePreparedRef(e).Lookup("freeze_journal")
		if entry == nil || entry.Catalog.DiscardedHeaps[0].Versions != batch.Versions || private.Discarded != nil || original.Generation != before.Generation {
			t.Fatal("PREPARE did not transfer immutable deferred batch without abort/publication")
		}
		finisher := enginetest.StartSession(t, e, nil)
		if err := preparedUnitControl(t, e, finisher, parser.KindCommitPrepared, "freeze_journal"); err != nil {
			t.Fatal(err)
		}
	} else {
		enginetest.SessionSQL(t, sess, "COMMIT")
	}
	current := e.Storage.Relations[ref.Address]
	if current != private || original.Discarded != nil || before.Discarded != nil || privateBefore.Discarded != nil {
		t.Fatal("final publication moved tuple debris to the wrong heap or mutated capture")
	}
	freezeRetentionAssert(t, current, row)
}

// Keep the family selector stable while the assertions exercise separate real
// ownership boundaries rather than public SQL result witnesses.
func freezeRetentionComposed(t *testing.T) {
	t.Run("protected_stage_suffix", freezeRetentionProtectedStage)
	t.Run("PL_rollback_stage", freezeRetentionPLRollback)
	t.Run("native_terminal_deferred_failure", func(t *testing.T) { freezeRetentionDeferredFailure(t, false) })
	t.Run("failed_prepare_deferred_stage", func(t *testing.T) { freezeRetentionDeferredFailure(t, true) })
	t.Run("journal_savepoint_commit", func(t *testing.T) { freezeRetentionJournalSavepoint(t, false) })
	t.Run("journal_savepoint_prepare_commit", func(t *testing.T) { freezeRetentionJournalSavepoint(t, true) })
	t.Run("repair_before_commit", func(t *testing.T) { freezeRetentionRepairOutcome(t, true) })
	t.Run("repair_before_abort", func(t *testing.T) { freezeRetentionRepairOutcome(t, false) })
	t.Run("replacement_fences_old_owner", freezeRetentionReplacement)
	for _, outcome := range []string{"commit", "rollback", "savepoint_commit"} {
		t.Run("materialized_double_refresh_"+outcome, func(t *testing.T) { freezeRetentionMaterialized(t, outcome) })
	}
	t.Run("drop_commit_retires_heap", func(t *testing.T) { freezeRetentionDroppedHeap(t, true) })
	t.Run("drop_abort_restores_exact_heap", func(t *testing.T) { freezeRetentionDroppedHeap(t, false) })
}
