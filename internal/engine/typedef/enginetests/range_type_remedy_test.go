package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestRangeTypeAliasMappingOrder(t *testing.T) {
	ctx := context.Background()
	for _, subtype := range []sqltype.ID{sqltype.Regclass, sqltype.Regtype} {
		_, r, m := rangeTypeUnitFamily(t, subtype, false)
		definition, _ := r.RangeDefinition()
		ten, twenty := sqltype.String(definition.Subtype.Type, "10"), sqltype.String(definition.Subtype.Type, "20")
		finite, err := sqltype.RangeFromValues(ctx, r, ten, twenty, "[]")
		if err != nil {
			t.Fatal(err)
		}
		lower, err := sqltype.RangeFromValues(ctx, r, ten, ten, "[]")
		if err != nil {
			t.Fatal(err)
		}
		upper, err := sqltype.RangeFromValues(ctx, r, twenty, twenty, "[]")
		if err != nil {
			t.Fatal(err)
		}
		members, err := sqltype.MultirangeFromValues(ctx, m, []sqltype.Value{lower, upper})
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []sqltype.Value{finite, members} {
			for _, ordered := range []bool{false, true} {
				leaf := func(v sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
					text, err := v.Text()
					if err != nil {
						return sqltype.Value{}, err
					}
					if text == "10" {
						text = "30"
					} else if text == "20" && ordered {
						text = "40"
					}
					return sqltype.String(v.Type(), text), nil
				}
				mapped, err := source.MapRange(source.Type(), sqltype.RangeMapIdentity, leaf)
				if err != nil {
					t.Fatal(err)
				}
				err = mapped.ValidateMappedRanges(ctx)
				if ordered && err != nil || !ordered && err == nil {
					t.Fatal("actual destination provider must detect inverted bounds/member order", subtype, ordered, err)
				}
				if _, err := source.MapRange(source.Type(), sqltype.RangeMapGeneration, leaf); err == nil {
					t.Fatal("generation mapping silently changed comparison payload")
				}
			}
			identity := func(v sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) { return v, nil }
			if allocations := testing.AllocsPerRun(10, func() {
				mapped, err := source.MapRange(source.Type(), sqltype.RangeMapGeneration, identity)
				if err != nil || !sqltype.SameDatum(source, mapped) {
					panic("unchanged range mapping")
				}
			}); allocations != 0 {
				t.Fatal("unchanged immutable range mapping allocated", allocations)
			}
		}
	}
}

func TestRangeTypeCompanionACLProvider(t *testing.T) {
	view, r, m := rangeTypeUnitFamily(t, sqltype.Int4, false)
	original := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(m.ID())})
	changed := *original
	changed.ACL = authorization.ACLValue{Explicit: true}
	changed.ACLSyntheticDefault = false
	state := catalog.CloneState(view.State())
	state.SetDescriptor(changed.Address(), &changed)
	if err := (stored.TypeProvider{}).Validate(catalog.NewRead(view.Owner(), state), &changed); err == nil {
		t.Fatal("independently mutable companion ACL accepted")
	}
	owner := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.ID())})
	if got, err := catalog.TypeUsageAuthority(view, original); err != nil || got != owner {
		t.Fatal("companion lost actual owning range authority", err)
	}
	stale := *original
	if _, err := catalog.TypeUsageAuthority(view, &stale); err == nil {
		t.Fatal("copied descriptor supplied authorization authority")
	}
}

func TestRangeTypeGeneratedCastProvider(t *testing.T) {
	view, procedure, cast := rangeTypeUnitGeneratedCast(t)
	m := procedure.RangeConstructor.Type
	if err := (catalog.CastProvider{}).Validate(view, cast); err != nil {
		t.Fatal("actual type-owned cast", err)
	}
	dependencies := catalog.CastDependencies(cast)
	if len(dependencies) != 3 || dependencies[0] != cast.GeneratedRange || dependencies[1].ObjectID != ids.OID(m.ID()) || dependencies[2] != procedure.Address() || catalog.CastDependencyKind(cast) != 'i' {
		t.Fatal("generated cast must have exactly three INTERNAL edges", dependencies)
	}
	ordinary := *cast
	ordinary.GeneratedRange = ids.ObjectAddress{}
	if catalog.CastDependencyKind(&ordinary) != 'n' {
		t.Fatal("ordinary casts acquired internal ownership")
	}
	for _, mutate := range []func(*catalog.Cast){func(c *catalog.Cast) { c.GeneratedRange.ObjectID++ }, func(c *catalog.Cast) { c.Context = "i" }, func(c *catalog.Cast) { c.InputCast = procedure.Address() }} {
		changed := *cast
		mutate(&changed)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(changed.Address(), &changed)
		if err := (catalog.CastProvider{}).Validate(catalog.NewRead(view.Owner(), state), &changed); err == nil {
			t.Fatal("forged generated cast provenance accepted", changed)
		}
	}
}

func TestRangeTypeOwnershipCanonicalRoot(t *testing.T) {
	view, r, m := rangeTypeUnitFamily(t, sqltype.Int4, false)
	for _, typ := range []*sqltype.Descriptor{r, m} {
		descriptor := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
		if got := access.OwnershipRoot(view, descriptor, nil); got != (typ == r) {
			t.Fatal("only the owning range may be a bulk ownership root", typ.Name(), got)
		}
		array, ok := view.LookupArray(typ)
		if !ok {
			t.Fatal("missing actual family array")
		}
		if access.OwnershipRoot(view, view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(array.ID())}), nil) {
			t.Fatal("family array acquired independent bulk ownership")
		}
	}
}

func rangeTypeUnitGeneratedCast(t *testing.T) (catalog.Read, *catalog.Procedure, *catalog.Cast) {
	t.Helper()
	view, r, m := rangeTypeUnitFamily(t, sqltype.Int4, false)
	owner := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.ID())}).Owner
	provider := &catalog.RangeConstructor{Type: m, Form: catalog.MultirangeConstructorOne}
	signature, err := provider.Signature(view)
	if err != nil {
		t.Fatal(err)
	}
	procedure := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 920004}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 2, ObjectName: m.Name(), Signature: signature, RoutineKind: catalog.FunctionRoutine, Result: ids.OID(m.ID()), Owner: owner, Volatility: expr.Immutable, Strict: true, RangeConstructor: provider, ACL: authorization.DefaultBaseline(owner, authorization.DefaultRoutines, false)}
	view.State().SetDescriptor(procedure.Address(), procedure)
	view.State().BindName(catalog.DescriptorNameKey(procedure), procedure.Address())
	procedures := catalog.ProcedureIndexEdit{Index: view.State().ProcedureNames()}
	procedures.Replace(nil, procedure)
	view.State().SetProcedureNames(procedures.Index)
	cast := &catalog.Cast{OID: 920010, ObjectRevision: 2, Source: r.ID(), Target: m.ID(), Function: procedure.Address(), Context: "e", Method: "f", GeneratedRange: ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.ID())}}
	view.State().SetDescriptor(cast.Address(), cast)
	view.State().BindName(catalog.DescriptorNameKey(cast), cast.Address())
	return view, procedure, cast
}
