package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Real catalog classes, members and executable declarations supply every capture.
// Changing the candidate member is not a fabricated WithSupport callback.
func rangeTypeUnitFamily(t *testing.T, subtype sqltype.ID, subtract bool) (catalog.Read, *sqltype.Descriptor, *sqltype.Descriptor) {
	t.Helper()
	e := New()
	state := catalog.CloneState(e.CatalogRead().State())
	view := catalog.NewRead(e.CatalogRead().Owner(), state)
	u, _ := view.LookupType(subtype)
	class, ok := view.DefaultIndexClass(403, u)
	if !ok && (u.ContainsRegclass() || u.ContainsRegtype()) {
		oid, _ := view.LookupType(sqltype.Oid)
		class, ok = view.DefaultIndexClass(403, oid)
	}
	if !ok {
		t.Fatal("missing actual subtype class")
	}
	if subtract {
		member, ok := view.FamilySupport(ids.OID(class.FamilyOID()), 23, 23, 1)
		if !ok {
			t.Fatal("missing actual support member")
		}
		copy := *member
		copy.Function = 181
		copy.ObjectRevision++
		state.SetDescriptor(copy.Address(), &copy)
	}
	class, err := routine.BindClassSupport(view, class)
	if err != nil {
		t.Fatal(err)
	}
	ordering, err := routine.BindIndexOrdering(view, u.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	boolean, _ := view.LookupType(sqltype.Bool)
	r, m, err := sqltype.NewRangeFamily(900001, "unit_span", "public.unit_span", 900002, "unit_spans", "public.unit_spans", sqltype.RangeDefinition{Subtype: u.Usage(), Class: class, Ordering: ordering}, boolean)
	if err != nil {
		t.Fatal(err)
	}
	owner := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(subtype)}).Owner
	for i, typ := range []*sqltype.Descriptor{r, m} {
		array, err := sqltype.Array(sqltype.ID(900003+i), "_"+typ.Name(), typ)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range []*catalog.Type{{SQLType: typ, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 2, Owner: owner, ACL: authorization.DefaultBaseline(owner, authorization.DefaultTypes, false), ACLSyntheticDefault: true, Array: array.ID()}, {SQLType: array, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 2, Owner: owner, ACL: authorization.DefaultBaseline(owner, authorization.DefaultTypes, false), ACLSyntheticDefault: true}} {
			state.SetDescriptor(d.Address(), d)
			state.BindName(catalog.DescriptorNameKey(d), d.Address())
		}
	}
	return view, r, m
}

func rangeTypeUnitInput(t *testing.T, d *sqltype.Descriptor, text string) sqltype.Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(text, err)
	}
	return v
}

func rangeTypeUnitOutput(t *testing.T, v sqltype.Value, want string) {
	t.Helper()
	got, err := v.Type().Output(context.Background(), v)
	if err != nil || got != want {
		t.Fatalf("output %q want %q: %v", got, want, err)
	}
}

func TestRangeTypeProviders(t *testing.T) {
	for _, subtract := range []bool{false, true} {
		view, r, m := rangeTypeUnitFamily(t, sqltype.Int4, subtract)
		d, _ := r.RangeDefinition()
		a, b := rangeTypeUnitInput(t, d.Subtype.Type, "1"), rangeTypeUnitInput(t, d.Subtype.Type, "3")
		got, err := d.Ordering.Compare(context.Background(), a, b, sqltype.InputEnvironment{})
		want := -1
		if subtract {
			want = -2
		}
		if err != nil || got != want {
			t.Fatal("selected actual comparator", got, want, err)
		}
		member, ok := view.FamilySupport(ids.OID(d.Class.FamilyOID()), 23, 23, 1)
		if !ok || d.Ordering.Support().Member != uint32(member.Address().ObjectID) || d.Ordering.Support().Routine != uint32(member.Function) {
			t.Fatal("capture did not select actual member")
		}
		for _, typ := range []*sqltype.Descriptor{r, m} {
			if err := (stored.TypeProvider{}).Validate(view, view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})); err != nil {
				t.Fatal(err)
			}
		}
		rangeTypeUnitCarriers(t, r, m)
		foreign, _ := New().CatalogRead().LookupType(sqltype.Int4)
		forged := d
		forged.Subtype.Type = foreign
		boolean, _ := view.LookupType(sqltype.Bool)
		if _, _, err := sqltype.NewRangeFamily(930001, "foreign_span", "public.foreign_span", 930002, "foreign_spans", "public.foreign_spans", forged, boolean); err == nil {
			t.Fatal("foreign subtype ordering capture accepted")
		}
		stale := catalog.CloneState(view.State())
		copy := *member
		copy.ObjectRevision++
		stale.SetDescriptor(copy.Address(), &copy)
		if err := (stored.TypeProvider{}).Validate(catalog.NewRead(nil, stale), view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.ID())})); err == nil {
			t.Fatal("stale genuine support accepted")
		}
	}
}

func TestRangeTypeBounds(t *testing.T) {
	_, r, _ := rangeTypeUnitFamily(t, sqltype.Int4, false)
	for _, tc := range [][2]string{{"[1,3]", "[1,3]"}, {"[1,4)", "[1,4)"}, {"[4,4]", "[4,4]"}, {"[4,4)", "empty"}, {"[,]", "(,)"}, {"[2147483647,2147483647]", "[2147483647,2147483647]"}} {
		rangeTypeUnitOutput(t, rangeTypeUnitInput(t, r, tc[0]), tc[1])
	}
	a := rangeTypeUnitInput(t, r, "[1,3]")
	b := rangeTypeUnitInput(t, r, "[1,3]")
	if !sqltype.SameDatum(a, b) || sqltype.SameDatum(a, rangeTypeUnitInput(t, r, "[1,4)")) {
		t.Fatal("bound semantic identity lost")
	}
	binary, err := r.AppendBinaryWithEnvironment(context.Background(), nil, a, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := r.BinaryInputWithEnvironment(context.Background(), binary, -1, sqltype.InputEnvironment{})
	if err != nil || !sqltype.SameDatum(a, decoded) {
		t.Fatal("generic binary subtype roundtrip", err)
	}
}

func TestRangeTypeMultirange(t *testing.T) {
	_, _, m := rangeTypeUnitFamily(t, sqltype.Int4, false)
	for _, tc := range [][2]string{{"{[1,3),[2,4),empty,[8,9)}", "{[1,4),[8,9)}"}, {"{[1,2],[3,4]}", "{[1,2],[3,4]}"}, {"{[1,2),[2,4]}", "{[1,4]}"}, {"{empty}", "{}"}, {"{[2147483647,2147483647]}", "{[2147483647,2147483647]}"}} {
		rangeTypeUnitOutput(t, rangeTypeUnitInput(t, m, tc[0]), tc[1])
	}
	value := rangeTypeUnitInput(t, m, "{[1,3],[8,9]}")
	copy, err := value.MapRange(m, sqltype.RangeMapGeneration, func(v sqltype.Value, target *sqltype.Descriptor) (sqltype.Value, error) { return v.Retag(target) })
	if err != nil || !sqltype.SameDatum(value, copy) {
		t.Fatal("owned member mapping", err)
	}
	rangeTypeUnitOutput(t, value, "{[1,3],[8,9]}")
}

func TestRangeTypeAtomicity(t *testing.T) {
	_, r, m := rangeTypeUnitFamily(t, sqltype.Int4, true)
	before := rangeTypeUnitInput(t, m, "{[1,3],[8,9]}")
	for _, tc := range []struct {
		d    *sqltype.Descriptor
		text string
	}{{r, "[-2147483648,2147483647]"}, {m, "{[-2147483648,-2147483647],[2147483646,2147483647]}"}} {
		_, err := tc.d.Input(context.Background(), tc.text, -1)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" || diagnostic.Message != "integer out of range" {
			t.Fatal("comparator error lost", err)
		}
		rangeTypeUnitOutput(t, before, "{[1,3],[8,9]}")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Input(ctx, "[1,3]", -1); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}

func TestRangeTypeIdentityRemap(t *testing.T) {
	view, r, m := rangeTypeUnitFamily(t, sqltype.Int4, false)
	next := catalog.CloneState(view.State())
	mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
	for i, typ := range []*sqltype.Descriptor{r, m} {
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}
		mapping[address] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(910001 + i)}
	}
	changed, err := typedef.ReownDomainTypes(next, mapping, true, rangeTypeRebinder(next, mapping))
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	for _, typ := range []*sqltype.Descriptor{r, m} {
		input := "[1,3]"
		if typ == m {
			input = "{[1,3],[8,9]}"
		}
		old := rangeTypeUnitInput(t, typ, input)
		target := next.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}).SQLType
		if target.ID() == typ.ID() {
			t.Fatal("destination identity not displaced")
		}
		mapped, err := typedef.ReownDomainDatum(old, target, nil, next.ScalarImports(), nil)
		if err != nil {
			t.Fatal(err)
		}
		rangeTypeUnitOutput(t, mapped, input)
		rangeTypeUnitOutput(t, old, input)
		if _, err := old.Retag(target); err == nil {
			t.Fatal("ordinary Retag reidentified type")
		}
	}
}

func TestRangeTypeDependencyClosure(t *testing.T) {
	view, r, m := rangeTypeUnitFamily(t, sqltype.Int4, false)
	rangeOwner := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.ID())})
	multiOwner := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(m.ID())})
	d, _ := r.RangeDefinition()
	want := []ids.ObjectAddress{{ClassID: ids.TypeClassID, ObjectID: 23}, {ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(d.Class.OID())}}
	if got := catalog.RangeDependencies(rangeOwner); !reflect.DeepEqual(got, want) {
		t.Fatal("range normal dependencies", got, want)
	}
	if got := catalog.RangeDependencies(multiOwner); !reflect.DeepEqual(got, []ids.ObjectAddress{rangeOwner.Address()}) {
		t.Fatal("companion internal owner", got)
	}
	without := catalog.CloneState(view.State())
	without.RemoveDescriptor(rangeOwner.Address())
	if err := (stored.TypeProvider{}).Validate(catalog.NewRead(nil, without), multiOwner); err == nil {
		t.Fatal("orphan companion accepted")
	}
}
