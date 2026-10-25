package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestObjectAddressAuthority(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	roles := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{})
	for address, descriptor := range view.Descriptors() {
		identity, err := regobj.IdentifyAddress(view, roles, nil, address)
		if err != nil || !identity.Found || identity.Identity == "" || identity.Kind == "" {
			t.Fatal("descriptor authority", address, descriptor.Name(), identity, err)
		}
		if relation, ok := descriptor.(*catalog.Relation); ok {
			for _, sub := range relation.Attributes {
				column, err := regobj.IdentifyAddress(view, roles, nil, ids.ObjectAddress{1259, address.ObjectID, sub})
				if err != nil || !column.Found || len(column.Names) != 3 {
					t.Fatal("column authority", address, sub, column, err)
				}
			}
		}
	}
	released := catalog.BuiltinRoutineIdentities()
	if len(released) != 3397 {
		t.Fatal("released routine count", len(released))
	}
	for _, routine := range released {
		identity, err := regobj.IdentifyAddress(view, roles, nil, ids.ObjectAddress{1255, routine.ID, 0})
		if err != nil || !identity.Found || len(identity.Names) != 2 || identity.Names[1] != routine.Name {
			t.Fatal("lookup-only routine authority", routine, identity, err)
		}
	}
	for i := range sqltype.CastMetadataCount() {
		fact, _ := sqltype.CastMetadataAt(i)
		identity, err := regobj.IdentifyAddress(view, roles, nil, ids.ObjectAddress{2605, ids.OID(fact.OID), 0})
		if err != nil || !identity.Found || len(identity.Names) != 1 || len(identity.Args) != 1 {
			t.Fatal("cast owner", fact, identity, err)
		}
	}
	for i := range sqltype.IndexClassMetadataCount() {
		fact, _ := sqltype.IndexClassMetadataAt(i)
		for _, a := range []ids.ObjectAddress{{2616, ids.OID(fact.OID), 0}, {2753, ids.OID(fact.Family), 0}} {
			identity, err := regobj.IdentifyAddress(view, roles, nil, a)
			if err != nil || !identity.Found || len(identity.Names) != 3 {
				t.Fatal("index owner", fact, a, identity, err)
			}
		}
	}
}

func objectAddressUnitRelation(t *testing.T) (*engine.Engine, catalog.Read, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	view := e.CatalogRead()
	view = view.WithState(catalog.CloneState(view.State()))
	integer, _ := view.LookupType(sqltype.Int4)
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{1259, 48001, 0}, ObjectNamespace: ids.ObjectAddress{2615, 2200, 0}, ObjectName: "address_unit", ObjectRevision: 1, RelationKind: catalog.TableRelation, Columns: []catalog.Column{{Name: "a", Usage: integer.Usage()}, {Name: "c", Usage: integer.Usage()}}, Attributes: []int32{1, 3}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3, Dropped: map[int32]catalog.AttributeMetadata{2: {Name: "........pg.dropped.2........", Number: 2, Dropped: true}}}}
	view.State().SetDescriptor(d.Address(), d)
	view.State().BindName(catalog.DescriptorNameKey(d), d.Address())
	return e, view, d
}
func TestObjectAddressIdentity(t *testing.T) {
	e, view, d := objectAddressUnitRelation(t)
	roles := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{})
	for _, tc := range []struct {
		sub  int32
		name string
	}{{1, "a"}, {3, "c"}, {2, "........pg.dropped.2........"}, {65538, "........pg.dropped.2........"}, {2147483647, "ctid"}, {-6, "tableoid"}} {
		identity, err := regobj.IdentifyAddress(view, roles, []ids.ObjectAddress{d.Namespace()}, ids.ObjectAddress{1259, d.Address().ObjectID, tc.sub})
		if err != nil || !identity.Found || identity.Names[2] != tc.name || identity.Name != "address_unit" {
			t.Fatal(tc, identity, err)
		}
	}
	d.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 48010, Name: "nn"}}
	d.Constraints.Check = []catalog.CheckConstraint{{OID: 48011, Name: "ck"}}
	d.Constraints.Unique = []catalog.UniqueConstraint{{OID: 48012, Name: "uk"}, {OID: 48013, Name: "pk"}}
	d.Constraints.ForeignKeys = []catalog.ForeignKeyConstraint{{OID: 48014, Name: "fk"}}
	d.Constraints.Exclusion = []catalog.ExclusionConstraint{{OID: 48015, Name: "ex"}}
	d.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {OID: 48016}}
	d.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{3: {OID: 48017}}
	d.Rules = stored.RulesPayload([]stored.Rule{{OID: 48018, Name: "rule_unit"}})
	d.Security.Policies = []catalog.PolicyDefinition{{OID: 48019, Name: "policy_unit"}}
	d.View = stored.NewView(stored.ViewParts{Rule: 48022})
	for _, tc := range []struct {
		class, id ids.OID
		name      string
	}{{2606, 48010, "nn"}, {2606, 48011, "ck"}, {2606, 48012, "uk"}, {2606, 48013, "pk"}, {2606, 48014, "fk"}, {2606, 48015, "ex"}, {2604, 48016, "a"}, {2604, 48017, "c"}, {2618, 48018, "rule_unit"}, {3256, 48019, "policy_unit"}, {2618, 48022, "_RETURN"}} {
		a := ids.ObjectAddress{tc.class, tc.id, 0}
		identity, err := regobj.IdentifyAddress(view, roles, nil, a)
		if err != nil || !identity.Found || identity.Names[2] != tc.name || regobj.OwnedAddressByName(d, tc.class, tc.name) != tc.id {
			t.Fatal("owned definition roundtrip", tc, identity, err)
		}
	}
	// Domain CHECK has an actual owned OID even without a pg_constraint SQL row.
	integer, _ := view.LookupType(sqltype.Int4)
	domain, err := sqltype.Domain(48020, "address_domain", "public.address_domain", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	typ := &catalog.Type{SQLType: domain, ObjectNamespace: d.Namespace(), DomainChecks: []catalog.DomainCheckConstraint{{OID: 48021, Name: "domain_ck"}}}
	view.State().SetDescriptor(typ.Address(), typ)
	identity, err := regobj.IdentifyAddress(view, roles, nil, ids.ObjectAddress{2606, 48021, 0})
	if err != nil || !identity.Found || identity.Kind != "domain constraint" || !reflect.DeepEqual(identity.Args, []string{"domain_ck"}) {
		t.Fatal(identity, err)
	}
}
func TestObjectAddressLookup(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		class ids.OID
	}{{"table column", 1259}, {"function", 1255}, {"aggregate", 1255}, {"procedure", 1255}, {"type", 1247}, {"domain constraint", 2606}, {"operator family", 2753}, {"default value", 2604}} {
		class, err := regobj.ObjectInputClass(tc.kind)
		if err != nil || class != tc.class {
			t.Fatal(tc, class, err)
		}
	}
	for _, kind := range []string{"routine", "role membership", "domain", "TABLE", "stone", "view column", "index column"} {
		if _, err := regobj.ObjectInputClass(kind); err == nil {
			t.Fatal("invalid inverse kind accepted", kind)
		}
	}
	e := engine.New()
	view := e.CatalogRead()
	array, _ := view.LookupType(1009)
	text, _ := view.LookupType(sqltype.Text)
	v, err := sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Lower: -3, Length: 2}}, []sqltype.Value{sqltype.String(text, "public"), sqltype.String(text, "t")})
	if err != nil {
		t.Fatal(err)
	}
	names, err := regobj.ObjectInputArray(v)
	if err != nil || !reflect.DeepEqual(names, []string{"public", "t"}) {
		t.Fatal("structured flat names", names, err)
	}
	v, err = sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{sqltype.Null(text)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := regobj.ObjectInputArray(v); err == nil {
		t.Fatal("null name accepted")
	}
}
func TestObjectAddressStoredRecipes(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, name := range []string{"pg_identify_object", "pg_identify_object_as_address", "pg_get_object_address", "pg_describe_object"} {
		q := domainUnitBindSelect(t, e, op, "SELECT "+name+"(NULL,NULL,NULL)")
		n, ok := q.Root.(*plan.ProjectNode).Expressions[0].(functions.ObjectAddress)
		if !ok || n.Validate() != nil || n.Properties().Volatility != expr.Stable || plan.GenerationImmutable(n) {
			t.Fatal("typed stable carrier", name, n)
		}
		children := 0
		plan.ExpressionChildren(n, func(expr.Expr) { children++ })
		if children != 3 {
			t.Fatal("carrier children", children)
		}
		bad := n
		bad.Slots[1] = bad.Slots[0]
		if bad.Validate() == nil {
			t.Fatal("duplicate slot accepted")
		}
		bad = n
		bad.Property.Volatility = expr.Immutable
		if bad.Validate() == nil {
			t.Fatal("immutable inquiry accepted")
		}
		value, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		if err != nil || !value.IsNull() || value.Type() != n.ResultUsage().Type {
			t.Fatal("strict typed NULL", value, err)
		}
		mapped, err := stored.RemapExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || mapped.(functions.ObjectAddress).Validate() != nil {
			t.Fatal("stored remap", mapped, err)
		}
	}
}
func TestObjectAddressSnapshot(t *testing.T) {
	e, old, d := objectAddressUnitRelation(t)
	roles := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{})
	fresh := old
	fresh = fresh.WithState(catalog.CloneState(old.State()))
	renamed := *d
	renamed.ObjectName = "address_renamed"
	fresh.State().SetDescriptor(d.Address(), &renamed)
	before, err := regobj.IdentifyAddress(old, roles, nil, d.Address())
	if err != nil {
		t.Fatal(err)
	}
	after, err := regobj.IdentifyAddress(fresh, roles, nil, d.Address())
	if err != nil {
		t.Fatal(err)
	}
	again, err := regobj.IdentifyAddress(old, roles, nil, d.Address())
	if err != nil || before.Identity != "public.address_unit" || after.Identity != "public.address_renamed" || !reflect.DeepEqual(before, again) {
		t.Fatal("immutable snapshot identity", before, after, again, err)
	}
}
func TestObjectAddressMetadata(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	array, _ := view.LookupType(1009)
	for _, names := range [][]string{nil, {}, {"Odd.Schema", "a.b\"c"}} {
		value, err := regobj.ObjectAddressTextArray(array, names)
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
