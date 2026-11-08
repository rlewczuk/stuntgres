package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func TestEnumIndexLayoutGenerationPrivateRoots(t *testing.T) {
	for _, private := range []bool{false, true} {
		for _, populated := range []bool{false, true} {
			name := map[bool]string{false: "committed-parent", true: "private-parent"}[private] + "/" + map[bool]string{false: "empty", true: "populated"}[populated]
			t.Run(name, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				for _, q := range []string{"CREATE TABLE enum_generation_unrelated(n INT)", "INSERT INTO enum_generation_unrelated VALUES(90)", "CREATE INDEX enum_generation_unrelated_idx ON enum_generation_unrelated(n)"} {
					enginetest.SessionSQL(t, s, q)
				}
				unrelated, unrelatedRoot := btreeRoot(t, e, "enum_generation_unrelated_idx")
				unrelatedEntries := transactionalIndexSnapshot(unrelatedRoot)
				var tx = enginetest.TransactionBegin(t, s)
				if !private {
					enginetest.TransactionEnd(t, s, tx, false)
				}
				for _, q := range []string{"CREATE TYPE enum_generation_mood AS ENUM('pending')", "CREATE TABLE enum_generation_heap(status enum_generation_mood,id INT)"} {
					enginetest.SessionSQL(t, s, q)
				}
				if populated {
					enginetest.SessionSQL(t, s, "INSERT INTO enum_generation_heap VALUES('pending',1),('pending',2)")
				}
				if !private {
					tx = enginetest.TransactionBegin(t, s)
				}
				enginetest.SessionSQL(t, s, "CREATE TABLE enum_generation_private_sibling(n INT)")
				enginetest.SessionSQL(t, s, "INSERT INTO enum_generation_private_sibling VALUES(70)")
				enginetest.SessionSQL(t, s, "CREATE INDEX enum_generation_private_sibling_idx ON enum_generation_private_sibling(n)")
				privateView := e.PrivateCatalogLocked(s.Transactions.Catalog)
				sibling := enginetest.CatalogRef(t, e, privateView, "enum_generation_private_sibling_idx").Address
				siblingJournal := index.JournalIndexesOrZero(s.Transactions.Catalog)
				siblingRoot, siblingCandidate := siblingJournal.UniqueRoots[sibling], siblingJournal.Candidates[sibling]
				siblingEntries := transactionalIndexSnapshot(siblingRoot)
				if populated {
					enginetest.SessionSQL(t, s, "INSERT INTO enum_generation_heap VALUES('pending',3)")
				}
				enginetest.SessionSQL(t, s, "CREATE INDEX enum_generation_idx ON enum_generation_heap(status)")
				built := enumIndexGenerationCapture(t, e, s)
				wantBaseline, wantWrites, wantHistory := 0, 0, 0
				if populated {
					// Only heads present at CREATE INDEX receive build outputs;
					// subsequent plain B-tree DML does not produce new ones.
					wantHistory = 1
					if private {
						wantWrites = 2
					} else {
						wantBaseline = 2
					}
				}
				if len(built.root.Entries) != wantBaseline {
					t.Fatal("private root does not represent the actual committed baseline", len(built.root.Entries), wantBaseline)
				}
				if populated {
					for _, q := range []string{"INSERT INTO enum_generation_heap VALUES('pending',4)", "UPDATE enum_generation_heap SET id=30 WHERE id=3", "UPDATE enum_generation_heap SET id=31 WHERE id=30"} {
						enginetest.SessionSQL(t, s, q)
					}
				}
				before := enumIndexGenerationCapture(t, e, s)
				enumIndexGenerationOutputs(t, before, wantWrites, wantHistory)
				enumIndexGenerationPhysical(t, before.descriptor, before.parent, before.root, before.store)
				enginetest.SessionSQL(t, s, "ALTER TYPE enum_generation_mood ADD VALUE 'inhibited'")
				generated := enumIndexGenerationCapture(t, e, s)
				if generated.codec == before.codec || generated.codec.ID() != before.codec.ID() || len(generated.members) != len(before.members)+1 || generated.members[0] != before.members[0] || index.SameIndexKeySemantics(before.descriptor, generated.descriptor) {
					t.Fatal("enum transport lost certified same-identity generation or weakened strict Usage equality")
				}
				enumIndexGenerationSamePhysical(t, before.versions, generated.versions)
				enumIndexGenerationSamePhysical(t, before.writes, generated.writes)
				enumIndexGenerationOutputs(t, generated, wantWrites, wantHistory)
				enumIndexGenerationOutputTransport(t, before, generated)
				enumIndexGenerationPhysical(t, generated.descriptor, generated.parent, generated.root, generated.store)
				if wantBaseline != 0 && (generated.root == before.root || generated.root.Construction == before.root.Construction) {
					t.Fatal("typed baseline retained old root/construction instead of transporting it")
				}
				enginetest.SessionSQL(t, s, "ALTER TABLE enum_generation_heap ADD COLUMN extra TEXT")
				projected := enumIndexGenerationCapture(t, e, s)
				if projected.codec != generated.codec || len(projected.parent.Columns) != 3 {
					t.Fatal("column projection lost current typed generation")
				}
				enumIndexGenerationSamePhysical(t, generated.versions, projected.versions)
				enumIndexGenerationSamePhysical(t, generated.writes, projected.writes)
				enumIndexGenerationOutputs(t, projected, wantWrites, wantHistory)
				enumIndexGenerationOutputTransport(t, generated, projected)
				enumIndexGenerationPhysical(t, projected.descriptor, projected.parent, projected.root, projected.store)
				for _, retained := range []enumIndexGenerationReceipt{built, before, generated} {
					retained.immutable(t)
				}
				current := index.JournalIndexesOrZero(s.Transactions.Catalog)
				if current.UniqueRoots[sibling] != siblingRoot || current.Candidates[sibling] != siblingCandidate || !reflect.DeepEqual(siblingRoot.Entries, siblingEntries) || e.Storage.IndexRoots[unrelated.Address()] != unrelatedRoot || !reflect.DeepEqual(unrelatedRoot.Entries, unrelatedEntries) || e.Catalog.State.Get(before.descriptor.Address()) != nil || e.Storage.IndexRoots[before.descriptor.Address()] != nil {
					t.Fatal("generation/column DDL changed sibling authority or published the private index early")
				}
				enginetest.TransactionEnd(t, s, tx, true)
				published, finalRoot := btreeRoot(t, e, "enum_generation_idx")
				parent := e.Catalog.State.MustRelation(published.SQLIndex.Parent)
				store := e.Storage.Relations[parent.Address()]
				if published.Address() != before.descriptor.Address() || parent.Address() != before.parent.Address() || parent.Columns[0].Usage.Type != projected.codec || len(finalRoot.Entries) != map[bool]int{false: 0, true: 4}[populated] {
					t.Fatal("publication lost exact index/parent/type identity or physical membership")
				}
				enumIndexGenerationPhysical(t, published, parent, finalRoot, store)
				for _, retained := range []enumIndexGenerationReceipt{built, before, generated, projected} {
					retained.immutable(t)
				}
				if e.Storage.IndexRoots[unrelated.Address()] != unrelatedRoot || !reflect.DeepEqual(unrelatedRoot.Entries, unrelatedEntries) || !reflect.DeepEqual(siblingRoot.Entries, siblingEntries) {
					t.Fatal("commit mutated unrelated or retained private sibling roots")
				}
				if s.Transactions.Catalog != nil || s.Transactions.Writes != nil || s.Transactions.Deletes != nil {
					t.Fatal("commit retained terminal private authority")
				}
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func TestEnumIndexLayoutGenerationSavepointRoots(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE enum_generation_unrelated(n INT)")
	enginetest.SessionSQL(t, s, "CREATE INDEX enum_generation_unrelated_idx ON enum_generation_unrelated(n)")
	committed := indexMaintenanceCapture(e)
	frontier, rowFrontier, versionFrontier := e.Visibility.CommitSeq(), e.Storage.RowFrontier(), e.Storage.VersionFrontier()
	tx := enginetest.TransactionBegin(t, s)
	for _, q := range []string{"CREATE TYPE enum_generation_mood AS ENUM('pending')", "CREATE TABLE enum_generation_heap(status enum_generation_mood,id INT)", "INSERT INTO enum_generation_heap VALUES('pending',1),('pending',9)", "CREATE INDEX enum_generation_idx ON enum_generation_heap(status)", "UPDATE enum_generation_heap SET id=2 WHERE id=1", "DELETE FROM enum_generation_heap WHERE id=9", "SAVEPOINT generation"} {
		enginetest.SessionSQL(t, s, q)
	}
	prefix := enumIndexGenerationCapture(t, e, s)
	prefixDeletes := s.Transactions.Deletes[prefix.parent.Address()]
	deleteCopy := prefixDeletes.Clone()
	if prefixDeletes == nil {
		t.Fatal("savepoint fixture did not retain an actual private deletion")
	}
	var retired []storage.RowVersion
	var retained []enumIndexGenerationReceipt
	heaps := []struct {
		store  *storage.Relation
		before enginetest.HeapAbortSnapshot
	}{{prefix.store, enginetest.CaptureHeapAbort(prefix.store)}}
	for range 2 {
		enginetest.SessionSQL(t, s, "ALTER TYPE enum_generation_mood ADD VALUE 'inhibited'")
		generated := enumIndexGenerationCapture(t, e, s)
		enumIndexGenerationOutputs(t, generated, 1, 1)
		enumIndexGenerationOutputTransport(t, prefix, generated)
		retained = append(retained, generated)
		enginetest.SessionSQL(t, s, "SAVEPOINT projection")
		for range 2 {
			enginetest.SessionSQL(t, s, "ALTER TABLE enum_generation_heap ADD COLUMN extra TEXT")
			projected := enumIndexGenerationCapture(t, e, s)
			if projected.store == generated.store {
				t.Fatal("column projection did not create a distinct heap incarnation")
			}
			enumIndexGenerationOutputTransport(t, generated, projected)
			var heapBefore enginetest.HeapAbortSnapshot
			retained = append(retained, projected)
			enginetest.SessionSQL(t, s, "SAVEPOINT writes")
			var discarded []storage.RowVersion
			for range 2 {
				for _, q := range []string{"UPDATE enum_generation_heap SET id=3", "INSERT INTO enum_generation_heap VALUES('pending',4,NULL)", "DELETE FROM enum_generation_heap WHERE id=3"} {
					enginetest.SessionSQL(t, s, q)
				}
				suffix := enumIndexGenerationCapture(t, e, s)
				enumIndexGenerationOutputTransport(t, projected, suffix)
				created := enumIndexGenerationCreatedAfter(projected.writes, suffix.writes)
				if len(created) != 2 {
					t.Fatal("DML suffix did not retain its genuine update and insertion versions", len(created))
				}
				discarded = append(discarded, created...)
				// Disposal preserves the physical header events admitted by
				// DML; capture them before crossing the rollback boundary.
				heapBefore = enginetest.CaptureHeapAbort(projected.store)
				enginetest.SessionSQL(t, s, "ROLLBACK TO writes")
				restored := enumIndexGenerationCapture(t, e, s)
				if restored.store != projected.store || restored.codec != projected.codec || restored.parent != projected.parent || restored.descriptor != projected.descriptor || restored.root != projected.root || restored.candidate != projected.candidate || !storage.SameVersionRoot(restored.writes, projected.writes) || !reflect.DeepEqual(restored.deletes, projected.deleteCopy) {
					t.Fatal("DML rollback failed to restore exact projected heap/output/deletion authority")
				}
				// The heap survives this boundary: every created head and
				// predecessor must be deferred with its exact payload backing.
				enginetest.AssertJournalDiscards(t, restored.journal, projected.journal, projected.parent.Address(), projected.store, discarded...)
				enginetest.AssertHeapAbort(t, projected.store, heapBefore, 0)
				enumIndexGenerationOutputs(t, restored, 1, 1)
				enumIndexGenerationOutputTransport(t, projected, restored)
				projected.immutable(t)
				suffix.immutable(t)
				retained = append(retained, suffix, restored)
			}
			heaps = append(heaps, struct {
				store  *storage.Relation
				before enginetest.HeapAbortSnapshot
			}{projected.store, heapBefore})
			retired = append(retired, discarded...)
			enginetest.SessionSQL(t, s, "RELEASE writes")
			enginetest.SessionSQL(t, s, "ROLLBACK TO projection")
			restored := enumIndexGenerationCapture(t, e, s)
			if restored.store != generated.store || restored.codec != generated.codec || restored.parent != generated.parent || restored.descriptor != generated.descriptor || restored.root != generated.root || restored.candidate != generated.candidate || !storage.SameVersionRoot(restored.writes, generated.writes) || !reflect.DeepEqual(restored.deletes, generated.deleteCopy) {
				t.Fatal("column rollback rebuilt or retained suffix authority")
			}
			// Rewinding the replacement retires its slots, not the
			// same-address predecessor heap restored by this mark.
			enginetest.AssertJournalDiscards(t, restored.journal, generated.journal, generated.parent.Address(), generated.store)
			enginetest.AssertHeapAbort(t, projected.store, heapBefore, 0)
			enumIndexGenerationRetiredAbsent(t, e, s, prefix, retired)
			enumIndexGenerationOutputs(t, restored, 1, 1)
			enumIndexGenerationOutputTransport(t, generated, restored)
			generated.immutable(t)
			if e.PrivateCatalogLocked(s.Transactions.Catalog).MustRelation(prefix.parent.Address()).Columns[0].Usage.Type != generated.codec || len(restored.parent.Columns) != 2 {
				t.Fatal("column rollback lost correct generation/shape")
			}
			retained = append(retained, restored)
		}
		enginetest.SessionSQL(t, s, "RELEASE projection")
		enginetest.SessionSQL(t, s, "ROLLBACK TO generation")
		restored := enumIndexGenerationCapture(t, e, s)
		if restored.store != prefix.store || restored.codec != prefix.codec || restored.parent != prefix.parent || restored.descriptor != prefix.descriptor || restored.root != prefix.root || restored.candidate != prefix.candidate || !storage.SameVersionRoot(restored.writes, prefix.writes) || !reflect.DeepEqual(s.Transactions.Deletes[prefix.parent.Address()], deleteCopy) || !reflect.DeepEqual(prefixDeletes, deleteCopy) {
			t.Fatal("generation rollback failed to restore exact prefix storage/output/deletion authority")
		}
		prefix.immutable(t)
		enginetest.AssertJournalDiscards(t, restored.journal, prefix.journal, prefix.parent.Address(), prefix.store)
		enumIndexGenerationRetiredAbsent(t, e, s, prefix, retired)
		for _, receipt := range retained {
			receipt.immutable(t)
		}
	}
	for _, q := range []string{"ALTER TYPE enum_generation_mood ADD VALUE 'inhibited'", "ALTER TABLE enum_generation_heap ADD COLUMN extra TEXT", "UPDATE enum_generation_heap SET id=5", "RELEASE generation"} {
		enginetest.SessionSQL(t, s, q)
	}
	redo := enumIndexGenerationCapture(t, e, s)
	enumIndexGenerationOutputs(t, redo, 1, 1)
	enumIndexGenerationOutputTransport(t, prefix, redo)
	prefix.immutable(t)
	retired = append(retired, enumIndexGenerationCreatedAfter(nil, redo.writes)...)
	redoHeap := enginetest.CaptureHeapAbort(redo.store)
	enginetest.TransactionEnd(t, s, tx, false)
	indexMaintenanceUnchanged(t, e, committed)
	if e.Visibility.CommitSeq() != frontier || s.Transactions.Catalog != nil || s.Transactions.Writes != nil || s.Transactions.Deletes != nil || s.TransactionState() != session.TxIdle || e.Catalog.State.Get(prefix.parent.Address()) != nil || e.Storage.IndexRoots[prefix.descriptor.Address()] != nil {
		t.Fatal("outer rollback published or retained private generation/root/rows")
	}
	// Physical tuple allocators are monotonic even for discarded private work;
	// rollback must not fake a successful cleanup by rewinding those identities.
	if e.Storage.RowFrontier() <= rowFrontier || e.Storage.VersionFrontier() <= versionFrontier {
		t.Fatal("rollback rewound actual physical allocation history")
	}
	if uint64(len(retired)) != e.Storage.VersionFrontier()-versionFrontier {
		t.Fatal("rollback witness omitted a genuinely allocated version")
	}
	enumIndexGenerationRetiredAbsent(t, e, s, prefix, retired)
	enginetest.AssertHeapAbort(t, redo.store, redoHeap, 0)
	for _, heap := range heaps {
		enginetest.AssertHeapAbort(t, heap.store, heap.before, 0)
	}
	redo.immutable(t)
	for _, receipt := range retained {
		receipt.immutable(t)
	}
	prefix.immutable(t)
	enginetest.LockEmpty(t, e)
}
