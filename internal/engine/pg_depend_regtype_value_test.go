package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Represented type identities are distinct from both the literal datatype and
// relation identities with the same numeric OID. Frozen capture and pg_depend
// share the value walk, but intentionally retain different pinned-ref rules.
func TestPGDependRegtypeValueReferences(t *testing.T) {
	e, catalog, integer := pgDependUnitCatalog(t)
	regtype, ok := catalog.LookupType(sqltype.Regtype)
	if !ok {
		t.Fatal("missing regtype")
	}
	domain, err := sqltype.Domain(48101, "provider", "public.provider", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	provider := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(domain.ID())}
	catalog.state.setDescriptor(provider, &typeDescriptor{typ: domain})
	row, err := sqltype.Composite(48102, "row_provider", "public.row_provider", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	rowAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.ID())}
	relationAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(row.ID())}
	catalog.state.setDescriptor(rowAddress, &typeDescriptor{typ: row, owningRelation: relationAddress})
	catalog.state.setDescriptor(relationAddress, &relationDescriptor{address: relationAddress, rowType: row.ID()})
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
			literal := literalExpression{expressionInfo: expressionInfo{Usage: regtype.Usage()}, value: tc.value}
			refs, err := pgDependExpressionRefs(context.Background(), catalog, literal)
			if err != nil {
				t.Fatal(err)
			}
			pgDependUnitRefs(t, refs, tc.want)
			var captured []ids.ObjectRef
			if err := (scalarRenderer{dependencies: &captured}).recordCatalogAliasDependencies(catalog, tc.value); err != nil {
				t.Fatal(err)
			}
			var addresses []ids.ObjectAddress
			for _, ref := range captured {
				if ref.Domain() != e.catalog.domain || ref.Epoch != catalog.Epoch() {
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
	catalog.state.setDescriptor(payloadAddress, &typeDescriptor{typ: payloadType})
	literal := literalExpression{expressionInfo: expressionInfo{Usage: payloadType.Usage()}, value: payload}
	want := []ids.ObjectAddress{payloadAddress, provider, rowAddress}
	refs, err := pgDependExpressionRefs(context.Background(), catalog, literal)
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, refs, want)
	refs, err = pgDependQueryRefs(context.Background(), catalog, &BoundQuery{Root: &projectNode{expressions: []BoundExpr{literal}}})
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, refs, want)
	var captured []ids.ObjectRef
	if err := (scalarRenderer{dependencies: &captured}).recordCatalogAliasDependencies(catalog, payload); err != nil {
		t.Fatal(err)
	}
	var addresses []ids.ObjectAddress
	for _, ref := range captured {
		if ref.Domain() != e.catalog.domain || ref.Epoch != catalog.Epoch() {
			t.Fatal("lost recursive identity provenance", ref)
		}
		addresses = append(addresses, ref.Address)
	}
	pgDependUnitRefs(t, addresses, []ids.ObjectAddress{provider, rowAddress})

	// A callback error must stop traversal rather than being overwritten by
	// later identities. No transformed payload is allocated by the walk.
	stop := errors.New("stop recording")
	calls := 0
	if err := walkCatalogAliasReferences(catalog, payload, func(ids.ObjectAddress) error {
		calls++
		return stop
	}); !errors.Is(err, stop) || calls != 1 {
		t.Fatal("callback failure lost", err, calls)
	}
	remapped, err := remapBoundExpression(literal, func(ref ids.ObjectRef) ids.ObjectRef {
		if ref.Address == provider {
			ref.Address = rowAddress
		}
		return ref
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	refs, err = pgDependExpressionRefs(context.Background(), catalog, remapped)
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
	if refs, err := pgDependExpressionRefs(canceled, catalog, literal); !errors.Is(err, context.Canceled) || refs != nil {
		t.Fatal("cancellation lost", refs, err)
	}
}
