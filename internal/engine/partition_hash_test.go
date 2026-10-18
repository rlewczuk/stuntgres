package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
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
					left, right := &partitionMembership{Modulus: a, Remainder: r}, &partitionMembership{Modulus: b, Remainder: s}
					want := false
					for slot := range b {
						want = want || slot%a == r && slot%b == s
					}
					if got := hashPartitionOverlap(left, right); got != want {
						t.Fatal(a, b, r, s, got, want)
					}
				}
			}
		}
	}
	if !validHashPartitionBound(&partitionMembership{Modulus: 2147483647, Remainder: 2147483646}) {
		t.Fatal("sparse maximum bound rejected")
	}
	for _, b := range []partitionMembership{{Modulus: 0}, {Modulus: 2, Remainder: 2}, {Modulus: 2, Remainder: -1}, {Modulus: 2, DefaultBound: true}, {Modulus: 2, Lower: []partitionDatum{{}}}} {
		if validHashPartitionBound(&b) {
			t.Fatal("invalid hash bound accepted", b)
		}
	}
}

func hashPartitionUnitDescriptor(t *testing.T) (CatalogRead, *relationDescriptor, *relationDescriptor) {
	_, catalog, root, leaf := hashPartitionUnitEngine(t)
	return catalog, root, leaf
}

// hashPartitionUnitEngine is hashPartitionUnitDescriptor with the engine that
// owns the returned view.
func hashPartitionUnitEngine(t *testing.T) (*Engine, CatalogRead, *relationDescriptor, *relationDescriptor) {
	t.Helper()
	e := New()
	catalog := cloneCatalog(e.catalog.state)
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
	root := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 25000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "hash_root", RelationKind: partitionedRelation, Columns: []ColumnDescriptor{{Name: "k", Usage: typ.Usage(), Nullable: true}}, Attributes: []int32{1}, AttributeHighWater: 1, Index: map[string]int{"k": 0}}
	leaf := *root
	leaf.ObjectAddress.ObjectID++
	leaf.ObjectName = "hash_leaf"
	leaf.RelationKind = tableRelation
	leaf.Membership = &partitionMembership{Parent: root.Address(), Strategy: parser.PartitionHash, Modulus: 1}
	root.Partition = &partitionDefinition{Strategy: parser.PartitionHash, Keys: []partitionKeyEntry{{Attribute: 1, Usage: typ.Usage(), Class: class, Hash: provider, Label: "k"}}, Children: []ids.ObjectAddress{leaf.Address()}}
	catalog.SetDescriptor(root.Address(), root)
	catalog.SetDescriptor(leaf.Address(), &leaf)
	return e, e.catalog.read(catalog), root, &leaf
}

func TestHashPartitionInvariants(t *testing.T) {
	catalog, root, leaf := hashPartitionUnitDescriptor(t)
	for _, d := range []*relationDescriptor{root, leaf} {
		if err := validatePartitionDescriptor(catalog, d); err != nil {
			t.Fatal(err)
		}
	}
	original := *leaf.Membership
	for _, bad := range []partitionMembership{{Parent: root.Address(), Modulus: 1}, {Parent: root.Address(), Strategy: parser.PartitionHash, Modulus: 0}, {Parent: root.Address(), Strategy: parser.PartitionHash, Modulus: 1, DefaultBound: true}} {
		leaf.Membership = &bad
		if err := validatePartitionDescriptor(catalog, root); err == nil {
			t.Fatal("invalid child accepted", bad)
		}
	}
	leaf.Membership = &original
	evaluation := EvalContext{Context: context.Background()}
	var scratch [32]partitionDatum
	value, err := sqltype.Integer(root.Columns[0].Usage.Type, 0)
	if err != nil {
		t.Fatal(err)
	}
	key, err := partitionRowKey(root, []sqltype.Value{value}, &evaluation, scratch[:])
	if err != nil {
		t.Fatal(err)
	}
	if key.hash != 901916981443598768 {
		t.Fatal(key.hash)
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
	remapped, err := remapPartitionDescriptor(catalog, root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor {
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
	catalog, root, source := hashPartitionUnitDescriptor(t)
	source.Membership.Modulus = 2
	destination := *source
	destination.ObjectAddress.ObjectID++
	destination.ObjectName = "hash_destination"
	destination.Membership = &partitionMembership{Parent: root.Address(), Strategy: parser.PartitionHash, Modulus: 2, Remainder: 1}
	root.Partition.Children = append(root.Partition.Children, destination.Address())
	catalog.State().SetDescriptor(destination.Address(), &destination)
	class, _ := sqltype.DefaultIndexClass(source.Columns[0].Usage.Type)
	makeIndex := func(parent *relationDescriptor, oid ids.OID) *relationDescriptor {
		index := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: parent.Namespace(), ObjectName: parent.Name() + "_key", RelationKind: indexRelation,
			SQLIndex: &indexDefinition{Parent: parent.Address(), Method: btreeMethod, Keys: []indexKey{{Attribute: 1, Class: class}}, Unique: true, Ready: true, Live: true, Valid: true}}
		catalog.State().SetDescriptor(index.Address(), index)
		return index
	}
	sourceIndex := makeIndex(source, 25100)
	destinationIndex := makeIndex(&destination, 25101)
	value, err := sqltype.Integer(source.Columns[0].Usage.Type, 3)
	if err != nil {
		t.Fatal(err)
	}
	movedRow := rowVersion{row: 7, version: 11, born: 1, values: []sqltype.Value{value}}
	storage := map[ids.ObjectAddress]*relationStorage{
		source.Address():      {},
		destination.Address(): {versions: []rowVersion{movedRow}},
	}
	snapshot := &indexSnapshot{sources: map[ids.ObjectAddress]indexSnapshotSource{
		sourceIndex.Address():      {index: sourceIndex, parent: source, frontier: 1},
		destinationIndex.Address(): {index: destinationIndex, parent: &destination, frontier: 1},
	}}
	evaluation := EvalContext{Context: context.Background(), Scope: 1, Frame: 1, catalog: catalog}
	dirty := map[ids.ObjectAddress]bool{source.Address(): true, destination.Address(): true}
	if err := snapshot.repair(evaluation.Context, &evaluation, storage, nil, dirty, AuthorizationView{}); err != nil {
		t.Fatal(err)
	}
	// The existing storage remint mutates candidate row identities in place.
	// Repaired snapshot versions must remain the pre-remint transport authority.
	storage[destination.Address()].versions[0].row = 99
	storage[destination.Address()].versions[0].version = 100
	captured := snapshot.sources[destinationIndex.Address()].store.versions[0]
	if captured.row != 7 || captured.version != 11 {
		t.Fatal("repaired snapshot aliases reminted candidate identities", captured.row, captured.version)
	}
	positions := map[ids.ObjectAddress]map[indexRowIdentity]int{
		source.Address():      {},
		destination.Address(): {{row: 7, version: 11}: 0},
	}
	roots, err := snapshot.transport(evaluation.Context, catalog.State(), storage, positions)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots[sourceIndex.Address()].entries) != 0 {
		t.Fatal("emptied source retained index entries")
	}
	entries := roots[destinationIndex.Address()].entries
	if len(entries) != 1 || entries[0].row != 99 || entries[0].version != 100 || entries[0].keys[0] != value {
		t.Fatal("moved destination did not transport exactly one reminted index entry", entries)
	}
}
