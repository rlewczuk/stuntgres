package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Package-function regression: transport must retain the catalog owner's
// installed-provider authority, not turn the candidate into an ownerless view.
func cubeIndexTransportAuthority(t *testing.T) {
	e, ext, base := cubeUnitInstalled(t)
	ctx := context.Background()
	state := catalog.CloneState(e.catalog.State)
	view := e.CatalogRead().WithState(state)
	class, ok, defaultErr := view.DefaultIndexClass(403, base.SQLType)
	if defaultErr != nil {
		t.Fatal(defaultErr)
	}
	if !ok {
		t.Fatal("missing cube class")
	}
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 9001000}, ObjectNamespace: ext.TargetNamespace, ObjectRevision: 1, ObjectName: "cube_transport_parent", Owner: ext.Owner, RelationKind: catalog.TableRelation, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "v", Usage: base.SQLType.Usage()}}, Index: map[string]int{"v": 0}}
	definition := &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: parent.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: class}}, Unique: true, Ready: true, Valid: true, Live: true}
	idx := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 9001001}, ObjectNamespace: ext.TargetNamespace, ObjectRevision: 1, ObjectName: "cube_transport_index", Owner: ext.Owner, RelationKind: catalog.IndexRelation, SQLIndex: definition}
	state.SetDescriptor(parent.Address(), parent)
	state.SetDescriptor(idx.Address(), idx)
	value := func(input string) sqltype.Value {
		v, err := base.SQLType.Input(ctx, input, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	one, two := value("1"), value("2")
	sourceStore := storage.Relation{Generation: 1, Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{two}}, {Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{one}}}}
	entries := []storage.IndexEntry{{Keys: []sqltype.Value{one}, Row: 2, Version: 2, Position: 1}, {Keys: []sqltype.Value{two}, Row: 1, Version: 1, Position: 0}}
	snapshot := &index.Snapshot{Sources: map[ids.ObjectAddress]index.SnapshotSource{idx.Address(): {Index: idx, Parent: parent, Store: sourceStore, Root: index.NewLogicalIndexRoot(definition, entries), Frontier: 1}}}
	store := &storage.Relation{Generation: 2, Versions: append([]storage.RowVersion(nil), sourceStore.Versions...)}
	stores := map[ids.ObjectAddress]*storage.Relation{parent.Address(): store}
	positions := map[ids.ObjectAddress]map[index.RowIdentity]int{parent.Address(): {{Row: 1, Version: 1}: 0, {Row: 2, Version: 2}: 1}}
	if _, err := snapshot.Transport(ctx, catalog.NewRead(nil, state), stores, positions); err == nil {
		t.Fatal("ownerless transport manufactured installed authority")
	}
	roots, err := snapshot.TransportRows(ctx, view, stores, positions, true)
	if err != nil {
		t.Fatal("owned generation transport", err)
	}
	if got := roots[idx.Address()].Entries; len(got) != 2 || got[0].Keys[0] != one || got[1].Keys[0] != two {
		t.Fatal("generation transport changed ordered keys", got)
	}
	roots, nextRow, nextVersion, err := index.RemintStorage(ctx, view, index.RemintBase{Catalog: e.catalog.State, Relations: map[ids.ObjectAddress]*storage.Relation{}, NextRow: 100, NextVersion: 200, Frontier: 10}, stores, snapshot)
	if err != nil {
		t.Fatal("owned import remint/unique validation", err)
	}
	got := roots[idx.Address()].Entries
	if nextRow != 102 || nextVersion != 202 || len(got) != 2 || got[0].Keys[0] != one || got[0].Row != 102 || got[1].Keys[0] != two || got[1].Row != 101 {
		t.Fatal("remint lost ordered keys or row identities", nextRow, nextVersion, got)
	}
	if sourceStore.Versions[0].Row != 1 || entries[0].Row != 2 || sourceStore.Versions[0].Values[0] != two {
		t.Fatal("transport mutated captured source authority")
	}
	changes := &session.Journal{}
	called := false
	if err := index.TransportEmptyIndexStorage(ctx, view, changes, func(address ids.ObjectAddress, candidate index.RootCandidate) {
		if address != idx.Address() || candidate.Root == nil || len(candidate.Root.Entries) != 0 {
			t.Fatal("invalid retained empty root", address, candidate)
		}
		called = true
	}, parent.Address(), &storage.Relation{Generation: 3}); err != nil {
		t.Fatal("owned empty storage transport", err)
	}
	if !called {
		t.Fatal("empty root was not retained")
	}
}
