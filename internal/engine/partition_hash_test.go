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
					left, right := &partitionMembership{modulus: a, remainder: r}, &partitionMembership{modulus: b, remainder: s}
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
	if !validHashPartitionBound(&partitionMembership{modulus: 2147483647, remainder: 2147483646}) {
		t.Fatal("sparse maximum bound rejected")
	}
	for _, b := range []partitionMembership{{modulus: 0}, {modulus: 2, remainder: 2}, {modulus: 2, remainder: -1}, {modulus: 2, defaultBound: true}, {modulus: 2, lower: []partitionDatum{{}}}} {
		if validHashPartitionBound(&b) {
			t.Fatal("invalid hash bound accepted", b)
		}
	}
}

func hashPartitionUnitDescriptor(t *testing.T) (CatalogRead, *relationDescriptor, *relationDescriptor) {
	t.Helper()
	e := New()
	catalog := cloneCatalog(e.state)
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
	root := &relationDescriptor{address: ids.ObjectAddress{ClassID: RelationClassID, ObjectID: 25000}, namespace: ids.ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, name: "hash_root", relationKind: partitionedRelation, columns: []ColumnDescriptor{{Name: "k", Usage: typ.Usage(), Nullable: true}}, attributes: []int32{1}, attributeHighWater: 1, index: map[string]int{"k": 0}}
	leaf := *root
	leaf.address.ObjectID++
	leaf.name = "hash_leaf"
	leaf.relationKind = tableRelation
	leaf.membership = &partitionMembership{parent: root.address, strategy: parser.PartitionHash, modulus: 1}
	root.partition = &partitionDefinition{strategy: parser.PartitionHash, keys: []partitionKeyEntry{{attribute: 1, usage: typ.Usage(), class: class, hash: provider, label: "k"}}, children: []ids.ObjectAddress{leaf.address}}
	catalog.descriptors[root.address], catalog.descriptors[leaf.address] = root, &leaf
	return CatalogRead{owner: e, state: catalog}, root, &leaf
}

func TestHashPartitionInvariants(t *testing.T) {
	catalog, root, leaf := hashPartitionUnitDescriptor(t)
	for _, d := range []*relationDescriptor{root, leaf} {
		if err := validatePartitionDescriptor(catalog, d); err != nil {
			t.Fatal(err)
		}
	}
	original := *leaf.membership
	for _, bad := range []partitionMembership{{parent: root.address, modulus: 1}, {parent: root.address, strategy: parser.PartitionHash, modulus: 0}, {parent: root.address, strategy: parser.PartitionHash, modulus: 1, defaultBound: true}} {
		leaf.membership = &bad
		if err := validatePartitionDescriptor(catalog, root); err == nil {
			t.Fatal("invalid child accepted", bad)
		}
	}
	leaf.membership = &original
	evaluation := EvalContext{Context: context.Background()}
	var scratch [32]partitionDatum
	value, err := sqltype.Integer(root.columns[0].Usage.Type, 0)
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
	old := root.partition.keys[0]
	var replacement *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			replacement = typ
			break
		}
	}
	remapped, err := remapPartitionDescriptor(catalog, root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor {
		if typ == old.usage.Type {
			return replacement
		}
		return typ
	}, nil, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if root.partition.keys[0] != old || remapped.partition.keys[0].usage.Type != replacement || remapped.partition.strategy != parser.PartitionHash {
		t.Fatal("mutable or strategy-incorrect remap")
	}
	value, err := sqltype.Integer(replacement, 42)
	if err != nil {
		t.Fatal(err)
	}
	got, err := remapped.partition.keys[0].hash.Hash(context.Background(), value, 0x7a5b22367996dcfd)
	if err != nil || int64(got) != 7363975540656877951 {
		t.Fatal(got, err)
	}
}

func TestHashPartitionIndexRepairTransport(t *testing.T) {
	catalog, root, source := hashPartitionUnitDescriptor(t)
	source.membership.modulus = 2
	destination := *source
	destination.address.ObjectID++
	destination.name = "hash_destination"
	destination.membership = &partitionMembership{parent: root.address, strategy: parser.PartitionHash, modulus: 2, remainder: 1}
	root.partition.children = append(root.partition.children, destination.address)
	catalog.state.descriptors[destination.address] = &destination
	class, _ := sqltype.DefaultIndexClass(source.columns[0].Usage.Type)
	makeIndex := func(parent *relationDescriptor, oid ids.OID) *relationDescriptor {
		index := &relationDescriptor{address: ids.ObjectAddress{ClassID: RelationClassID, ObjectID: oid}, namespace: parent.namespace, name: parent.name + "_key", relationKind: indexRelation,
			sqlIndex: &indexDefinition{parent: parent.address, method: btreeMethod, keys: []indexKey{{attribute: 1, class: class}}, unique: true, ready: true, live: true, valid: true}}
		catalog.state.descriptors[index.address] = index
		return index
	}
	sourceIndex := makeIndex(source, 25100)
	destinationIndex := makeIndex(&destination, 25101)
	value, err := sqltype.Integer(source.columns[0].Usage.Type, 3)
	if err != nil {
		t.Fatal(err)
	}
	movedRow := rowVersion{row: 7, version: 11, born: 1, values: []sqltype.Value{value}}
	storage := map[ids.ObjectAddress]*relationStorage{
		source.address:      {},
		destination.address: {versions: []rowVersion{movedRow}},
	}
	snapshot := &indexSnapshot{sources: map[ids.ObjectAddress]indexSnapshotSource{
		sourceIndex.address:      {index: sourceIndex, parent: source, frontier: 1},
		destinationIndex.address: {index: destinationIndex, parent: &destination, frontier: 1},
	}}
	evaluation := EvalContext{Context: context.Background(), Scope: 1, Frame: 1, catalog: catalog}
	dirty := map[ids.ObjectAddress]bool{source.address: true, destination.address: true}
	if err := snapshot.repair(evaluation.Context, &evaluation, storage, nil, dirty, AuthorizationView{}); err != nil {
		t.Fatal(err)
	}
	// The existing storage remint mutates candidate row identities in place.
	// Repaired snapshot versions must remain the pre-remint transport authority.
	storage[destination.address].versions[0].row = 99
	storage[destination.address].versions[0].version = 100
	captured := snapshot.sources[destinationIndex.address].store.versions[0]
	if captured.row != 7 || captured.version != 11 {
		t.Fatal("repaired snapshot aliases reminted candidate identities", captured.row, captured.version)
	}
	positions := map[ids.ObjectAddress]map[indexRowIdentity]int{
		source.address:      {},
		destination.address: {{row: 7, version: 11}: 0},
	}
	roots, err := snapshot.transport(evaluation.Context, catalog.state, storage, positions)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots[sourceIndex.address].entries) != 0 {
		t.Fatal("emptied source retained index entries")
	}
	entries := roots[destinationIndex.address].entries
	if len(entries) != 1 || entries[0].row != 99 || entries[0].version != 100 || entries[0].keys[0] != value {
		t.Fatal("moved destination did not transport exactly one reminted index entry", entries)
	}
}
