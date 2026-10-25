package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func TestTransactionalIndexMaintenanceRootIdentity(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "invalid"}[invalid], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(n INT,s TEXT)", "INSERT INTO t VALUES(1,'a'),(2,'b'),(NULL,NULL)", "CREATE INDEX ix ON t(lower(s) text_pattern_ops,n DESC NULLS LAST) INCLUDE(s) WHERE n>0", "CREATE INDEX sibling ON t(n)"} {
				sessionSQL(t, s, q)
			}
			old, root := btreeRoot(t, e, "ix")
			if invalid {
				base := e.CatalogRead()
				stage := e.BeginCatalogStage(base)
				defer e.DiscardCatalogStage(stage)
				next := *old
				definition := *old.SQLIndex
				definition.Valid = false
				next.SQLIndex = &definition
				next.ObjectRevision++
				if err := stage.Replace(&next); err != nil {
					t.Fatal(err)
				}
				prepared, err := e.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := publishCatalog(e, prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
					t.Fatal(err)
				}
				old, root = btreeRoot(t, e, "ix")
			}
			definition := old.SQLIndex
			parent := e.Catalog.State.Get(definition.Parent)
			store := e.Storage.Relations[definition.Parent]
			sibling, siblingRoot := btreeRoot(t, e, "sibling")
			entries := transactionalIndexSnapshot(root)
			tx := transactionBegin(t, s)
			for _, q := range []string{"ALTER INDEX ix RENAME TO fresh", "ALTER INDEX fresh SET(fillfactor=80.0,deduplicate_items=off)", "ALTER INDEX fresh RESET(fillfactor)", "ALTER INDEX fresh RESET(deduplicate_items)"} {
				previous := e.PrivateCatalogLocked(s.Transactions.Catalog).MustRelation(old.Address())
				previousName := previous.Name()
				previousOptions := slices.Clone(previous.StorageOptions.Base)
				sessionSQL(t, s, q)
				view := e.PrivateCatalogLocked(s.Transactions.Catalog)
				next := view.MustRelation(old.Address())
				if previous.Name() != previousName || !reflect.DeepEqual(previous.StorageOptions.Base, previousOptions) {
					t.Fatal("metadata operation mutated retained predecessor descriptor/option slice")
				}
				if next.Address() != old.Address() || next.SQLIndex != definition || view.Get(definition.Parent) != parent || e.Storage.Relations[definition.Parent] != store || e.Storage.IndexRoots[old.Address()] != root || e.Storage.IndexRoots[sibling.Address()] != siblingRoot {
					t.Fatal("metadata-only operation changed identity/providers/root/store")
				}
				j := index.JournalIndexesOrZero(s.Transactions.Catalog)
				if len(j.Candidates) != 0 || len(j.Rebuilds) != 0 {
					t.Fatal("metadata operation requested a key build", j)
				}
				journal := s.Transactions.Catalog
				if journal.RolesBase != nil || journal.Roles != nil || len(journal.Storage) != 0 {
					t.Fatal("metadata operation retained role-root or heap replacement authority")
				}
				for _, fence := range journal.Fences {
					if fence.Kind() == session.FenceStorageRead {
						t.Fatal("metadata operation retained a heap read barrier")
					}
					if fence.Kind() == FenceCatalogRoleRead {
						t.Fatal("metadata operation retained pending producer role authority")
					}
				}
				if !reflect.DeepEqual(root.Entries, entries) || len(old.StorageOptions.Base) != 0 || e.Catalog.State.Get(old.Address()) != old {
					t.Fatal("metadata operation mutated committed capture")
				}
				if q == "ALTER INDEX fresh SET(fillfactor=80.0,deduplicate_items=off)" {
					want := []catalog.RelationOption{{Name: "fillfactor", Text: "80.0", Kind: catalog.RelationOptionInteger, Integer: 80}, {Name: "deduplicate_items", Text: "off", Kind: catalog.RelationOptionBoolean, Boolean: false}}
					if !reflect.DeepEqual(next.StorageOptions.Base, want) {
						t.Fatal("not real typed lexical descriptor options", next.StorageOptions.Base)
					}
				}
			}
			transactionEnd(t, s, tx, true)
			next, same := btreeRoot(t, e, "fresh")
			if same != root || next.Address() != old.Address() || next.SQLIndex != definition || next.SQLIndex.Valid == invalid || e.Storage.Relations[definition.Parent] != store {
				t.Fatal("commit rebuilt or normalized index")
			}
			if invalid {
				sessionSQL(t, s, "BEGIN")
				sessionSQL(t, s, "DROP INDEX fresh")
				sessionSQL(t, s, "ROLLBACK")
				if e.Catalog.State.Get(old.Address()) != next || e.Storage.IndexRoots[old.Address()] != root {
					t.Fatal("invalid drop rollback lost exact index")
				}
			}
			lockEmpty(t, e)
		})
	}
}

func TestTransactionalIndexMaintenanceJournalSavepoints(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(n INT,s TEXT)", "INSERT INTO t VALUES(1,'a'),(2,'b')", "CREATE UNIQUE INDEX ix ON t(n)", "CREATE INDEX expression ON t(lower(s)) WHERE n>0", "CREATE TABLE unrelated(n INT)", "CREATE INDEX unrelated_idx ON unrelated(n)"} {
		sessionSQL(t, s, q)
	}
	old, root := btreeRoot(t, e, "ix")
	derived, derivedRoot := btreeRoot(t, e, "expression")
	stored := e.Storage.Relations[old.SQLIndex.Parent]
	committed := indexMaintenanceCapture(e)
	retained := transactionalIndexSnapshot(root)
	tx := transactionBegin(t, s)
	for _, q := range []string{"ALTER INDEX ix RENAME TO fresh", "ALTER INDEX fresh SET(fillfactor=80.0,deduplicate_items=off)", "INSERT INTO t VALUES(3,'before')", "SAVEPOINT keep"} {
		sessionSQL(t, s, q)
	}
	prefix := s.Transactions.Catalog
	copy := prefix.Clone()
	writes := s.Transactions.Writes[old.SQLIndex.Parent]
	deletes := s.Transactions.Deletes[old.SQLIndex.Parent]
	view := e.PrivateCatalogLocked(prefix)
	prefixDescriptor := view.Get(old.Address())
	prefixDefinition := view.MustRelation(old.Address()).SQLIndex
	prefixOptions := slices.Clone(prefixDescriptor.(*catalog.Relation).StorageOptions.Base)
	for range 2 {
		for _, q := range []string{"ALTER INDEX fresh RESET(fillfactor)", "ALTER INDEX fresh RENAME TO later", "DROP INDEX later,expression", "INSERT INTO t VALUES(1,'after')"} {
			sessionSQL(t, s, q)
		}
		removed := e.PrivateCatalogLocked(s.Transactions.Catalog)
		if removed.Get(old.Address()) != nil || removed.Get(derived.Address()) != nil {
			t.Fatal("drop did not remove exact private identities")
		}
		if !reflect.DeepEqual(prefix.Clone(), copy) || !reflect.DeepEqual(root.Entries, retained) {
			t.Fatal("postmark work mutated retained immutable journal/root")
		}
		if prefixDescriptor.Name() != "fresh" || !reflect.DeepEqual(prefixDescriptor.(*catalog.Relation).StorageOptions.Base, prefixOptions) {
			t.Fatal("postmark operation mutated retained prefix options/name")
		}
		sessionSQL(t, s, "ROLLBACK TO keep")
		restored := e.PrivateCatalogLocked(s.Transactions.Catalog)
		if s.Transactions.Catalog != prefix || restored.Get(old.Address()) != prefixDescriptor || restored.MustRelation(old.Address()).SQLIndex != prefixDefinition || restored.Get(derived.Address()) != derived || !storage.SameVersionRoot(s.Transactions.Writes[old.SQLIndex.Parent], writes) || s.Transactions.Deletes[old.SQLIndex.Parent] != deletes {
			t.Fatal("savepoint did not restore exact descriptor/provider/write/deletion authority")
		}
		if !reflect.DeepEqual(prefix.Clone(), copy) || e.Storage.IndexRoots[old.Address()] != root || e.Storage.IndexRoots[derived.Address()] != derivedRoot || e.Storage.Relations[old.SQLIndex.Parent] != stored {
			t.Fatal("savepoint lost dependency observations/root/storage")
		}
	}
	sessionSQL(t, s, "RELEASE keep")
	transactionEnd(t, s, tx, false)
	indexMaintenanceUnchanged(t, e, committed)
	lockEmpty(t, e)
}

func TestTransactionalIndexMaintenanceAtomicPublication(t *testing.T) {
	for _, veto := range []string{"none", "cancel", "epoch", "caller"} {
		t.Run(veto, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX ix ON t(n)", "CREATE INDEX sibling ON t(n DESC)"} {
				sessionSQL(t, s, q)
			}
			old, root := btreeRoot(t, e, "ix")
			sibling, oldSibling := btreeRoot(t, e, "sibling")
			tx := transactionBegin(t, s)
			sessionSQL(t, s, "SAVEPOINT recover")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				op.Done()
				t.Fatal(err)
			}
			defer func() { scope.Finish(errors.New("unit cleanup")); op.Done() }()
			_, err = scope.Execute(statement(t, "ALTER INDEX ix SET(fillfactor=80)"), 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if scope.Catalog == nil || scope.Catalog.Descriptors[old.Address()] == nil || s.Transactions.Catalog != nil || e.Catalog.State.Get(old.Address()) != old || e.Storage.IndexRoots[old.Address()] != root {
				t.Fatal("Execute did not retain private, unmerged complete candidate")
			}
			var primary error
			switch veto {
			case "cancel":
				cancel()
			case "epoch":
				if err := Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			case "caller":
				primary = errors.New("consumer rejected utility")
			}
			before := indexMaintenanceCapture(e)
			err = scope.Finish(primary)
			op.Done()
			if veto != "none" {
				if err == nil {
					t.Fatal("Finish accepted veto")
				}
				if veto == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if veto == "epoch" && !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
				if veto == "caller" && !errors.Is(err, primary) {
					t.Fatal(err)
				}
				indexMaintenanceUnchanged(t, e, before)
				transactionEnd(t, s, tx, false)
				lockEmpty(t, e)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			journal := s.Transactions.Catalog
			if journal == nil {
				t.Fatal("Finish failed to merge successful completed utility")
			}
			if journal.RolesBase != nil || journal.Roles != nil {
				t.Fatal("metadata-only utility retained statement role authority")
			}
			if len(journal.Storage) != 0 {
				t.Fatal("metadata-only utility retained a heap replacement")
			}
			for _, fence := range journal.Fences {
				if fence.Kind() == session.FenceStorageRead {
					t.Fatal("metadata-only utility retained a heap read barrier")
				}
				if fence.Kind() == FenceCatalogRoleRead {
					t.Fatal("metadata-only utility retained pending producer role authority")
				}
			}
			if err := scope.Finish(nil); err != nil || s.Transactions.Catalog != journal {
				t.Fatal("Finish merged twice", err)
			}
			// New unrelated/auth frontiers and parent DML are legitimate, not stale
			// statement-auth or table-root commit conflicts for metadata-only edits.
			sessionSQL(t, peer, "INSERT INTO t VALUES(2)")
			sessionSQL(t, peer, "CREATE ROLE unrelated")
			newSibling := e.Storage.IndexRoots[sibling.Address()]
			if newSibling == oldSibling {
				t.Fatal("peer did not install real newest sibling")
			}
			sessionSQL(t, s, "INSERT INTO t VALUES(3)")
			transactionEnd(t, s, tx, true)
			final, finalRoot := btreeRoot(t, e, "ix")
			if final.Address() != old.Address() || len(final.StorageOptions.Base) != 1 || old.StorageOptions.Base != nil || finalRoot == root {
				t.Fatal("combined publication lost metadata/own DML or mutated old descriptor")
			}
			btreeWantKeys(t, e, "ix", "1", "2", "3")
			btreeWantKeys(t, e, "sibling", "3", "2", "1")
			if !reflect.DeepEqual(btreeKeys(t, newSibling), []string{"2", "1"}) {
				t.Fatal("combined publication mutated newest retained peer root")
			}
			lockEmpty(t, e)
		})
	}
	t.Run("newest-untouched-sibling", func(t *testing.T) {
		e := New()
		owner, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
		for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX ix ON t(n)", "CREATE INDEX sibling ON t(n DESC)"} {
			sessionSQL(t, owner, q)
		}
		selected, old := btreeRoot(t, e, "ix")
		sibling, _ := btreeRoot(t, e, "sibling")
		retained := transactionalIndexSnapshot(old)
		tx := transactionBegin(t, owner)
		sessionSQL(t, owner, "ALTER INDEX ix SET(fillfactor=80)")
		sessionSQL(t, peer, "INSERT INTO t VALUES(2)")
		newSelected, newSibling := e.Storage.IndexRoots[selected.Address()], e.Storage.IndexRoots[sibling.Address()]
		transactionEnd(t, owner, tx, true)
		if e.Storage.IndexRoots[selected.Address()] != newSelected || e.Storage.IndexRoots[sibling.Address()] != newSibling || !reflect.DeepEqual(old.Entries, retained) {
			t.Fatal("metadata commit overwrote newest selected/sibling root or mutated retained capture")
		}
		btreeWantKeys(t, e, "ix", "1", "2")
		btreeWantKeys(t, e, "sibling", "2", "1")
		lockEmpty(t, e)
	})
	t.Run("commit-context-veto", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE UNIQUE INDEX ix ON t(n)", "CREATE INDEX sibling ON t(n)"} {
			sessionSQL(t, s, q)
		}
		before := indexMaintenanceCapture(e)
		tx := transactionBegin(t, s)
		for _, q := range []string{"ALTER INDEX ix RENAME TO fresh", "ALTER INDEX fresh SET(fillfactor=80)", "DROP INDEX fresh", "INSERT INTO t VALUES(1)"} {
			sessionSQL(t, s, q)
		}
		if e.PrivateCatalogLocked(s.Transactions.Catalog).Get(roleTableRef(t, e, "ix").Address) != nil {
			t.Fatal("fixture did not assemble private removal")
		}
		ctx, cancel := context.WithCancel(context.Background())
		op, err := s.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		_, err = e.Commit(op, tx)
		op.Done()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("commit context veto ignored", err)
		}
		indexMaintenanceUnchanged(t, e, before)
		// An admitted native Commit consumes its handle even on a context veto.
		// Recover through the current SQL owner, never reuse the retired TxRef.
		if s.TransactionState() != session.TxIdle || s.Transactions.Catalog != nil {
			t.Fatal("canceled commit retained private transaction state")
		}
		sessionSQL(t, s, "ROLLBACK")
		lockEmpty(t, e)
	})
}

func TestTransactionalIndexMaintenanceGrantFences(t *testing.T) {
	for _, action := range []string{"rename", "set", "reset", "drop"} {
		for _, veto := range []string{"replace", "epoch", "cancel", "terminal"} {
			t.Run(action+"/"+veto, func(t *testing.T) {
				e := New()
				holder, owner := sessionStart(t, e, nil), sessionStart(t, e, nil)
				for _, q := range []string{"CREATE TABLE t(n INT)", "INSERT INTO t VALUES(1)", "CREATE INDEX ix ON t(n)"} {
					sessionSQL(t, holder, q)
				}
				if action == "reset" {
					sessionSQL(t, holder, "ALTER INDEX ix SET(fillfactor=80)")
				}
				old, _ := btreeRoot(t, e, "ix")
				ht := transactionBegin(t, holder)
				ot := transactionBegin(t, owner)
				resource := roleTableRef(t, e, "ix")
				held := lock.Share
				q := "ALTER INDEX ix RENAME TO fresh"
				switch action {
				case "set":
					q = "ALTER INDEX ix SET(fillfactor=80)"
				case "reset":
					q = "ALTER INDEX ix RESET(fillfactor)"
				case "drop":
					q = "DROP INDEX ix"
					resource = roleTableRef(t, e, "t")
					held = lock.AccessShare
				}
				if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: resource}, held); err != nil {
					t.Fatal(err)
				}
				cancel, await := indexMaintenanceAsync(t, e, owner, q)
				lockPending(t, e, 1)
				switch veto {
				case "replace":
					indexMaintenancePublishIndexClone(t, e, "ix", "ix", true)
				case "epoch":
					if err := Reset(context.Background(), e); err != nil {
						t.Fatal(err)
					}
				case "cancel":
					cancel()
				case "terminal":
					owner.RequestEnd(session.Terminated)
				}
				before := indexMaintenanceCapture(e)
				transactionEnd(t, holder, ht, false)
				err := await()
				if err == nil {
					t.Fatal("stale/terminal/canceled admission succeeded")
				}
				if veto == "replace" || veto == "epoch" {
					if !errors.Is(err, ids.ErrCatalogReplaced) {
						t.Fatal("grant retargeted captured identity", err)
					}
				}
				if veto == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				indexMaintenanceUnchanged(t, e, before)
				if veto == "replace" {
					current, _ := btreeRoot(t, e, "ix")
					if current.Address() == old.Address() {
						t.Fatal("fixture failed to replace identity")
					}
				}
				if veto != "terminal" {
					transactionEnd(t, owner, ot, false)
				} else {
					if err := owner.WaitEnd(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				lockEmpty(t, e)
			})
		}
	}
	for _, action := range []string{"rename", "set", "reset", "drop"} {
		t.Run(action+"/grant-owner-revoked", func(t *testing.T) {
			e := New()
			admin, actor := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE USER actor", "CREATE TABLE t(n INT)", "ALTER TABLE t OWNER TO actor", "CREATE INDEX ix ON t(n)", "ALTER INDEX ix SET(fillfactor=80)"} {
				sessionSQL(t, admin, q)
			}
			sessionSQL(t, actor, "SET ROLE actor")
			// ALTER RENAME needs schema CREATE in addition to exact index owner.
			ns := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
			user, _ := e.Roles().Lookup("actor")
			root, _ := e.Roles().Lookup("postgres")
			if err := setObjectACL(e, context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: root.Ref, Grantee: authorization.ACLGrantee{Role: user.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}); err != nil {
				t.Fatal(err)
			}
			at := transactionBegin(t, admin)
			ot := transactionBegin(t, actor)
			ref := roleTableRef(t, e, "ix")
			resource := ref
			held := lock.Share
			q := "ALTER INDEX ix RENAME TO fresh"
			switch action {
			case "set":
				q = "ALTER INDEX ix SET(fillfactor=70)"
			case "reset":
				q = "ALTER INDEX ix RESET(fillfactor)"
			case "drop":
				q = "DROP INDEX ix"
				resource = roleTableRef(t, e, "t")
				held = lock.AccessShare
			}
			if err := e.Locks.Acquire(context.Background(), admin.Transactions.Ticket, lock.Resource{ObjectRef: resource}, held); err != nil {
				t.Fatal(err)
			}
			_, await := indexMaintenanceAsync(t, e, actor, q)
			lockPending(t, e, 1)
			base := e.CatalogRead()
			next := *base.MustRelation(ref.Address)
			next.Owner = root.Ref
			next.ObjectRevision++
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			if err := stage.Replace(&next); err != nil {
				t.Fatal(err)
			}
			parent := *base.MustRelation(next.SQLIndex.Parent)
			parent.Owner = root.Ref
			parent.ObjectRevision++
			if err := stage.Replace(&parent); err != nil {
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := publishCatalog(e, prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
				t.Fatal(err)
			}
			before := indexMaintenanceCapture(e)
			transactionEnd(t, admin, at, false)
			state(t, await(), "42501")
			indexMaintenanceUnchanged(t, e, before)
			transactionEnd(t, actor, ot, false)
			lockEmpty(t, e)
		})
	}
}
