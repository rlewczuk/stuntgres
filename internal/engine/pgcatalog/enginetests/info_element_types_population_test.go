package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func TestInfoElementTypesProducer(t *testing.T) {
	f := newElementUnitFixture(t)
	in := elementUnitInput(t, f.f, f.state, f.f.owner)
	source := in.Source
	if source.RowType != 13516 || source.Namespace().ObjectID != 13229 || !source.VirtualView || source.RelationKind != catalog.CatalogRelation {
		t.Fatal("source identity", source)
	}
	rowType, _ := in.Catalog.LookupType(13516)
	array, _ := in.Catalog.LookupType(13515)
	if rowType.AssociatedArray() != array.ID() || array.Element() != rowType || in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13516}).OwningRelation != source.Address() {
		t.Fatal("canonical row/array backlinks")
	}
	for i, column := range source.Columns {
		domain, _ := in.Catalog.LookupType(elementUnitDomains[i])
		baseID, collation := sqltype.Name, uint32(950)
		if elementUnitDomains[i] == 13246 {
			baseID = sqltype.VarChar
		}
		if elementUnitDomains[i] == 13243 {
			baseID, collation = sqltype.Int4, 0
		}
		base, _ := in.Catalog.LookupType(baseID)
		if column.Name != elementUnitNames[i] || column.Usage.Type != domain || domain.DomainBase().Type != base || domain.OwnDomainNotNull() || column.Usage.Typmod != -1 || column.Usage.Collation.ID != collation || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != 0 || column.OriginTable != uint32(pgcatalog.InfoElementTypesOID) || column.OriginAttribute != int16(i+1) || source.Attributes[i] != int32(i+1) {
			t.Fatal("exact source domain/attribute", i, column)
		}
	}
	want := [][]any{
		elementUnitExpected("default_schema", "element_array", "DOMAIN", 1, "integer", "pg_catalog", "int4"),
		elementUnitExpected("default_schema", "element_table", "TABLE", 1, "integer", "pg_catalog", "int4"),
		elementUnitExpected("default_schema", "element_table", "TABLE", 4, "USER-DEFINED", "default_schema", "element_scalar"),
		elementUnitExpected("default_schema", "element_table", "TABLE", 5, "USER-DEFINED", "default_schema", "element_array"),
		elementUnitExpected("default_schema", "element_composite", "USER-DEFINED TYPE", 2, "integer", "pg_catalog", "int4"),
		elementUnitExpected("default_schema", "element_composite", "USER-DEFINED TYPE", 4, "text", "pg_catalog", "text"),
		elementUnitExpected("default_schema", "element_result_48103", "ROUTINE", 0, "integer", "pg_catalog", "int4"),
	}
	for _, x := range []struct {
		ordinal            int
		data, schema, name string
	}{
		{1, "integer", "pg_catalog", "int4"}, {2, "text", "pg_catalog", "text"},
		{3, "USER-DEFINED", "default_schema", "element_scalar"}, {5, "bigint", "pg_catalog", "int8"},
	} {
		want = append(want, elementUnitExpected("default_schema", "element_routine_48101", "ROUTINE", x.ordinal, x.data, x.schema, x.name))
	}
	for _, actor := range []authorization.PrincipalRef{f.f.owner, f.f.member} {
		elementUnitAssert(t, elementUnitRows(t, elementUnitInput(t, f.f, f.state, actor), "default_schema"), want...)
	}
	elementUnitAssert(t, elementUnitRows(t, elementUnitInput(t, f.f, f.state, f.f.reader), "default_schema"))
	for _, privilege := range []authorization.Privileges{authorization.SelectPrivilege, authorization.InsertPrivilege, authorization.UpdatePrivilege, authorization.ReferencesPrivilege, authorization.DeletePrivilege} {
		state := catalog.CloneState(f.state)
		relation := *f.table
		relation.Security.ColumnACL = map[int32]authorization.ACLValue{4: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, privilege, 0)}}}
		state.SetDescriptor(relation.Address(), &relation)
		rows := elementUnitRows(t, elementUnitInput(t, f.f, state, f.f.reader), "default_schema")
		if privilege == authorization.DeletePrivilege {
			elementUnitAssert(t, rows)
		} else {
			elementUnitAssert(t, rows, want[2])
		}
	}
}

func TestInfoElementTypesNominalBoundaries(t *testing.T) {
	f := newElementUnitFixture(t)
	for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.ViewRelation, catalog.ForeignRelation, catalog.PartitionedRelation, catalog.MaterializedViewRelation, catalog.IndexRelation, catalog.SequenceRelation} {
		state := catalog.CloneState(f.state)
		relation := *f.table
		relation.RelationKind = kind
		relation.Columns = append([]catalog.Column(nil), relation.Columns...)
		relation.Attributes = []int32{2, 3, 4, 5}
		relation.ColumnMeta.Dropped = map[int32]catalog.AttributeMetadata{2: {Number: 2}}
		if kind == catalog.IndexRelation {
			// ClassKind authenticates an index's parent to distinguish ordinary
			// and partitioned indexes; a kind tag alone is not an index fixture.
			parent := *f.table
			parent.ObjectAddress.ObjectID = 48104
			parent.ObjectName = "element_index_parent"
			state.SetDescriptor(parent.Address(), &parent)
			relation.SQLIndex = &catalog.IndexDefinition{Parent: parent.Address(), Method: catalog.BtreeMethod, MethodOID: 403, Keys: []catalog.IndexKey{{Attribute: 4}}}
		}
		state.SetDescriptor(relation.Address(), &relation)
		rows := elementUnitRows(t, elementUnitInput(t, f.f, state, f.f.owner), "default_schema")
		var selected [][]sqltype.Value
		for _, row := range rows {
			if columnsUnitCell(t, row[2]) == relation.Name() {
				selected = append(selected, row)
			}
		}
		if kind == catalog.TableRelation || kind == catalog.ViewRelation || kind == catalog.ForeignRelation || kind == catalog.PartitionedRelation {
			elementUnitAssert(t, selected, elementUnitExpected("default_schema", relation.Name(), "TABLE", 4, "USER-DEFINED", "default_schema", "element_scalar"), elementUnitExpected("default_schema", relation.Name(), "TABLE", 5, "USER-DEFINED", "default_schema", "element_array"))
		} else {
			elementUnitAssert(t, selected)
		}
	}
	state := catalog.CloneState(f.state)
	view := f.f.e.Catalog.Read(state)
	vectors := *f.table
	vectors.ObjectName = "element_vectors"
	vectors.Columns, vectors.Attributes = nil, nil
	for i, id := range []sqltype.ID{22, 30, 19} {
		typ, _ := view.LookupType(id)
		vectors.Columns = append(vectors.Columns, catalog.Column{Name: typ.Name(), Usage: typ.Usage(), Nullable: true, NullabilityKnown: true})
		vectors.Attributes = append(vectors.Attributes, int32(i+1))
	}
	state.SetDescriptor(vectors.Address(), &vectors)
	pseudo := *f.routine
	pseudo.ObjectName = "element_pseudo"
	pseudo.Result, pseudo.Signature = 2277, "2277,5078"
	pseudo.SQL = &proc.Definition{}
	anyarray, _ := view.LookupType(2277)
	compatible, _ := view.LookupType(5078)
	if err := pseudo.SQL.SetFormals([]proc.Formal{{Mode: "i", Name: "a", Usage: anyarray.Usage()}, {Mode: "i", Name: "b", Usage: compatible.Usage()}}); err != nil {
		t.Fatal(err)
	}
	state.SetDescriptor(pseudo.Address(), &pseudo)
	var selected [][]sqltype.Value
	for _, row := range elementUnitRows(t, elementUnitInput(t, f.f, state, f.f.owner), "default_schema") {
		if columnsUnitCell(t, row[2]) == "element_vectors" || columnsUnitCell(t, row[2]) == "element_pseudo_48101" {
			selected = append(selected, row)
		}
	}
	elementUnitAssert(t, selected, elementUnitExpected("default_schema", "element_vectors", "TABLE", 1, "smallint", "pg_catalog", "int2"), elementUnitExpected("default_schema", "element_vectors", "TABLE", 2, "oid", "pg_catalog", "oid"))
	// The same declaration and ACL remain visible only to its physical TEMP owner.
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 48200}, ObjectName: "pg_temp_element", ObjectRevision: 1, Owner: f.f.owner, Temporary: view.Owner().Domain.Session(100)}
	state.SetDescriptor(ns.Address(), ns)
	vectors.ObjectNamespace = ns.Address()
	state.SetDescriptor(vectors.Address(), &vectors)
	in := elementUnitInput(t, f.f, state, f.f.owner)
	elementUnitAssert(t, elementUnitRows(t, in, ns.Name()))
	runtime := core.CatalogRuntimeFieldsOf(in.Runtime)
	runtime.Temporary = ns.Temporary
	in.Runtime = core.NewCatalogRuntime(runtime)
	elementUnitAssert(t, elementUnitRows(t, in, ns.Name()), elementUnitExpected(ns.Name(), "element_vectors", "TABLE", 1, "smallint", "pg_catalog", "int2"), elementUnitExpected(ns.Name(), "element_vectors", "TABLE", 2, "oid", "pg_catalog", "oid"))
}
