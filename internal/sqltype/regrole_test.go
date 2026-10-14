package sqltype

import (
	"bytes"
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/sqlstate"
)

type regroleUnitProvider struct{}

func (regroleUnitProvider) ResolveRegrole(ctx context.Context, text string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if text == "unit_role" {
		return 20000, nil
	}
	return 0, failure("42704", `role "`+text+`" does not exist`)
}
func (regroleUnitProvider) FormatRegrole(id uint32) (string, error) { return "unit_role", nil }

func TestRegroleDatum(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	d := types[Regrole]
	if d.ID() != 4096 || d.PostgreSQLTypeLength() != 4 || d.AssociatedArray() != 4097 || types[4097].Element() != d || !types[4097].Concrete() {
		t.Fatal("regrole inventory", d)
	}
	if usage := d.Usage(); usage.Typmod != -1 || usage.Collation.ID != 0 {
		t.Fatal("regrole usage metadata", usage)
	}
	for _, invalid := range []*Descriptor{nil, types[Oid], LookupArray(4097, "_regrole", d)} {
		if _, err := RegroleValue(invalid, 10); err == nil {
			t.Fatal("invalid descriptor admitted")
		}
	}
	if _, err := Null(d).RoleOID(); err == nil {
		t.Fatal("NULL identity admitted")
	}
	order, err := BindOrdering(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	group, err := BindGrouping(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	previous, _ := RegroleValue(d, 0)
	for _, id := range []uint32{0, 10, 2147483648, math.MaxUint32} {
		v, err := RegroleValue(d, id)
		if err != nil {
			t.Fatal(err)
		}
		got, err := v.RoleOID()
		if err != nil || got != id || v.text != "" {
			t.Fatal("inline identity", got, err)
		}
		cmp, err := order.Compare(ctx, previous, v, InputEnvironment{})
		if err != nil || id == 0 && cmp != 0 || id != 0 && cmp >= 0 {
			t.Fatal("unsigned ordering", cmp, err)
		}
		equal, err := group.Equal(ctx, previous, v)
		if err != nil || equal != (id == 0) {
			t.Fatal("identity grouping", equal, err)
		}
		previous = v
	}
}

func TestRegroleCodec(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	d := types[Regrole]
	input := InputEnvironment{Regrole: regroleUnitProvider{}}
	for _, tc := range []struct {
		text string
		id   uint32
	}{{"-", 0}, {"0", 0}, {"010", 8}, {"4294967295", math.MaxUint32}, {"unit_role", 20000}} {
		v, err := d.InputWithEnvironment(ctx, tc.text, -1, input)
		if err != nil {
			t.Fatal(tc.text, err)
		}
		id, err := v.RoleOID()
		if err != nil || id != tc.id {
			t.Fatal(tc.text, id, err)
		}
	}
	for _, tc := range []struct{ text, code, message string }{
		{"08", "22P02", `invalid input syntax for type oid: "08"`},
		{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
		{" 10 ", "42704", `role " 10 " does not exist`},
	} {
		_, err := d.InputWithEnvironment(ctx, tc.text, -1, input)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
			t.Fatal(tc.text, err)
		}
	}
	for _, tc := range []struct {
		from, to ID
		minimum  CoercionContext
	}{
		{Int2, Regrole, Implicit}, {Int4, Regrole, Implicit}, {Int8, Regrole, Implicit}, {Oid, Regrole, Implicit}, {Regrole, Oid, Implicit},
		{Regrole, Int4, Assignment}, {Regrole, Int8, Assignment}, {Text, Regrole, Explicit}, {VarChar, Regrole, Explicit},
	} {
		for mode := Implicit; mode <= Explicit; mode++ {
			_, err := PlanCoercion(types[tc.from].Usage(), types[tc.to].Usage(), mode)
			if (err == nil) != (mode >= tc.minimum) {
				t.Fatal("cast admission", tc, mode, err)
			}
		}
	}
	for _, id := range []ID{Regclass, Regtype, Regproc, Bool} {
		if _, err := PlanCoercion(types[id].Usage(), d.Usage(), Explicit); err == nil {
			t.Fatal("invented role cast", id)
		}
	}
	plan, err := PlanCoercion(types[Int4].Usage(), d.Usage(), Implicit)
	if err != nil {
		t.Fatal(err)
	}
	negative, _ := Integer(types[Int4], -1)
	v, err := plan.Apply(ctx, negative, input, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := v.RoleOID()
	if id != math.MaxUint32 {
		t.Fatal("int4 bits", id)
	}
	wide, _ := Integer(types[Int8], -1)
	plan, err = PlanCoercion(types[Int8].Usage(), d.Usage(), Implicit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply(ctx, wide, input, OutputEnvironment{}); err == nil {
		t.Fatal("int8 overflow accepted")
	}
	for _, payload := range [][]byte{{0, 0, 0, 0}, {255, 255, 255, 255}} {
		v, err := d.BinaryInputWithEnvironment(ctx, payload, -1, input)
		if err != nil {
			t.Fatal(err)
		}
		out, err := d.AppendBinaryWithEnvironment(ctx, nil, v, OutputEnvironment{})
		if err != nil || !bytes.Equal(payload, out) {
			t.Fatal("network-order codec", out, err)
		}
	}
	for _, payload := range [][]byte{nil, {0}, {0, 0, 0}, {0, 0, 0, 0, 0}} {
		if _, err := d.BinaryInputWithEnvironment(ctx, payload, -1, input); err == nil {
			t.Fatal("malformed binary length admitted")
		}
	}
	array, err := types[4097].InputWithEnvironment(ctx, "{unit_role,NULL,0}", -1, input)
	if err != nil {
		t.Fatal(err)
	}
	out, err := array.Type().OutputWithEnvironment(ctx, array, OutputEnvironment{Regrole: regroleUnitProvider{}})
	if err != nil || out != "{unit_role,NULL,unit_role}" {
		t.Fatal("recursive role provider", out, err)
	}
}
