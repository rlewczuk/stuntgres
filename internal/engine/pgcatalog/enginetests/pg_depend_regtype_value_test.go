package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// Represented type identities are distinct from both the literal datatype and
// relation identities with the same numeric OID. Frozen capture and pg_depend
// share the value walk, but intentionally retain different pinned-ref rules.
func TestPGDependRegtypeValueReferences(t *testing.T) {
	e, view, integer := pgDependUnitCatalog(t)
	regtype, ok := view.LookupType(sqltype.Regtype)
	if !ok {
		t.Fatal("missing regtype")
	}
	domain, err := sqltype.Domain(48101, "provider", "public.provider", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	provider := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(domain.ID())}
	view.State().SetDescriptor(provider, &catalog.Type{SQLType: domain})
	row, err := sqltype.Composite(48102, "row_provider", "public.row_provider", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	rowAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.ID())}
	relationAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(row.ID())}
	view.State().SetDescriptor(rowAddress, &catalog.Type{SQLType: row, OwningRelation: relationAddress})
	view.State().SetDescriptor(relationAddress, &catalog.Relation{ObjectAddress: relationAddress, RowType: row.ID()})
	value := func(id ids.OID) sqltype.Value {
		t.Helper()
		v, err := sqltype.RegtypeValue(regtype, uint32(id))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	pinned := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(integer.ID())}
	cases := []struct {
		name    string
		value   sqltype.Value
		want    []ids.ObjectAddress
		capture []ids.ObjectAddress
	}{
		{"domain", value(provider.ObjectID), []ids.ObjectAddress{provider}, []ids.ObjectAddress{provider}},
		{"row_type", value(rowAddress.ObjectID), []ids.ObjectAddress{rowAddress}, []ids.ObjectAddress{rowAddress}},
		{"pinned", value(pinned.ObjectID), nil, []ids.ObjectAddress{pinned}},
		{"null", sqltype.Null(regtype), nil, nil},
		{"zero", value(0), nil, nil},
		{"missing", value(49999), nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			literal := plan.LiteralExpr{Info: expr.Info{Usage: regtype.Usage()}, Value: tc.value}
			refs, err := stored.ExpressionDependRefs(context.Background(), view, literal)
			if err != nil {
				t.Fatal(err)
			}
			pgDependUnitRefs(t, refs, tc.want)
			var captured []ids.ObjectRef
			if err := (stored.ScalarRenderer{Dependencies: &captured}).RecordCatalogAliasDependencies(view, tc.value); err != nil {
				t.Fatal(err)
			}
			var addresses []ids.ObjectAddress
			for _, ref := range captured {
				if ref.Domain() != e.Catalog.Domain || ref.Epoch != view.Epoch() {
					t.Fatal("lost identity provenance", ref)
				}
				addresses = append(addresses, ref.Address)
			}
			pgDependUnitRefs(t, addresses, tc.capture)
		})
	}

	wrappedType, err := sqltype.Domain(48103, "wrapped", "public.wrapped", regtype.Usage())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := wrappedType.Wrap(value(provider.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	arrayType, err := sqltype.Array(48104, "_wrapped", wrappedType)
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.ArrayValue(arrayType, []sqltype.ArrayDimension{{Lower: -1, Length: 3}}, []sqltype.Value{wrapped, sqltype.Null(wrappedType), wrapped})
	if err != nil {
		t.Fatal(err)
	}
	compositeType, err := sqltype.Composite(48105, "payload", "public.payload", 0, []sqltype.CompositeMember{
		{Attribute: 1, Name: "types", Usage: arrayType.Usage()},
		{Attribute: 2, Name: "row_type", Usage: regtype.Usage()},
	})
	if err != nil {
		t.Fatal(err)
	}
	composite, err := sqltype.CompositeValue(compositeType, []sqltype.Value{array, value(rowAddress.ObjectID)})
	if err != nil {
		t.Fatal(err)
	}
	payloadType, err := sqltype.Domain(48106, "payload_domain", "public.payload_domain", compositeType.Usage())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := payloadType.Wrap(composite)
	if err != nil {
		t.Fatal(err)
	}
	payloadAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(payloadType.ID())}
	view.State().SetDescriptor(payloadAddress, &catalog.Type{SQLType: payloadType})
	literal := plan.LiteralExpr{Info: expr.Info{Usage: payloadType.Usage()}, Value: payload}
	want := []ids.ObjectAddress{payloadAddress, provider, rowAddress}
	refs, err := stored.ExpressionDependRefs(context.Background(), view, literal)
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, refs, want)
	refs, err = stored.QueryDependRefs(context.Background(), view, nil, &plan.Query{Root: &plan.ProjectNode{Expressions: []expr.Expr{literal}}})
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, refs, want)
	var captured []ids.ObjectRef
	if err := (stored.ScalarRenderer{Dependencies: &captured}).RecordCatalogAliasDependencies(view, payload); err != nil {
		t.Fatal(err)
	}
	var addresses []ids.ObjectAddress
	for _, ref := range captured {
		if ref.Domain() != e.Catalog.Domain || ref.Epoch != view.Epoch() {
			t.Fatal("lost recursive identity provenance", ref)
		}
		addresses = append(addresses, ref.Address)
	}
	pgDependUnitRefs(t, addresses, []ids.ObjectAddress{provider, rowAddress})

	// A callback error must stop traversal rather than being overwritten by
	// later identities. No transformed payload is allocated by the walk.
	stop := errors.New("stop recording")
	calls := 0
	if err := catalog.WalkAliasReferences(view, payload, func(ids.ObjectAddress) error {
		calls++
		return stop
	}); !errors.Is(err, stop) || calls != 1 {
		t.Fatal("callback failure lost", err, calls)
	}
	remapped, err := stored.RemapExpression(literal, func(ref ids.ObjectRef) ids.ObjectRef {
		if ref.Address == provider {
			ref.Address = rowAddress
		}
		return ref
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	refs, err = stored.ExpressionDependRefs(context.Background(), view, remapped)
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, refs, []ids.ObjectAddress{payloadAddress, rowAddress})
	originalID, err := payload.CompositeField(0).ArrayElement(0).TypeOID()
	if err != nil || ids.OID(originalID) != provider.ObjectID {
		t.Fatal("remapping mutated captured value", originalID, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if refs, err := stored.ExpressionDependRefs(canceled, view, literal); !errors.Is(err, context.Canceled) || refs != nil {
		t.Fatal("cancellation lost", refs, err)
	}
}
