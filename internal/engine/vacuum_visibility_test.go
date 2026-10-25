package engine

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"testing"
)

func vacuumStore(t *testing.T, e *Engine, name string) (ids.ObjectRef, *storage.Relation) {
	t.Helper()
	ref := catalogRef(t, e, e.CatalogRead(), name)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	store := e.Storage.Relations[ref.Address]
	if store == nil {
		t.Fatal("missing store", name)
	}
	return ref, store
}
func TestVacuumSnapshotSafety(t *testing.T) {
	e := New()
	reader, writer := sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE anchor(id INT)", "CREATE TABLE subject(id INT)", "INSERT INTO anchor VALUES(9)", "INSERT INTO subject VALUES(1),(2),(3)", "CREATE INDEX subject_ix ON subject(id)"} {
		sessionSQL(t, writer, q)
	}
	ref, store := vacuumStore(t, e, "subject")
	old := store.Versions
	headers := slices.Clone(old)
	payloads := slices.Clone(old)
	for i := range headers {
		headers[i].Values = slices.Clone(headers[i].Values)
		if headers[i].Frozen || headers[i].ImportFrozen || headers[i].InsertFullXID < 3 || headers[i].Born == 0 {
			t.Fatalf("initial ordinary publisher provenance row%d: %#v", i, headers[i])
		}
	}
	indexRef := catalogRef(t, e, e.CatalogRead(), "subject_ix")
	oldIndex := e.Storage.IndexRoots[indexRef.Address]
	if len(oldIndex.Entries) != 3 {
		t.Fatal("pre-delete index must own all three entries", oldIndex.Entries)
	}
	copyEntries := func(entries []storage.IndexEntry) []storage.IndexEntry {
		copy := slices.Clone(entries)
		for i := range copy {
			copy[i].Keys = slices.Clone(copy[i].Keys)
		}
		return copy
	}
	oldEntries := copyEntries(oldIndex.Entries)
	oldMembership, oldRowEntries := maps.Clone(oldIndex.Membership), maps.Clone(oldIndex.RowEntries)
	sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	retained := transactionRows(t, reader, "SELECT id FROM anchor", 9)
	defer retained.Close()
	snapshot := reader.Transactions.Roots[ref.Address]
	snapshotEnds := snapshot.Ends
	var snapshotEndEntries map[ids.RowID]storage.RowEnd
	if snapshotEnds != nil {
		snapshotEndEntries = maps.Clone(snapshotEnds.Rows)
	}
	sessionSQL(t, writer, "DELETE FROM subject WHERE id<3")
	ends := store.Ends
	endEntries := maps.Clone(ends.Rows)
	deletedIndex := e.Storage.IndexRoots[indexRef.Address]
	if deletedIndex == oldIndex || len(deletedIndex.Entries) != 1 || deletedIndex.Entries[0].Row != old[2].Row || deletedIndex.Entries[0].Version != old[2].Version || deletedIndex.Entries[0].Position != 2 {
		t.Fatal("DELETE must publish a distinct one-live-key root without changing old slots", deletedIndex.Entries)
	}
	deletedEntries := copyEntries(deletedIndex.Entries)
	deletedMembership, deletedRowEntries := maps.Clone(deletedIndex.Membership), maps.Clone(deletedIndex.RowEntries)
	assertRetained := func(phase string) {
		t.Helper()
		if !reflect.DeepEqual(old, headers) {
			t.Fatalf("%s: retained headers/provenance/payload values changed: got%#v want%#v", phase, old, headers)
		}
		for i := range old {
			if len(old[i].Values) != 0 && &old[i].Values[0] != &payloads[i].Values[0] {
				t.Fatalf("%s: retained payload backing changed row%d", phase, i)
			}
		}
		if snapshot.Storage != store || !storage.SameVersionRoot(snapshot.Versions, old) || snapshot.Ends != snapshotEnds {
			t.Fatal(phase, "snapshot storage/vector/end ownership changed")
		}
		if snapshotEnds != nil && !reflect.DeepEqual(snapshotEnds.Rows, snapshotEndEntries) {
			t.Fatal(phase, "snapshot-owned end map changed")
		}
		if !reflect.DeepEqual(ends.Rows, endEntries) {
			t.Fatal(phase, "retained committed end map changed", ends.Rows, endEntries)
		}
		if !reflect.DeepEqual(oldIndex.Entries, oldEntries) || !reflect.DeepEqual(oldIndex.Membership, oldMembership) || !reflect.DeepEqual(oldIndex.RowEntries, oldRowEntries) {
			t.Fatal(phase, "pre-delete three-key root changed", oldIndex.Entries, oldEntries)
		}
		if !reflect.DeepEqual(deletedIndex.Entries, deletedEntries) || !reflect.DeepEqual(deletedIndex.Membership, deletedMembership) || !reflect.DeepEqual(deletedIndex.RowEntries, deletedRowEntries) {
			t.Fatal(phase, "post-delete one-key root changed", deletedIndex.Entries, deletedEntries)
		}
	}
	sessionSQL(t, writer, "VACUUM (FREEZE) subject")
	if e.Storage.Relations[ref.Address] != store || len(store.Versions) != 3 || store.Ends != ends || store.Maintenance.Live != 1 || store.Maintenance.Dead != 2 {
		t.Fatal("snapshot-gated current root", store)
	}
	assertRetained("snapshot-gated freeze")
	transactionRows(t, reader, "SELECT id FROM subject ORDER BY id", 1, 2, 3)
	if snapshot.Storage != store || !storage.SameVersionRoot(snapshot.Versions, old) || !reflect.DeepEqual(old, headers) {
		t.Fatal("stable roots/provenance mutated")
	}
	detached := sessionSQL(t, reader, "SELECT id FROM subject ORDER BY id")
	defer detached.Rows.Close()
	sessionSQL(t, reader, "COMMIT")
	sessionSQL(t, writer, "VACUUM subject")
	if len(store.Versions) != 1 || store.Versions[0].Row != old[2].Row || store.Versions[0].Version != old[2].Version || len(store.Ends.Rows) != 0 || store.Maintenance.Dead != 0 {
		t.Fatal("quiescent pass did not retire exact dead heads", store)
	}
	assertRetained("quiescent compaction")
	currentIndex := e.Storage.IndexRoots[indexRef.Address]
	if currentIndex == deletedIndex || len(currentIndex.Entries) != 1 || currentIndex.Entries[0].Position != 0 || currentIndex.Entries[0].Row != old[2].Row || currentIndex.Entries[0].Version != old[2].Version {
		t.Fatal("current index was not remapped independently", currentIndex.Entries)
	}
	if !store.Versions[0].Frozen || store.Versions[0].InsertFullXID != headers[2].InsertFullXID || store.Versions[0].Born != headers[2].Born || &store.Versions[0].Values[0] != &payloads[2].Values[0] {
		t.Fatal("current copied survivor changed provenance/payload or did not freeze", store.Versions[0], headers[2])
	}
	if detached.Rows == nil || len(tuples(detached)) != 3 {
		t.Fatal("detached result ownership lost")
	}
	root, generation := store.Versions, store.Generation
	rowFrontier, versionFrontier := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
	sessionSQL(t, writer, "VACUUM subject")
	if !storage.SameVersionRoot(root, store.Versions) || generation != store.Generation || rowFrontier != e.Storage.RowFrontier() || versionFrontier != e.Storage.VersionFrontier() {
		t.Fatal("no-change pass reallocated roots/identities")
	}
	assertRetained("unchanged repeat")
	// A private/prepared writer conservatively gates unrelated dead-head removal.
	for _, prepare := range []bool{false, true} {
		sessionSQL(t, writer, "INSERT INTO subject VALUES(4)")
		sessionSQL(t, writer, "DELETE FROM subject WHERE id=4")
		sessionSQL(t, reader, "BEGIN")
		sessionSQL(t, reader, "INSERT INTO anchor VALUES(10)")
		if prepare {
			sessionSQL(t, reader, "PREPARE TRANSACTION 'vacuum_gate'")
		}
		sessionSQL(t, writer, "VACUUM subject")
		if len(store.Versions) != 2 {
			t.Fatal("private/prepared writer failed conservative gate")
		}
		if prepare {
			sessionSQL(t, reader, "ROLLBACK PREPARED 'vacuum_gate'")
		} else {
			sessionSQL(t, reader, "ROLLBACK")
		}
		sessionSQL(t, writer, "VACUUM subject")
		if len(store.Versions) != 1 {
			t.Fatal("ended private owner kept dead current head")
		}
	}
	lockEmpty(t, e)
}

func TestVacuumFreezeProvenance(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE provenance(id INT)", "INSERT INTO provenance VALUES(1)", "CREATE TABLE provenance_ctas AS SELECT id FROM provenance", "CREATE MATERIALIZED VIEW provenance_mv AS SELECT id FROM provenance"} {
		sessionSQL(t, s, q)
	}
	for _, name := range []string{"provenance", "provenance_ctas", "provenance_mv"} {
		_, store := vacuumStore(t, e, name)
		row := store.Versions[0]
		e.Mu.Lock()
		state, known, err := e.Visibility.TransactionStatusLocked(row.InsertFullXID)
		e.Mu.Unlock()
		if row.InsertFullXID < 3 || row.Frozen || row.ImportFrozen || err != nil || !known || state != "committed" {
			t.Fatal("birth not actual canonical publisher", name, row, state, err)
		}
	}
	_, store := vacuumStore(t, e, "provenance")
	before := store.Versions[0]
	sessionSQL(t, s, "ALTER TABLE provenance ADD COLUMN note INT DEFAULT 7")
	if store = e.Storage.Relations[catalogRef(t, e, e.CatalogRead(), "provenance").Address]; store.Versions[0].InsertFullXID != before.InsertFullXID {
		t.Fatal("projection changed insertion identity")
	}
	sessionSQL(t, s, "UPDATE provenance SET id=2")
	if store.Versions[0].InsertFullXID == before.InsertFullXID {
		t.Fatal("update reused predecessor publisher")
	}
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "INSERT INTO provenance(id) VALUES(3)")
	sessionSQL(t, s, "PREPARE TRANSACTION 'vacuum_provenance'")
	e.Mu.Lock()
	publisher := e.Visibility.FullXIDLocked(e.prepared.Lookup("vacuum_provenance").Ref)
	e.Mu.Unlock()
	sessionSQL(t, s, "COMMIT PREPARED 'vacuum_provenance'")
	if store.Versions[1].InsertFullXID != publisher {
		t.Fatal("prepared commit lost detached publisher")
	}
	// Distinct domains deliberately disagree: CommitSeq10000, writer order700,
	// canonical active90, committed insertion identities80 and95.
	e.Mu.Lock()
	self := e.Catalog.Domain.Tx(s.SessionToken, 999, e.Catalog.State.Epoch(), s.Transactions, 999)
	e.Visibility.SetLastAssignedXIDLocked(99)
	e.Visibility.Entries[self] = visibility.Entry{XID: 700, FullXID: 90, AssignedXID: 90}
	cutoff, err := e.Visibility.VacuumCutoffLocked()
	e.Mu.Unlock()
	if err != nil || cutoff != 90 {
		t.Fatal(cutoff, err)
	}
	rows := []storage.RowVersion{{Row: 1, Version: 1, Born: 10000, InsertFullXID: 80}, {Row: 2, Version: 2, Born: 10000, InsertFullXID: 95}}
	c, err := vacuumScan(context.Background(), rows, nil, 10000, cutoff, false)
	if err != nil || c.Frozen != 1 || !c.Versions[0].Frozen || c.Versions[1].Frozen || c.Horizon != 90 || rows[0].Frozen || c.Versions[0].InsertFullXID != 80 {
		t.Fatal("unsafe/cross-domain freeze", c, err)
	}
	rows[0].InsertFullXID = 0
	c, err = vacuumScan(context.Background(), rows, nil, 10000, cutoff, false)
	if err != nil || c.Horizon != 0 || c.Frozen != 0 {
		t.Fatal("unknown provenance manufactured horizon", c, err)
	}
	next, err := visibility.NextFullXID(0xffffffff)
	if err != nil || next != 0x100000003 {
		t.Fatal("canonical wrap cutoff", next, err)
	}
	e.Mu.Lock()
	delete(e.Visibility.Entries, self)
	e.Mu.Unlock()
}

func TestVacuumImportHorizons(t *testing.T) {
	source, target := New(), New()
	ss := sessionStart(t, source, nil)
	sessionSQL(t, ss, "CREATE TABLE imported(id INT)")
	sessionSQL(t, ss, "INSERT INTO imported VALUES(1),(2)")
	sessionSQL(t, ss, "DELETE FROM imported WHERE id=1")
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	_, store := vacuumStore(t, target, "imported")
	if len(store.Versions) != 1 || !store.Versions[0].Frozen || !store.Versions[0].ImportFrozen || store.Versions[0].InsertFullXID != 0 || store.Maintenance.FrozenXID < 3 {
		t.Fatal("foreign XIDs crossed target universe", store)
	}
	before := source.Storage.Relations[catalogRef(t, source, source.CatalogRead(), "imported").Address].Versions[1]
	if _, err := source.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	_, self := vacuumStore(t, source, "imported")
	if self.Versions[0].InsertFullXID != before.InsertFullXID || self.Versions[0].Frozen != before.Frozen || self.Versions[0].ImportFrozen != before.ImportFrozen {
		t.Fatal("self replacement forged import provenance")
	}
	ts := sessionStart(t, target, nil)
	sessionSQL(t, ts, "CREATE TABLE unselected(id INT)")
	_, unselected := vacuumStore(t, target, "unselected")
	bound := unselected.Maintenance.FrozenXID
	sessionSQL(t, ts, "INSERT INTO imported VALUES(3)")
	sessionSQL(t, ts, "VACUUM (FREEZE) imported")
	if target.DatabaseFrozenXID > bound {
		t.Fatal("database bound passed unselected relation")
	}
	db := target.DatabaseFrozenXID
	root := store.Versions
	count := store.Maintenance.VacuumCount
	sessionSQL(t, ts, "VACUUM (ONLY_DATABASE_STATS)")
	if !storage.SameVersionRoot(root, store.Versions) || store.Maintenance.VacuumCount != count || target.DatabaseFrozenXID != db {
		t.Fatal("ONLY database stats did main work")
	}
	sessionSQL(t, ts, "DROP TABLE imported")
	sessionSQL(t, ts, "CREATE TABLE imported(id INT)")
	_, fresh := vacuumStore(t, target, "imported")
	if fresh == store || fresh.Maintenance.VacuumCount != 0 || !fresh.Maintenance.LastVacuum.IsZero() {
		t.Fatal("same-name table inherited maintenance")
	}
	// Source captures contain committed visible versions, never unresolved writes.
	sessionSQL(t, ss, "BEGIN")
	sessionSQL(t, ss, "INSERT INTO imported VALUES(9)")
	other := New()
	if _, err := other.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	_, committed := vacuumStore(t, other, "imported")
	if len(committed.Versions) != 1 {
		t.Fatal("imported private source rows")
	}
	sessionSQL(t, ss, "ROLLBACK")
	oldRows := slices.Clone(store.Versions)
	if err := Reset(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if len(target.Storage.Relations) != 0 {
		t.Fatal("Reset retained obsolete relation maintenance")
	}
	if err := Restore(context.Background(), target, strings.NewReader("CREATE TABLE restored(id INT); INSERT INTO restored VALUES(1),(3);")); err != nil {
		t.Fatal(err)
	}
	_, restored := vacuumStore(t, target, "restored")
	if len(restored.Versions) != 2 || restored.Maintenance.VacuumCount != 0 || !restored.Maintenance.LastVacuum.IsZero() {
		t.Fatal("Restore reused vacuum statistics")
	}
	for _, row := range restored.Versions {
		if !row.ImportFrozen || !row.Frozen || row.InsertFullXID != 0 {
			t.Fatal("Restore fabricated target/source XID", row)
		}
	}
	if !reflect.DeepEqual(store.Versions, oldRows) {
		t.Fatal("Reset/Restore mutated retained old roots")
	}
}
