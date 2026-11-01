package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestInfoElementTypesMetadataAdmission(t *testing.T) {
	f := newElementUnitFixture(t)
	state := catalog.CloneState(f.state)
	view := f.f.e.Catalog.Read(state)
	lookup := func(id sqltype.ID) *sqltype.Descriptor {
		t.Helper()
		typ, ok := view.LookupType(id)
		if !ok {
			t.Fatal("missing real builtin identity", id)
		}
		return typ
	}
	// Genuine lookup-only identities are not invented catalog metadata. Their
	// absence cannot abort an unrelated admitted array declaration. Conversely,
	// lookup-only codecs do not exclude arrays with real canonical type facts.
	gtsvector := lookup(3642)
	if _, admitted := gtsvector.PGTypeMetadata(); admitted {
		t.Fatal("fixture no longer distinguishes lookup from metadata admission")
	}
	refcursorArray := lookup(2201)
	if refcursorArray.Concrete() {
		t.Fatal("fixture no longer distinguishes metadata from codec admission")
	}
	relation := *f.table
	relation.ObjectName = "element_admission"
	relation.Columns, relation.Attributes = nil, nil
	for i, id := range []sqltype.ID{3642, 3644, 1007, 22, 30, 2201, 273, 19, 2277} {
		typ := lookup(id)
		relation.Columns = append(relation.Columns, catalog.Column{Name: typ.Name(), Usage: typ.Usage(), Nullable: true, NullabilityKnown: true})
		relation.Attributes = append(relation.Attributes, int32(i+1))
	}
	state.SetDescriptor(relation.Address(), &relation)
	// Both immediate scalar and array bases without admitted element facts
	// remain metadata-only declarations, not new codec or pg_type population.
	for i, id := range []sqltype.ID{3642, 3644} {
		name := "element_unadmitted_" + lookup(id).Name()
		typ := sqltype.LookupDomain(sqltype.ID(48300+i), name, "default_schema."+name, lookup(id).Usage())
		domain := &catalog.Type{SQLType: typ, ObjectNamespace: f.f.schema, ObjectRevision: 1, Owner: f.f.owner, ACL: authorization.ACLValue{Explicit: true}}
		state.SetDescriptor(domain.Address(), domain)
		state.BindName(catalog.DescriptorNameKey(domain), domain.Address())
	}
	var selected [][]sqltype.Value
	for _, row := range elementUnitRows(t, elementUnitInput(t, f.f, state, f.f.owner), "default_schema") {
		name := columnsUnitCell(t, row[2])
		if name == "element_admission" || name == "element_unadmitted_gtsvector" || name == "element_unadmitted__gtsvector" {
			selected = append(selected, row)
		}
	}
	elementUnitAssert(t, selected,
		elementUnitExpected("default_schema", "element_admission", "TABLE", 3, "integer", "pg_catalog", "int4"),
		elementUnitExpected("default_schema", "element_admission", "TABLE", 4, "smallint", "pg_catalog", "int2"),
		elementUnitExpected("default_schema", "element_admission", "TABLE", 5, "oid", "pg_catalog", "oid"),
		elementUnitExpected("default_schema", "element_admission", "TABLE", 6, "refcursor", "pg_catalog", "refcursor"),
		elementUnitExpected("default_schema", "element_admission", "TABLE", 7, "pg_class", "pg_catalog", "pg_class"),
	)
	// Admission is not an error-suppression boundary. A foreign immediate base
	// underneath an otherwise canonical domain/array chain must still reject
	// the complete capture, even if the terminal builtin has no metadata.
	foreign := engine.New()
	bad := catalog.CloneState(state)
	leaf := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 3642})
	leaf.SQLType, _ = foreign.CatalogRead().LookupType(3642)
	bad.SetDescriptor(leaf.Address(), &leaf)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoElementTypesOID)
	if !ok {
		t.Fatal("missing producer")
	}
	if rows, err := produce(elementUnitInput(t, f.f, bad, f.f.owner)); rows != nil || err == nil {
		t.Fatal("foreign non-admitted element generation was suppressed", rows, err)
	}
}
