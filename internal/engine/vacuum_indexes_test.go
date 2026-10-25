package engine

import (
	"context"
	"reflect"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

// vacuumScan is one VACUUM heap pass over rows: inspection, then
// materialization when it removes or freezes a version.
func vacuumScan(ctx context.Context, rows []storage.RowVersion, ends *storage.DeletionRoot, frontier ids.CommitSeq, cutoff uint64, retire bool) (storage.VacuumCandidate, error) {
	c, err := storage.InspectVacuum(ctx, rows, ends, frontier, cutoff, retire)
	if err != nil {
		return c, err
	}
	return storage.MaterializeVacuum(ctx, rows, ends, frontier, cutoff, c)
}

func TestVacuumCompactionIndexes(t *testing.T) {
	e := New()
	bytea, _ := e.CatalogRead().LookupType(sqltype.Bytea)
	value, err := bytea.Input(context.Background(), `\x010203`, -1)
	if err != nil {
		t.Fatal(err)
	}
	payload := []sqltype.Value{value, sqltype.Null(bytea)}
	rows := []storage.RowVersion{
		{Row: 1, Version: 10, Born: 1, InsertFullXID: 3, Values: payload},
		{Row: 2, Version: 20, Born: 1, InsertFullXID: 3, Values: payload},
		{Row: 3, Version: 30, Born: 1, InsertFullXID: 3, Values: nil},
	}
	ends := &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{1: {Version: 10, End: 2}, 2: {Version: 19, End: 2}, 99: {Version: 99, End: 2}}}
	store := &storage.Relation{Versions: rows, Ends: ends, Generation: 5}
	old := slices.Clone(rows)
	candidate, err := vacuumScan(context.Background(), rows, ends, 2, 4, true)
	if err != nil || candidate.Removed != 1 || candidate.Live != 2 || candidate.Dead != 0 || len(candidate.Versions) != 2 || len(candidate.Ends.Rows) != 2 {
		t.Fatal(candidate, err)
	}
	if candidate.Versions[0].Row != 2 || candidate.Versions[0].Version != 20 || candidate.Versions[0].Born != 1 || candidate.Versions[0].InsertFullXID != 3 || &candidate.Versions[0].Values[0] != &payload[0] || candidate.Versions[1].Values != nil {
		t.Fatal("survivor identity/payload/zero-column lost")
	}
	entries := []storage.IndexEntry{{Row: 1, Version: 10, Position: 0, Keys: payload}, {Row: 2, Version: 20, Position: 1, Keys: payload}, {Row: 3, Version: 30, Position: 2}}
	root := &storage.IndexRoot{Entries: entries, Membership: map[ids.RowID]ids.VersionID{1: 10, 2: 20}, RowEntries: map[ids.RowID]int{1: 0, 2: 1, 3: 2}}
	remapped, err := storage.RemapVacuumIndex(context.Background(), root, rows, candidate, store, 6)
	if err != nil || len(remapped.Entries) != 2 || remapped.Entries[0].Position != 0 || remapped.Entries[1].Position != 1 || remapped.RowEntries[2] != 0 || len(remapped.Membership) != 1 || remapped.Membership[2] != 20 {
		t.Fatal("btree/GiST/partial/expression remap", remapped, err)
	}
	bucket := []storage.HashEntry{{Row: 1, Version: 10, Position: 0, Key: value}, {Row: 2, Version: 20, Position: 1, Key: value}}
	hash := &storage.IndexRoot{Hash: &storage.HashRoot{Storage: store, Generation: 5, Prefix: 3, Ends: ends, Buckets: map[uint64][]storage.HashEntry{7: bucket}, RowEntries: map[ids.RowID]*storage.HashEntry{1: &bucket[0], 2: &bucket[1]}}}
	next, err := storage.RemapVacuumIndex(context.Background(), hash, rows, candidate, store, 6)
	if err != nil || len(next.Hash.Buckets[7]) != 1 || next.Hash.Buckets[7][0].Position != 0 || next.Hash.RowEntries[2] != &next.Hash.Buckets[7][0] || next.Hash.Storage != store || next.Hash.Generation != 6 || next.Hash.Prefix != 2 || next.Hash.Ends != candidate.Ends {
		t.Fatal("hash pointer/backing identity remap", next, err)
	}
	prefixRoot, prefixHash := *hash, *hash.Hash
	prefixRoot.Hash = &prefixHash
	prefixHash.Prefix = 2
	partial, err := storage.RemapVacuumIndex(context.Background(), &prefixRoot, rows, candidate, store, 6)
	if err != nil || partial.Hash.Prefix != 1 || prefixHash.Prefix != 2 {
		t.Fatal("incomplete hash coverage was promoted to full coverage", partial, err)
	}
	freezeOnly, err := vacuumScan(context.Background(), rows, ends, 2, 4, false)
	if err != nil || freezeOnly.Positions != nil || freezeOnly.Removed != 0 {
		t.Fatal("freeze-only scan compacted protected positions", freezeOnly, err)
	}
	unchanged, err := storage.RemapVacuumIndex(context.Background(), root, rows, freezeOnly, store, 6)
	if err != nil || unchanged != root {
		t.Fatal("freeze-only B-tree unnecessarily remapped", err)
	}
	rebound, err := storage.RemapVacuumIndex(context.Background(), &prefixRoot, rows, freezeOnly, store, 6)
	if err != nil || rebound.Hash.Prefix != 2 || rebound.Hash.Generation != 6 || rebound.Hash.RowEntries[2] != prefixHash.RowEntries[2] {
		t.Fatal("freeze-only hash binding lost original coverage/pointers", rebound, err)
	}
	if !reflect.DeepEqual(rows, old) || len(ends.Rows) != 3 || len(root.Entries) != 3 || root.Entries[1].Position != 1 || hash.Hash.RowEntries[2] != &bucket[1] {
		t.Fatal("retained root mutated")
	}
	// A mismatched-version marker is not removal authority, including Born0.
	for _, row := range []storage.RowVersion{{Row: 1, Version: 11, Born: 1, InsertFullXID: 3}, {Row: 1, Version: 10}} {
		c, err := vacuumScan(context.Background(), []storage.RowVersion{row}, ends, 2, 4, true)
		if err != nil || c.Removed != 0 || len(c.Versions) != 1 {
			t.Fatal("wrong-version/private removal", c, err)
		}
	}
	allEnds := &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{1: {Version: 10, End: 2}, 2: {Version: 20, End: 2}, 3: {Version: 30, End: 2}}}
	all, err := vacuumScan(context.Background(), rows, allEnds, 2, 4, true)
	if err != nil || all.Removed != 3 || len(all.Versions) != 0 || len(all.Ends.Rows) != 0 {
		t.Fatal("all-dead compaction", all, err)
	}
	empty, err := vacuumScan(context.Background(), nil, nil, 2, 4, true)
	if err != nil || empty.Versions != nil || empty.Ends != nil || empty.Positions != nil {
		t.Fatal("empty pass allocated", empty, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := vacuumScan(canceled, rows, ends, 2, 4, true); err != context.Canceled {
		t.Fatal("scan ignored cancellation", err)
	}
	// Actual attached index forms retain validity and never reevaluate domains.
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE SEQUENCE vacuum_calls", "CREATE DOMAIN vacuum_checked AS INT CHECK(nextval('vacuum_calls')>0)", "CREATE TABLE indexed(id INT)", "INSERT INTO indexed VALUES(1),(2),(3)", "CREATE INDEX vi_expression ON indexed(((id::vacuum_checked)::INT))", "CREATE INDEX vi_partial ON indexed(id) WHERE id>1", "CREATE INDEX vi_hash ON indexed USING hash(id)"} {
		sessionSQL(t, s, q)
	}
	beforeCalls := tuples(sessionSQL(t, s, "SELECT currval('vacuum_calls')"))
	ref, actual := vacuumStore(t, e, "indexed")
	identity := actual
	sessionSQL(t, s, "DELETE FROM indexed WHERE id=1")
	sessionSQL(t, s, "VACUUM (INDEX_CLEANUP OFF) indexed")
	if e.Storage.Relations[ref.Address] != identity || len(actual.Versions) != 2 {
		t.Fatal("index maintenance replaced storage/retained dead heads")
	}
	if !reflect.DeepEqual(beforeCalls, tuples(sessionSQL(t, s, "SELECT currval('vacuum_calls')"))) {
		t.Fatal("VACUUM reevaluated user domain/index expressions")
	}
	for _, name := range []string{"vi_expression", "vi_partial", "vi_hash"} {
		indexRef := catalogRef(t, e, e.CatalogRead(), name)
		definition, _ := e.CatalogRead().Relation(indexRef.Address)
		if !definition.SQLIndex.Valid || !definition.SQLIndex.Ready {
			t.Fatal("VACUUM altered index validity")
		}
	}
}
