package engine

import (
	"context"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestHashPartitionBounds(t *testing.T) {
	for a := int32(1); a <= 12; a++ {
		for b := a; b <= 24; b++ {
			if b%a != 0 {
				continue
			}
			for r := range a {
				for s := range b {
					left, right := &catalog.PartitionMembership{Modulus: a, Remainder: r}, &catalog.PartitionMembership{Modulus: b, Remainder: s}
					want := false
					for slot := range b {
						want = want || slot%a == r && slot%b == s
					}
					if got := partition.HashOverlap(left, right); got != want {
						t.Fatal(a, b, r, s, got, want)
					}
				}
			}
		}
	}
	if !partition.ValidHashBound(&catalog.PartitionMembership{Modulus: 2147483647, Remainder: 2147483646}) {
		t.Fatal("sparse maximum bound rejected")
	}
	for _, b := range []catalog.PartitionMembership{{Modulus: 0}, {Modulus: 2, Remainder: 2}, {Modulus: 2, Remainder: -1}, {Modulus: 2, DefaultBound: true}, {Modulus: 2, Lower: []catalog.PartitionDatum{{}}}} {
		if partition.ValidHashBound(&b) {
			t.Fatal("invalid hash bound accepted", b)
		}
	}
}

func hashPartitionUnitDescriptor(t *testing.T) (catalog.Read, *catalog.Relation, *catalog.Relation) {
	_, catalog, root, leaf := hashPartitionUnitEngine(t)
	return catalog, root, leaf
}

// hashPartitionUnitEngine is hashPartitionUnitDescriptor with the engine that
// owns the returned view.
func hashPartitionUnitEngine(t *testing.T) (*Engine, catalog.Read, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := New()
	snapshot := catalog.CloneState(e.catalog.state)
	var typ *sqltype.Descriptor
	for _, candidate := range sqltype.Builtins() {
		if candidate.ID() == sqltype.Int4 {
			typ = candidate
			break
		}
	}
	class, _ := sqltype.DefaultHashIndexClass(typ)
	provider, err := sqltype.BindPartitionHash(typ.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	root := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 25000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "hash_root", RelationKind: catalog.PartitionedRelation, Columns: []catalog.Column{{Name: "k", Usage: typ.Usage(), Nullable: true}}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}, Index: map[string]int{"k": 0}}
	leaf := *root
	leaf.ObjectAddress.ObjectID++
	leaf.ObjectName = "hash_leaf"
	leaf.RelationKind = catalog.TableRelation
	leaf.Membership = &catalog.PartitionMembership{Parent: root.Address(), Strategy: parser.PartitionHash, Modulus: 1}
	root.Partition = &catalog.PartitionDefinition{Strategy: parser.PartitionHash, Keys: []catalog.PartitionKeyEntry{{Attribute: 1, Usage: typ.Usage(), Class: class, Hash: provider, Label: "k"}}, Children: []ids.ObjectAddress{leaf.Address()}}
	snapshot.SetDescriptor(root.Address(), root)
	snapshot.SetDescriptor(leaf.Address(), &leaf)
	return e, e.catalog.read(snapshot), root, &leaf
}

func TestHashPartitionInvariants(t *testing.T) {
	view, root, leaf := hashPartitionUnitDescriptor(t)
	for _, d := range []*catalog.Relation{root, leaf} {
		if err := partition.ValidateDescriptor(view, d); err != nil {
			t.Fatal(err)
		}
	}
	original := *leaf.Membership
	for _, bad := range []catalog.PartitionMembership{{Parent: root.Address(), Modulus: 1}, {Parent: root.Address(), Strategy: parser.PartitionHash, Modulus: 0}, {Parent: root.Address(), Strategy: parser.PartitionHash, Modulus: 1, DefaultBound: true}} {
		leaf.Membership = &bad
		if err := partition.ValidateDescriptor(view, root); err == nil {
			t.Fatal("invalid child accepted", bad)
		}
	}
	leaf.Membership = &original
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	var scratch [32]catalog.PartitionDatum
	value, err := sqltype.Integer(root.Columns[0].Usage.Type, 0)
	if err != nil {
		t.Fatal(err)
	}
	key, err := partition.RowKey(root, []sqltype.Value{value}, &evaluation, scratch[:])
	if err != nil {
		t.Fatal(err)
	}
	if key.Hash != 901916981443598768 {
		t.Fatal(key.Hash)
	}
}

func TestHashPartitionRemap(t *testing.T) {
	catalog, root, _ := hashPartitionUnitDescriptor(t)
	old := root.Partition.Keys[0]
	var replacement *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			replacement = typ
			break
		}
	}
	remapped, err := stored.RemapPartitionDescriptor(catalog, root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor {
		if typ == old.Usage.Type {
			return replacement
		}
		return typ
	}, nil, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if root.Partition.Keys[0] != old || remapped.Partition.Keys[0].Usage.Type != replacement || remapped.Partition.Strategy != parser.PartitionHash {
		t.Fatal("mutable or strategy-incorrect remap")
	}
	value, err := sqltype.Integer(replacement, 42)
	if err != nil {
		t.Fatal(err)
	}
	got, err := remapped.Partition.Keys[0].Hash.Hash(context.Background(), value, 0x7a5b22367996dcfd)
	if err != nil || int64(got) != 7363975540656877951 {
		t.Fatal(got, err)
	}
}

func TestHashPartitionIndexRepairTransport(t *testing.T) {
	view, root, source := hashPartitionUnitDescriptor(t)
	source.Membership.Modulus = 2
	destination := *source
	destination.ObjectAddress.ObjectID++
	destination.ObjectName = "hash_destination"
	destination.Membership = &catalog.PartitionMembership{Parent: root.Address(), Strategy: parser.PartitionHash, Modulus: 2, Remainder: 1}
	root.Partition.Children = append(root.Partition.Children, destination.Address())
	view.State().SetDescriptor(destination.Address(), &destination)
	class, _ := sqltype.DefaultIndexClass(source.Columns[0].Usage.Type)
	makeIndex := func(parent *catalog.Relation, oid ids.OID) *catalog.Relation {
		index := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: parent.Namespace(), ObjectName: parent.Name() + "_key", RelationKind: catalog.IndexRelation,
			SQLIndex: &catalog.IndexDefinition{Parent: parent.Address(), Method: catalog.BtreeMethod, Keys: []catalog.IndexKey{{Attribute: 1, Class: class}}, Unique: true, Ready: true, Live: true, Valid: true}}
		view.State().SetDescriptor(index.Address(), index)
		return index
	}
	sourceIndex := makeIndex(source, 25100)
	destinationIndex := makeIndex(&destination, 25101)
	value, err := sqltype.Integer(source.Columns[0].Usage.Type, 3)
	if err != nil {
		t.Fatal(err)
	}
	movedRow := storage.RowVersion{Row: 7, Version: 11, Born: 1, Values: []sqltype.Value{value}}
	relations := map[ids.ObjectAddress]*storage.Relation{
		source.Address():      {},
		destination.Address(): {Versions: []storage.RowVersion{movedRow}},
	}
	snapshot := &index.Snapshot{Sources: map[ids.ObjectAddress]index.SnapshotSource{
		sourceIndex.Address():      {Index: sourceIndex, Parent: source, Frontier: 1},
		destinationIndex.Address(): {Index: destinationIndex, Parent: &destination, Frontier: 1},
	}}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	dirty := map[ids.ObjectAddress]bool{source.Address(): true, destination.Address(): true}
	if err := snapshot.Repair(evaluation.Context, &evaluation, relations, nil, dirty, access.View{}, nil); err != nil {
		t.Fatal(err)
	}
	// The existing storage remint mutates candidate row identities in place.
	// Repaired snapshot versions must remain the pre-remint transport authority.
	relations[destination.Address()].Versions[0].Row = 99
	relations[destination.Address()].Versions[0].Version = 100
	captured := snapshot.Sources[destinationIndex.Address()].Store.Versions[0]
	if captured.Row != 7 || captured.Version != 11 {
		t.Fatal("repaired snapshot aliases reminted candidate identities", captured.Row, captured.Version)
	}
	positions := map[ids.ObjectAddress]map[index.RowIdentity]int{
		source.Address():      {},
		destination.Address(): {{Row: 7, Version: 11}: 0},
	}
	roots, err := snapshot.Transport(evaluation.Context, view.State(), relations, positions)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots[sourceIndex.Address()].Entries) != 0 {
		t.Fatal("emptied source retained index entries")
	}
	entries := roots[destinationIndex.Address()].Entries
	if len(entries) != 1 || entries[0].Row != 99 || entries[0].Version != 100 || entries[0].Keys[0] != value {
		t.Fatal("moved destination did not transport exactly one reminted index entry", entries)
	}
}
