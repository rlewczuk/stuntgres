package engine

import (
	"reflect"
	"stuntgres/internal/engine/storage"
	"testing"
	"time"
)

func TestVacuumMetadataStatistics(t *testing.T) {
	t.Run("materialized_same_object", vacuumMaterializedStatistics)
	t.Run("truncate_same_object", vacuumTruncateStatistics)
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE metadata(id INT)", "INSERT INTO metadata VALUES(1),(2),(3)", "DELETE FROM metadata WHERE id=2"} {
		sessionSQL(t, s, q)
	}
	ref, store := vacuumStore(t, e, "metadata")
	before := store.Maintenance
	if before.Scanned || before.Inserts != 3 || before.Deletes != 1 || before.Modifications != 4 || before.Live != 2 || before.Dead != 1 || before.FrozenXID < 3 {
		t.Fatal("not actual committed logical facts", before)
	}
	start := time.Now().UTC()
	sessionSQL(t, s, "VACUUM (FREEZE) metadata")
	m := store.Maintenance
	if !m.Scanned || m.Live != 2 || m.Dead != 0 || m.VacuumCount != 1 || m.LastVacuum.Before(start) || m.LastVacuum.After(time.Now().UTC()) || m.InsertsSince != 0 || m.Modifications != 4 || m.FrozenXID < before.FrozenXID {
		t.Fatal("maintenance facts", m)
	}
	root, ends, generation := store.Versions, store.Ends, store.Generation
	sessionSQL(t, s, "VACUUM metadata")
	if !storage.SameVersionRoot(root, store.Versions) || ends != store.Ends || generation != store.Generation || store.Maintenance.VacuumCount != 2 {
		t.Fatal("repeat/no-dead metadata", store.Maintenance)
	}
	unchanged := store.Maintenance
	for _, q := range []string{"VACUUM (PROCESS_MAIN false,PROCESS_TOAST false) metadata", "VACUUM (ONLY_DATABASE_STATS)"} {
		sessionSQL(t, s, q)
		if store.Maintenance != unchanged {
			t.Fatal("disabled main/only-database claimed work", q)
		}
	}
	sessionSQL(t, s, "CREATE INDEX metadata_ix ON metadata(id)")
	sessionSQL(t, s, "DROP INDEX metadata_ix")
	d, _ := e.CatalogRead().Relation(ref.Address)
	if !d.History.HasIndexes {
		t.Fatal("DROP eagerly cleared lazy index fact")
	}
	sessionSQL(t, s, "VACUUM metadata")
	d, _ = e.CatalogRead().Relation(ref.Address)
	if d.History.HasIndexes {
		t.Fatal("VACUUM failed to clear last-index history")
	}
	sessionSQL(t, s, "CREATE INDEX metadata_ix ON metadata(id)")
	d, _ = e.CatalogRead().Relation(ref.Address)
	if !d.History.HasIndexes {
		t.Fatal("new index did not restore lazy index fact")
	}
	committed := store.Maintenance
	sessionSQL(t, s, "BEGIN")
	sessionSQL(t, s, "INSERT INTO metadata VALUES(5)")
	sessionSQL(t, s, "DELETE FROM metadata WHERE id=1")
	sessionSQL(t, s, "ROLLBACK")
	if store.Maintenance != committed {
		t.Fatal("rolled-back private work changed committed counters")
	}
	sessionSQL(t, s, "UPDATE metadata SET id=4 WHERE id=3")
	if store.Maintenance.Updates != 1 || store.Maintenance.Modifications != 5 || store.Maintenance.Live != 2 {
		t.Fatal("UPDATE counters fabricated/lost", store.Maintenance)
	}
	// A committed no-change pass retains survivor payload identity and values.
	survivors := append([]storage.RowVersion(nil), store.Versions...)
	sessionSQL(t, s, "VACUUM metadata")
	for i, row := range store.Versions {
		if row.Row != survivors[i].Row || row.Version != survivors[i].Version || row.Born != survivors[i].Born || !reflect.DeepEqual(row.Values, survivors[i].Values) {
			t.Fatal("visible survivor changed", i)
		}
	}
	lockEmpty(t, e)
}
