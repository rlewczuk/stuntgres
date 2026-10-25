package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
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
	class, ok, defaultErr := view.DefaultIndexClass(403, u)
	if defaultErr != nil {
		t.Fatal(defaultErr)
	}
	if !ok && (u.ContainsRegclass() || u.ContainsRegtype()) {
		oid, _ := view.LookupType(sqltype.Oid)
		class, ok, defaultErr = view.DefaultIndexClass(403, oid)
		if defaultErr != nil {
			t.Fatal(defaultErr)
		}
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
