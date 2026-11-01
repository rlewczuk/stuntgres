package enginetests_test

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

var elementUnitNames = []string{"object_catalog", "object_schema", "object_name", "object_type", "collection_type_identifier", "data_type", "character_maximum_length", "character_octet_length", "character_set_catalog", "character_set_schema", "character_set_name", "collation_catalog", "collation_schema", "collation_name", "numeric_precision", "numeric_precision_radix", "numeric_scale", "datetime_precision", "interval_type", "interval_precision", "udt_catalog", "udt_schema", "udt_name", "scope_catalog", "scope_schema", "scope_name", "maximum_cardinality", "dtd_identifier"}
var elementUnitDomains = []sqltype.ID{13248, 13248, 13248, 13246, 13248, 13246, 13243, 13243, 13248, 13248, 13248, 13248, 13248, 13248, 13243, 13243, 13243, 13243, 13246, 13243, 13248, 13248, 13248, 13248, 13248, 13248, 13243, 13248}

type elementUnitFixture struct {
	f         defaultUnitFixture
	state     *catalog.State
	table     *catalog.Relation
	domain    *catalog.Type
	routine   *catalog.Procedure
	composite *catalog.Relation
}

func elementUnitInput(t *testing.T, f defaultUnitFixture, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoElementTypesOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth, Database: "captured_database"}), TypeNamePath: []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}}
}

func newElementUnitFixture(t *testing.T) elementUnitFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	lookup := func(id sqltype.ID) *sqltype.Descriptor {
		t.Helper()
		typ, ok := f.e.Catalog.Read(state).LookupType(id)
		if !ok {
			t.Fatal("missing fixture type", id)
		}
		return typ
	}
	putType := func(d *catalog.Type) {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	makeDomain := func(id sqltype.ID, name string, base sqltype.Usage) *catalog.Type {
		t.Helper()
		typ, err := sqltype.Domain(id, name, "default_schema."+name, base)
		if err != nil {
			t.Fatal(err)
		}
		array, err := sqltype.Array(id+1, "_"+name, typ)
		if err != nil {
			t.Fatal(err)
		}
		domain := &catalog.Type{SQLType: typ, Array: array.ID(), ObjectNamespace: f.schema, ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true}}
		putType(domain)
		putType(&catalog.Type{SQLType: array, ObjectNamespace: f.schema, ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true}})
		return domain
	}
	scalar := makeDomain(48000, "element_scalar", lookup(sqltype.Int4).Usage())
	domain := makeDomain(48002, "element_array", lookup(1007).Usage())
	makeDomain(48004, "element_outer", domain.SQLType.Usage())
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 48100}, ObjectNamespace: f.schema, ObjectName: "element_table", ObjectRevision: 1, RelationKind: catalog.TableRelation, Owner: f.owner,
		Columns: []catalog.Column{
			{Name: "matrix", Usage: lookup(1007).Usage(), Nullable: true, NullabilityKnown: true, DeclaredDimensions: 2},
			{Name: "nominal", Usage: domain.SQLType.Usage(), Nullable: true, NullabilityKnown: true},
			{Name: "domain_element", Usage: lookup(scalar.Array).Usage(), Nullable: true, NullabilityKnown: true},
			{Name: "array_domain_element", Usage: lookup(domain.Array).Usage(), Nullable: true, NullabilityKnown: true},
		}, Attributes: []int32{1, 3, 4, 5}, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}}}
	state.SetDescriptor(table.Address(), table)
	definition := &proc.Definition{Result: lookup(2249).Usage()}
	if err := definition.SetFormals([]proc.Formal{
		{Mode: "i", Name: "a", Usage: lookup(1007).Usage()},
		{Mode: "o", Name: "b", Usage: lookup(1009).Usage()},
		{Mode: "b", Name: "c", Usage: lookup(scalar.Array).Usage()},
		{Mode: "i", Name: "d", Usage: domain.SQLType.Usage()},
		{Mode: "v", Name: "tail", Usage: lookup(1016).Usage()},
	}); err != nil {
		t.Fatal(err)
	}
	routine := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 48101}, ObjectNamespace: f.schema, ObjectName: "element_routine", ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true}, RoutineKind: catalog.FunctionRoutine, SQL: definition, Signature: "1007,48001,48002,1016", Result: 2249}
	state.SetDescriptor(routine.Address(), routine)
	result := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 48103}, ObjectNamespace: f.schema, ObjectName: "element_result", ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true}, RoutineKind: catalog.FunctionRoutine, SQL: &proc.Definition{Result: lookup(1007).Usage()}, Result: 1007}
	state.SetDescriptor(result.Address(), result)
	members := []sqltype.CompositeMember{{Attribute: 2, Name: "a", Usage: lookup(1007).Usage()}, {Attribute: 4, Name: "b", Usage: lookup(1009).Usage()}}
	compositeType, err := sqltype.Composite(48010, "element_composite", "default_schema.element_composite", 0, members)
	if err != nil {
		t.Fatal(err)
	}
	composite := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 48102}, ObjectNamespace: f.schema, ObjectName: "element_composite", ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.CompositeRelation, RowType: compositeType.ID(), Attributes: []int32{2, 4}, Columns: []catalog.Column{{Name: "a", Usage: members[0].Usage, Nullable: true, NullabilityKnown: true}, {Name: "b", Usage: members[1].Usage, Nullable: true, NullabilityKnown: true}}}
	state.SetDescriptor(composite.Address(), composite)
	putType(&catalog.Type{SQLType: compositeType, ObjectNamespace: f.schema, ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true}, OwningRelation: composite.Address()})
	return elementUnitFixture{f: f, state: state, table: table, domain: domain, routine: routine, composite: composite}
}

func elementUnitExpected(schema, name, kind string, ordinal int, data, elementSchema, elementName string) []any {
	row := make([]any, 28)
	id := strconv.Itoa(ordinal)
	for i, value := range map[int]any{0: "captured_database", 1: schema, 2: name, 3: kind, 4: id, 5: data, 20: "captured_database", 21: elementSchema, 22: elementName, 27: "a" + id} {
		row[i] = value
	}
	return row
}

func elementUnitRows(t *testing.T, in pgcatalog.Input, schema string) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoElementTypesOID)
	if !ok {
		t.Fatal("missing element_types producer")
	}
	rows, err := produce(in)
	if err != nil || rows == nil {
		t.Fatal("capture", rows, err)
	}
	var selected [][]sqltype.Value
	for _, row := range rows {
		if len(row) != 28 {
			t.Fatal("width", len(row))
		}
		for i, value := range row {
			column := in.Source.Columns[i]
			if value.Type() != column.Usage.Type || value.Type().ID() != elementUnitDomains[i] || !value.ValidResult(column.Usage) {
				t.Fatal("cell generation/usage", i, value)
			}
			base, err := value.BaseValue()
			if err != nil || base.Type() != column.Usage.Type.BaseType() || base.IsNull() != value.IsNull() {
				t.Fatal("NULL/base ownership", i, value, err)
			}
		}
		if columnsUnitCell(t, row[1]) == schema {
			selected = append(selected, row)
		}
	}
	return selected
}

func elementUnitBag(t *testing.T, rows [][]sqltype.Value) map[string]int {
	t.Helper()
	bag := make(map[string]int)
	for _, row := range rows {
		values := make([]any, len(row))
		for i, v := range row {
			values[i] = columnsUnitCell(t, v)
		}
		bag[fmt.Sprintf("%#v", values)]++
	}
	return bag
}

func elementUnitAssert(t *testing.T, rows [][]sqltype.Value, want ...[]any) {
	t.Helper()
	expected := make(map[string]int)
	for _, row := range want {
		expected[fmt.Sprintf("%#v", row)]++
	}
	if got := elementUnitBag(t, rows); !reflect.DeepEqual(got, expected) {
		t.Fatalf("element bag=%v want=%v", got, expected)
	}
}
