package typedef

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func backendMemoryReownFixture(t *testing.T) (*catalog.State, *catalog.Type, *catalog.Type, *catalog.Relation) {
	t.Helper()
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() { types[d.ID()] = d }
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectName: "pg_catalog"}
	owner := (&authorization.Domain{}).Principal(10)
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12142}, ObjectNamespace: namespace.Address(), ObjectName: "pg_backend_memory_contexts", RelationKind: catalog.ViewRelation, RowType: 12144, Owner: owner}
	names := [...]string{"name", "ident", "type", "level", "path", "total_bytes", "total_nblocks", "free_bytes", "free_chunks", "used_bytes"}
	typeIDs := [...]sqltype.ID{25, 25, 25, 23, 1007, 20, 20, 20, 20, 20}
	for i, name := range names {
		relation.Columns = append(relation.Columns, catalog.Column{Name: name, Usage: types[typeIDs[i]].Usage()})
		relation.Attributes = append(relation.Attributes, int32(i+1))
	}
	rowType, arrayType, err := sqltype.CompleteBuiltinComposite(types[12144], types[12143], CompositeMembers(relation))
	if err != nil { t.Fatal(err) }
	row := &catalog.Type{SQLType: rowType, Array: 12143, OwningRelation: relation.Address(), Owner: owner, Comment: "row metadata", ObjectRevision: 7, ACLSyntheticDefault: true}
	array := &catalog.Type{SQLType: arrayType, OwningRelation: relation.Address(), Owner: owner, Comment: "array metadata", ObjectRevision: 9}
	state := &catalog.State{}
	for _, typ := range types {
		d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
		state.SetDescriptor(d.Address(), d)
	}
	for _, d := range []catalog.Descriptor{namespace, relation, row, array} { state.SetDescriptor(d.Address(), d) }
	return state, row, array, relation
}

func TestBackendMemoryBuiltinPairReownership(t *testing.T) {
	for _, arrayFirst := range []bool{false, true} {
		for _, mode := range []string{"unchanged", "stage_generation", "import", "import_generation", "owner_mapping", "retained_import", "retained_owner_mapping"} {
			t.Run(map[bool]string{false:"row_first", true:"array_first"}[arrayFirst]+"/"+mode, func(t *testing.T) {
				source, row, array, relation := backendMemoryReownFixture(t)
				next := catalog.CloneState(source)
				if mode == "stage_generation" || mode == "import_generation" {
					for _, d := range sqltype.Builtins() {
						if d.ID() == 25 || d.ID() == 23 || d.ID() == 1007 || d.ID() == 20 {
							copy := *source.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.ID())})
							copy.SQLType = d
							next.SetDescriptor(copy.Address(), &copy)
						}
					}
				}
				mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
				owningRelation := relation.Address()
				if mode == "owner_mapping" || mode == "retained_owner_mapping" {
					owningRelation.ObjectID = 50000
					mapping[relation.Address()] = owningRelation
				}
				roots := []catalog.Descriptor{row, array}
				if arrayFirst { roots[0], roots[1] = roots[1], roots[0] }
				for _, id := range []ids.OID{25, 23, 1007, 20} { roots = append(roots, next.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id})) }
				var entered []ids.ObjectAddress
				ordered := func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
					for _, d := range roots {
						entered = append(entered, d.Address())
						if !yield(d.Address(), d) { return }
					}
				}
				retainedPair := mode == "retained_import" || mode == "retained_owner_mapping"
				var retained map[sqltype.ID]*sqltype.Descriptor
				if retainedPair {
					retained = make(map[sqltype.ID]*sqltype.Descriptor)
					for _, descriptor := range source.Descriptors() {
						if owner, ok := descriptor.(*catalog.Type); ok && !owner.SQLType.IsDomain() && !(owner.SQLType.IsArray() && owner.SQLType.Element().IsDomain()) {
							retained[owner.SQLType.ID()] = owner.SQLType
						}
					}
				}
				importing := mode == "import" || mode == "import_generation" || retainedPair
				changed, err := reownDomainTypes(next, mapping, importing, nil, retained, ordered)
				wantChanged := mode != "unchanged" && mode != "retained_import"
				if err != nil || changed != wantChanged { t.Fatal("pair traversal", changed, err) }
				if len(entered) != len(roots) || entered[0] != roots[0].Address() || entered[1] != roots[1].Address() { t.Fatal("root order not exercised", entered) }
				currentRow, currentArray := next.MustType(row.Address()), next.MustType(array.Address())
				if currentArray.SQLType.Element() != currentRow.SQLType || !currentRow.SQLType.IsBuiltin() || !currentArray.SQLType.IsBuiltin() || !currentRow.SQLType.Concrete() || !currentArray.SQLType.Concrete() { t.Fatal("builtin nominal pair authority lost") }
				if changed && !retainedPair && (currentRow.SQLType == row.SQLType || currentArray.SQLType == array.SQLType) { t.Fatal("required destination pair not minted") }
				if retainedPair && (currentRow.SQLType != row.SQLType || currentArray.SQLType != array.SQLType) { t.Fatal("retained destination pair reminted") }
				if !changed && (currentRow != row || currentArray != array) { t.Fatal("unchanged traversal copied nominal owners") }
				for i, column := range relation.Columns {
					usage := column.Usage
					usage.Type = next.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())}).SQLType
					member := currentRow.SQLType.CompositeMember(i)
					if member.Name != column.Name || member.Attribute != relation.Attributes[i] || member.Usage != usage { t.Fatal("member not destination-owned", i) }
				}
				expectedRow, expectedArray := *row, *array
				expectedRow.SQLType, expectedArray.SQLType = currentRow.SQLType, currentArray.SQLType
				expectedRow.OwningRelation, expectedArray.OwningRelation = owningRelation, owningRelation
				if !reflect.DeepEqual(expectedRow, *currentRow) || !reflect.DeepEqual(expectedArray, *currentArray) { t.Fatal("catalog metadata/provenance changed") }
				if source.MustType(row.Address()) != row || source.MustType(array.Address()) != array || array.SQLType.Element() != row.SQLType || row.OwningRelation != relation.Address() { t.Fatal("source pair mutated") }
				for i, column := range relation.Columns { if row.SQLType.CompositeMember(i).Usage != column.Usage { t.Fatal("source members mutated") } }
			})
		}
	}
}

func TestBackendMemoryBuiltinPairRemapRejection(t *testing.T) {
	for _, arrayFirst := range []bool{false, true} {
		for _, from := range []ids.ObjectAddress{{ClassID: ids.TypeClassID, ObjectID: 12144}, {ClassID: ids.TypeClassID, ObjectID: 12143}, {ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}} {
			state, row, array, _ := backendMemoryReownFixture(t)
			next := catalog.CloneState(state)
			to := from
			to.ObjectID = 50000
			roots := []catalog.Descriptor{row, array}
			if arrayFirst { roots[0], roots[1] = roots[1], roots[0] }
			ordered := func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) { for _, d := range roots { if !yield(d.Address(), d) { return } } }
			if _, err := reownDomainTypes(next, map[ids.ObjectAddress]ids.ObjectAddress{from: to}, true, nil, nil, ordered); err == nil { t.Fatal("builtin type/namespace remapping accepted", from, arrayFirst) }
			if next.MustType(row.Address()) != row || next.MustType(array.Address()) != array { t.Fatal("rejected mapping mutated pair") }
		}
	}
}
