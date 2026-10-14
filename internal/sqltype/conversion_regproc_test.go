package sqltype

import (
	"context"
	"encoding/binary"
	"testing"
)

func TestRoutineAliasCarrier(t *testing.T) {
	ctx, types := context.Background(), testTypes()
	for _, id := range []ID{Regproc, Regprocedure} {
		d := types[id]
		array := types[d.AssociatedArray()]
		if !d.Concrete() || !array.Concrete() || d.PostgreSQLTypeLength() != 4 || !d.ContainsCatalogIdentifier() || !array.ContainsRegproc() {
			t.Fatal("routine alias inventory/capability", d, array)
		}
		for _, oid := range []uint32{0, 1242, 2147483648, 4294967295} {
			value, err := RegprocValue(d, oid)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := value.ProcedureOID()
			if err != nil || actual != oid {
				t.Fatal("unsigned identity", actual, err)
			}
			plan, err := PlanCoercion(d.Usage(), types[Oid].Usage(), Implicit)
			if err != nil {
				t.Fatal(err)
			}
			converted, err := plan.Apply(ctx, value, InputEnvironment{}, OutputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			actual, err = converted.OID()
			if err != nil || actual != oid {
				t.Fatal("alias -> OID", actual, err)
			}
			order, err := BindOrdering(d.Usage())
			if err != nil {
				t.Fatal(err)
			}
			if comparison, err := order.Compare(ctx, value, value, InputEnvironment{}); err != nil || comparison != 0 {
				t.Fatal("ordering", comparison, err)
			}
			group, err := BindGrouping(d.Usage())
			if err != nil {
				t.Fatal(err)
			}
			if equal, err := group.Equal(ctx, value, value); err != nil || !equal {
				t.Fatal("equality", equal, err)
			}
			if _, err := d.OutputWithEnvironment(ctx, value, OutputEnvironment{}); err == nil {
				t.Fatal("output lacks captured presentation")
			}
			raw, err := d.AppendBinaryWithEnvironment(ctx, nil, value, OutputEnvironment{})
			if err != nil || len(raw) != 4 || binary.BigEndian.Uint32(raw) != oid {
				t.Fatal("binary identity", raw, err)
			}
		}
		if _, err := d.Input(ctx, "1242", -1); err == nil {
			t.Fatal("input lacks catalog authority")
		}
		for _, source := range []ID{Text, Oid, Int2, Int4, Int8, Regproc, Regprocedure} {
			if _, err := PlanCoercion(types[source].Usage(), d.Usage(), Explicit); err != nil {
				t.Fatal("released input cast", source, err)
			}
		}
		for _, target := range []ID{Int4, Int8, Oid, Text, VarChar, Regproc, Regprocedure} {
			if _, err := PlanCoercion(d.Usage(), types[target].Usage(), Explicit); err != nil {
				t.Fatal("released output cast", target, err)
			}
		}
		for _, adjacent := range []ID{Regtype, Regclass, Bool} {
			if _, err := PlanCoercion(types[adjacent].Usage(), d.Usage(), Explicit); err == nil {
				t.Fatal("unreleased alias cast", adjacent)
			}
			if _, err := PlanCoercion(d.Usage(), types[adjacent].Usage(), Explicit); err == nil {
				t.Fatal("unreleased alias cast", adjacent)
			}
		}
		text, err := PlanCoercion(d.Usage(), types[Text].Usage(), Explicit)
		if err != nil || !text.RequiresCatalogOutput() {
			t.Fatal("text cast lacks output capture", text, err)
		}
		null, err := text.Apply(ctx, Null(d), InputEnvironment{}, OutputEnvironment{})
		if err != nil || !null.IsNull() || null.Type() != types[Text] {
			t.Fatal("NULL propagation", null, err)
		}
	}
	if _, err := RegprocValue(types[Oid], 1242); err == nil {
		t.Fatal("OID mislabeled routine alias")
	}
}
