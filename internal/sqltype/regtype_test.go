package sqltype

import (
	"context"
	"strconv"
	"testing"
)

func TestRegtypeInventoryCapability(t *testing.T) {
	types := testTypes()
	if len(types) != 621 {
		t.Fatalf("initialized identity count: %d", len(types))
	}
	for id, d := range types {
		if d.ID() != id || d.Name() == "" || d.NamespaceName() == "" {
			t.Fatal("invalid identity", id, d)
		}
		if arrayID := d.AssociatedArray(); arrayID != 0 {
			a := types[arrayID]
			if a == nil || !a.IsArray() || a.Element() != d || a.NamespaceName() != d.NamespaceName() {
				t.Fatal("nonreciprocal array", id, arrayID)
			}
		}
	}
	for _, id := range []ID{1002, 2287, 1041, 143, 6155, 1017, 1020, 629} {
		d := types[id]
		if d == nil || d.Concrete() || d.Usage().Valid() || d.IsUserDefined() {
			t.Fatal("lookup identity became executable", id, d)
		}
	}
	if types[22].IsArray() || types[30].IsArray() || types[19].IsArray() {
		t.Fatal("typelem mistaken for array subscript authority")
	}
	if types[603].Delimiter() != ';' || types[1020].Delimiter() != ';' {
		t.Fatal("BOX array delimiter")
	}
	for _, id := range []ID{Box, Point, Line} {
		if !types[id].Concrete() || types[id].IsArray() {
			t.Fatal("geometry scalar/raw typelem confused with SQL array", id)
		}
		if equality, ordering := ComparisonCapabilities(types[id]); equality || ordering {
			t.Fatal("geometry scalar operators became generic comparison authority", id)
		}
	}
	if types[2249].AssociatedArray() != 2287 || types[705].AssociatedArray() != 0 || types[2278].AssociatedArray() != 0 {
		t.Fatal("record/unknown/void associations")
	}
	lookup := types[1041]
	if _, err := ArrayValue(lookup, nil, nil); err == nil {
		t.Fatal("lookup-only array value admitted")
	}
	if _, err := lookup.Input(context.Background(), "{}", -1); err == nil {
		t.Fatal("lookup-only array codec admitted")
	}
	if _, err := PlanCoercion(types[Text].Usage(), lookup.Usage(), Explicit); err == nil {
		t.Fatal("lookup-only array admitted through text coercion")
	}
	if _, err := Null(types[1007]).Retag(LookupArray(1007, "_int4", types[Int4])); err == nil {
		t.Fatal("lookup-only identity admitted through retained value remapping")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("lookup-only NULL admitted")
			}
		}()
		_ = Null(lookup)
	}()
}

func TestRegtypeInlineUnsignedCodec(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	d := types[Regtype]
	order, err := BindOrdering(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	group, err := BindGrouping(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	class, _ := LookupHashIndexClass("oid_ops")
	hash, err := BindIndexHash(d.Usage(), class)
	if err != nil {
		t.Fatal(err)
	}
	fromOID, err := PlanCoercion(types[Oid].Usage(), d.Usage(), Implicit)
	if err != nil {
		t.Fatal(err)
	}
	previous, _ := RegtypeValue(d, 0)
	for _, id := range []uint32{0, 23, 2147483648, 4294967295} {
		value, err := RegtypeValue(d, id)
		if err != nil {
			t.Fatal(err)
		}
		if value.text != "" {
			t.Fatal("identity codec allocated display spelling")
		}
		actual, err := value.TypeOID()
		if err != nil || actual != id {
			t.Fatal(actual, err)
		}
		out, err := d.Output(ctx, value)
		if err != nil || out != strconv.FormatUint(uint64(id), 10) {
			t.Fatal(out, err)
		}
		cmp, err := order.Compare(ctx, previous, value, InputEnvironment{})
		if err != nil || id == 0 && cmp != 0 || id != 0 && cmp >= 0 {
			t.Fatal("signed or lexical ordering", cmp, err)
		}
		oid, err := OIDValue(types[Oid], id)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := fromOID.Apply(ctx, oid, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		same, err := group.Equal(ctx, value, decoded)
		if err != nil || !same {
			t.Fatal("OID coercion changed grouping identity", same, err)
		}
		first, err := hash.Hash(ctx, value)
		if err != nil {
			t.Fatal(err)
		}
		second, err := hash.Hash(ctx, decoded)
		if err != nil || first != second {
			t.Fatal("OID coercion changed index hash", first, second, err)
		}
		same, err = hash.Equal(ctx, previous, value)
		if err != nil || same != (id == 0) {
			t.Fatal("distinct unsigned index keys collapsed", id, same, err)
		}
		previous = value
	}
	if _, err := Null(d).TypeOID(); err == nil {
		t.Fatal("NULL accessor admitted")
	}
	if _, err := d.Input(ctx, "integer", -1); err == nil {
		t.Fatal("symbolic input lacked captured authority")
	}
}

func TestRegtypeDirectCastBoundaries(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	for _, tc := range []struct {
		from, to ID
		minimum  CoercionContext
	}{
		{Int2, Regtype, Implicit}, {Int4, Regtype, Implicit}, {Int8, Regtype, Implicit},
		{Oid, Regtype, Implicit}, {Regtype, Oid, Implicit},
		{Regtype, Int4, Assignment}, {Regtype, Int8, Assignment},
		{Regtype, Text, Assignment}, {Regtype, VarChar, Assignment},
		{Text, Regtype, Explicit}, {VarChar, Regtype, Explicit},
	} {
		for mode := Implicit; mode <= Explicit; mode++ {
			_, err := PlanCoercion(types[tc.from].Usage(), types[tc.to].Usage(), mode)
			if (err == nil) != (mode >= tc.minimum) {
				t.Fatal(tc, mode, err)
			}
		}
	}
	for _, id := range []ID{Bool, Int2, Regclass} {
		if _, err := PlanCoercion(types[Regtype].Usage(), types[id].Usage(), Explicit); err == nil {
			t.Fatal("invented cast", id)
		}
	}
	negative, _ := Integer(types[Int4], -1)
	plan, err := PlanCoercion(types[Int4].Usage(), types[Regtype].Usage(), Implicit)
	if err != nil {
		t.Fatal(err)
	}
	value, err := plan.Apply(ctx, negative, InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := value.TypeOID()
	if err != nil || id != 4294967295 {
		t.Fatal(id, err)
	}
	// String-target assignment uses released IO, not an implicit TEXT cast.
	// Applying the plan witnesses the actual conversion, rather than admission
	// alone; without a catalog formatter the codec exposes the unsigned OID.
	for _, target := range []ID{Text, VarChar} {
		for _, mode := range []CoercionContext{Assignment, Explicit} {
			conversion, err := PlanCoercion(types[Regtype].Usage(), types[target].Usage(), mode)
			if err != nil {
				t.Fatal(target, mode, err)
			}
			converted, err := conversion.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
			if err != nil {
				t.Fatal(target, mode, err)
			}
			text, err := converted.Text()
			if err != nil || text != "4294967295" || converted.Type() != types[target] {
				t.Fatal("unsigned string-target IO", target, mode, text, err)
			}
			converted, err = conversion.Apply(ctx, Null(types[Regtype]), InputEnvironment{}, OutputEnvironment{})
			if err != nil || !converted.IsNull() || converted.Type() != types[target] {
				t.Fatal("string-target IO lost typed NULL", target, mode, err)
			}
		}
	}
}
