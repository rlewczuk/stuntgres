package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestObjectAddressAuthority(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	roles := e.roles.root.Read(authorization.PrincipalRef{})
	for address, descriptor := range view.Descriptors() {
		identity, err := identifyAddress(view, roles, nil, address)
		if err != nil || !identity.found || identity.identity == "" || identity.kind == "" {
			t.Fatal("descriptor authority", address, descriptor.Name(), identity, err)
		}
		if relation, ok := descriptor.(*relationDescriptor); ok {
			for _, sub := range relation.attributes {
				column, err := identifyAddress(view, roles, nil, ids.ObjectAddress{1259, address.ObjectID, sub})
				if err != nil || !column.found || len(column.names) != 3 {
					t.Fatal("column authority", address, sub, column, err)
				}
			}
		}
	}
	if len(builtinRoutineIdentities) != 3397 {
		t.Fatal("released routine count", len(builtinRoutineIdentities))
	}
	for _, routine := range builtinRoutineIdentities {
		identity, err := identifyAddress(view, roles, nil, ids.ObjectAddress{1255, routine.id, 0})
		if err != nil || !identity.found || len(identity.names) != 2 || identity.names[1] != routine.name {
			t.Fatal("lookup-only routine authority", routine, identity, err)
		}
	}
	for i := range sqltype.CastMetadataCount() {
		fact, _ := sqltype.CastMetadataAt(i)
		identity, err := identifyAddress(view, roles, nil, ids.ObjectAddress{2605, ids.OID(fact.OID), 0})
		if err != nil || !identity.found || len(identity.names) != 1 || len(identity.args) != 1 {
			t.Fatal("cast owner", fact, identity, err)
		}
	}
	for i := range sqltype.IndexClassMetadataCount() {
		fact, _ := sqltype.IndexClassMetadataAt(i)
		for _, a := range []ids.ObjectAddress{{2616, ids.OID(fact.OID), 0}, {2753, ids.OID(fact.Family), 0}} {
			identity, err := identifyAddress(view, roles, nil, a)
			if err != nil || !identity.found || len(identity.names) != 3 {
				t.Fatal("index owner", fact, a, identity, err)
			}
		}
	}
}

func objectAddressUnitRelation(t *testing.T) (*Engine, CatalogRead, *relationDescriptor) {
	t.Helper()
	e := New()
	view := e.CatalogRead()
	view.state = cloneCatalog(view.state)
	integer, _ := view.LookupType(sqltype.Int4)
	d := &relationDescriptor{address: ids.ObjectAddress{1259, 48001, 0}, namespace: ids.ObjectAddress{2615, 2200, 0}, name: "address_unit", revision: 1, relationKind: tableRelation, columns: []ColumnDescriptor{{Name: "a", Usage: integer.Usage()}, {Name: "c", Usage: integer.Usage()}}, attributes: []int32{1, 3}, attributeHighWater: 3, droppedAttributes: map[int32]attributeMetadata{2: {name: "........pg.dropped.2........", number: 2, dropped: true}}}
	view.state.descriptors[d.address] = d
	view.state.names[descriptorNameKey(d)] = d.address
	return e, view, d
}
func TestObjectAddressIdentity(t *testing.T) {
	e, view, d := objectAddressUnitRelation(t)
	roles := e.roles.root.Read(authorization.PrincipalRef{})
	for _, tc := range []struct {
		sub  int32
		name string
	}{{1, "a"}, {3, "c"}, {2, "........pg.dropped.2........"}, {65538, "........pg.dropped.2........"}, {2147483647, "ctid"}, {-6, "tableoid"}} {
		identity, err := identifyAddress(view, roles, []ids.ObjectAddress{d.namespace}, ids.ObjectAddress{1259, d.address.ObjectID, tc.sub})
		if err != nil || !identity.found || identity.names[2] != tc.name || identity.name != "address_unit" {
			t.Fatal(tc, identity, err)
		}
	}
	d.notNullConstraints = []notNullConstraint{{OID: 48010, Name: "nn"}}
	d.checkConstraints = []checkConstraint{{OID: 48011, Name: "ck"}}
	d.uniqueConstraints = []uniqueConstraint{{OID: 48012, Name: "uk"}, {OID: 48013, Name: "pk"}}
	d.foreignKeys = []foreignKeyConstraint{{OID: 48014, Name: "fk"}}
	d.exclusionConstraints = []exclusionConstraint{{OID: 48015, Name: "ex"}}
	d.defaults = map[int32]columnDefault{1: {OID: 48016}}
	d.generations = map[int32]columnGeneration{3: {OID: 48017}}
	d.rules = []ruleDefinition{{OID: 48018, Name: "rule_unit"}}
	d.policies = []policyDefinition{{OID: 48019, Name: "policy_unit"}}
	d.view = &viewDefinition{rule: 48022}
	for _, tc := range []struct {
		class, id ids.OID
		name      string
	}{{2606, 48010, "nn"}, {2606, 48011, "ck"}, {2606, 48012, "uk"}, {2606, 48013, "pk"}, {2606, 48014, "fk"}, {2606, 48015, "ex"}, {2604, 48016, "a"}, {2604, 48017, "c"}, {2618, 48018, "rule_unit"}, {3256, 48019, "policy_unit"}, {2618, 48022, "_RETURN"}} {
		a := ids.ObjectAddress{tc.class, tc.id, 0}
		identity, err := identifyAddress(view, roles, nil, a)
		if err != nil || !identity.found || identity.names[2] != tc.name || ownedAddressByName(d, tc.class, tc.name) != tc.id {
			t.Fatal("owned definition roundtrip", tc, identity, err)
		}
	}
	// Domain CHECK has an actual owned OID even without a pg_constraint SQL row.
	integer, _ := view.LookupType(sqltype.Int4)
	domain, err := sqltype.Domain(48020, "address_domain", "public.address_domain", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	typ := &typeDescriptor{typ: domain, namespace: d.namespace, domainChecks: []domainCheckConstraint{{OID: 48021, Name: "domain_ck"}}}
	view.state.descriptors[typ.Address()] = typ
	identity, err := identifyAddress(view, roles, nil, ids.ObjectAddress{2606, 48021, 0})
	if err != nil || !identity.found || identity.kind != "domain constraint" || !reflect.DeepEqual(identity.args, []string{"domain_ck"}) {
		t.Fatal(identity, err)
	}
}
func TestObjectAddressLookup(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		class ids.OID
	}{{"table column", 1259}, {"function", 1255}, {"aggregate", 1255}, {"procedure", 1255}, {"type", 1247}, {"domain constraint", 2606}, {"operator family", 2753}, {"default value", 2604}} {
		class, err := objectInputClass(tc.kind)
		if err != nil || class != tc.class {
			t.Fatal(tc, class, err)
		}
	}
	for _, kind := range []string{"routine", "role membership", "domain", "TABLE", "stone", "view column", "index column"} {
		if _, err := objectInputClass(kind); err == nil {
			t.Fatal("invalid inverse kind accepted", kind)
		}
	}
	e := New()
	view := e.CatalogRead()
	array, _ := view.LookupType(1009)
	text, _ := view.LookupType(sqltype.Text)
	v, err := sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Lower: -3, Length: 2}}, []sqltype.Value{sqltype.String(text, "public"), sqltype.String(text, "t")})
	if err != nil {
		t.Fatal(err)
	}
	names, err := objectInputArray(v)
	if err != nil || !reflect.DeepEqual(names, []string{"public", "t"}) {
		t.Fatal("structured flat names", names, err)
	}
	v, err = sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{sqltype.Null(text)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := objectInputArray(v); err == nil {
		t.Fatal("null name accepted")
	}
}
func TestObjectAddressStoredRecipes(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, name := range []string{"pg_identify_object", "pg_identify_object_as_address", "pg_get_object_address", "pg_describe_object"} {
		q := domainUnitBindSelect(t, e, op, "SELECT "+name+"(NULL,NULL,NULL)")
		n, ok := q.Root.(*projectNode).expressions[0].(objectAddressExpression)
		if !ok || n.validateStored() != nil || n.Properties().Volatility != Stable || generationImmutable(n) {
			t.Fatal("typed stable carrier", name, n)
		}
		children := 0
		boundExpressionChildren(n, func(BoundExpr) { children++ })
		if children != 3 {
			t.Fatal("carrier children", children)
		}
		bad := n
		bad.slots[1] = bad.slots[0]
		if bad.validateStored() == nil {
			t.Fatal("duplicate slot accepted")
		}
		bad = n
		bad.Property.Volatility = Immutable
		if bad.validateStored() == nil {
			t.Fatal("immutable inquiry accepted")
		}
		value, err := n.Eval(&EvalContext{Context: context.Background()})
		if err != nil || !value.IsNull() || value.Type() != n.ResultUsage().Type {
			t.Fatal("strict typed NULL", value, err)
		}
		rendered, err := n.sql("pg_catalog."+name, func(BoundExpr) (string, error) { return "NULL", nil })
		if err != nil || rendered != "pg_catalog."+name+"(NULL, NULL, NULL)" {
			t.Fatal("stored renderer", rendered, err)
		}
		mapped, err := remapBoundExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || mapped.(objectAddressExpression).validateStored() != nil {
			t.Fatal("stored remap", mapped, err)
		}
	}
}
func TestObjectAddressSnapshot(t *testing.T) {
	e, old, d := objectAddressUnitRelation(t)
	roles := e.roles.root.Read(authorization.PrincipalRef{})
	fresh := old
	fresh.state = cloneCatalog(old.state)
	renamed := *d
	renamed.name = "address_renamed"
	fresh.state.descriptors[d.address] = &renamed
	before, err := identifyAddress(old, roles, nil, d.address)
	if err != nil {
		t.Fatal(err)
	}
	after, err := identifyAddress(fresh, roles, nil, d.address)
	if err != nil {
		t.Fatal(err)
	}
	again, err := identifyAddress(old, roles, nil, d.address)
	if err != nil || before.identity != "public.address_unit" || after.identity != "public.address_renamed" || !reflect.DeepEqual(before, again) {
		t.Fatal("immutable snapshot identity", before, after, again, err)
	}
}
func TestObjectAddressMetadata(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	array, _ := view.LookupType(1009)
	for _, names := range [][]string{nil, {}, {"Odd.Schema", "a.b\"c"}} {
		value, err := objectAddressTextArray(array, names)
		if err != nil || value.IsNull() || value.ArrayLen() != len(names) {
			t.Fatal("typed array", value, err)
		}
		for i, name := range names {
			s, err := value.ArrayElement(i).Text()
			if err != nil || s != name {
				t.Fatal("raw component", s, name, err)
			}
		}
	}
}
