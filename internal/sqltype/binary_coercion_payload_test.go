package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestCastDefinitionBinaryPayload(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	input := func(id ID, text string) Value {
		t.Helper()
		v, err := types[id].Input(ctx, text, -1)
		if err != nil {
			t.Fatal(id, text, err)
		}
		return v
	}
	t.Run("octets", func(t *testing.T) {
		citext, err := Citext(61000, "citext", "public.citext")
		if err != nil {
			t.Fatal(err)
		}
		family := []*Descriptor{types[Text], types[VarChar], types[BPChar], types[JSON], types[Bytea], citext}
		for _, source := range family {
			for _, target := range family {
				// Invalid JSON syntax is intentional: binary conversion must not
				// parse JSON, decode BYTEA escapes, fold CITEXT or trim BPCHAR.
				raw := `\x4142 Not JSON é  `
				v := Value{typ: source, text: raw}
				got := castBinaryUnitApply(t, v, target)
				if got.text != raw || got.Type() != target {
					t.Fatal("octets changed", source.Name(), target.Name(), got)
				}
				back := castBinaryUnitApply(t, got, source)
				if !SameDatum(v, back) {
					t.Fatal("octet roundtrip changed payload")
				}
			}
		}
		for _, raw := range []string{"a\x00b", "\xff", "\xc0\xaf", "\xed\xa0\x80"} {
			v, _ := Binary(types[Bytea], raw)
			for _, target := range family {
				if target.id == Bytea {
					continue
				}
				p, _ := PlanBinaryCoercion(types[Bytea], target)
				_, err := p.Apply(ctx, v)
				castBinaryUnitError(t, err, "binary cast value is not representable in embedded mode")
			}
		}
	})
	t.Run("bitAndNetwork", func(t *testing.T) {
		for _, text := range []string{"", "0", "101001001"} {
			v, err := BitString(types[Bit], text)
			if err != nil {
				t.Fatal(err)
			}
			back := castBinaryUnitApply(t, castBinaryUnitApply(t, v, types[VarBit]), types[Bit])
			if !SameDatum(v, back) {
				t.Fatal("canonical bit payload changed")
			}
		}
		for _, text := range []string{"192.0.2.0/24", "2001:db8::/32", "0.0.0.0/0", "::/0"} {
			v := input(Cidr, text)
			back := castBinaryUnitApply(t, castBinaryUnitApply(t, v, types[Inet]), types[Cidr])
			if !SameDatum(v, back) {
				t.Fatal("network family/prefix/address changed", text)
			}
		}
		p, _ := PlanBinaryCoercion(types[Inet], types[Cidr])
		v := input(Inet, "192.0.2.42/24")
		_, err := p.Apply(ctx, v)
		castBinaryUnitError(t, err, "binary cast value is not representable in embedded mode")
		if text, err := types[Inet].Output(ctx, v); err != nil || text != "192.0.2.42/24" {
			t.Fatal("failed cast masked source host bits", text, err)
		}
	})
	t.Run("geometry", func(t *testing.T) {
		v := input(Lseg, "[(0,1),(3,4)]")
		box := castBinaryUnitApply(t, v, types[Box])
		if box.geometry != v.geometry || box.geometry.coordinates != [4]float64{0, 1, 3, 4} {
			t.Fatal("BOX input normalization replaced raw pair order")
		}
		back := castBinaryUnitApply(t, box, types[Lseg])
		if !SameDatum(v, back) {
			t.Fatal("ordered point roundtrip changed payload")
		}
		line := input(Line, "{2,3,4}")
		circle := castBinaryUnitApply(t, line, types[Circle])
		if circle.circle != (circleDatum{2, 3, 4}) {
			t.Fatal("LINE triple was normalized", circle)
		}
		if !SameDatum(line, castBinaryUnitApply(t, circle, types[Line])) {
			t.Fatal("triple roundtrip changed payload")
		}
		// Signed-zero and NaN payloads are words, not geometric equality.
		raw := Value{typ: types[Line], geometry: &geometryPayload{coordinates: [4]float64{math.Float64frombits(0x7ff8000000000001), math.Copysign(0, -1), math.Inf(1)}}}
		if !SameDatum(raw, castBinaryUnitApply(t, castBinaryUnitApply(t, raw, types[Circle]), types[Line])) {
			t.Fatal("triple mapping changed IEEE payloads")
		}
		p, _ := PlanBinaryCoercion(types[Circle], types[Line])
		_, err := p.Apply(ctx, input(Circle, "<(0,0),1>"))
		castBinaryUnitError(t, err, "binary cast value is not representable in embedded mode")
	})
	t.Run("regclassNumericAuthority", func(t *testing.T) {
		v := castBinaryUnitApply(t, Value{typ: types[Xid], oid: math.MaxUint32}, types[Regclass])
		if v.text != "" || v.oid != math.MaxUint32 || v.integer != 1 {
			t.Fatal("REGCLASS lost allocation-free numeric authority", v)
		}
		canonical := String(types[Regclass], "4294967295")
		if !SameDatum(v, canonical) {
			t.Fatal("canonical text and numeric identity differ")
		}
		if same, err := v.SamePayload(ctx, canonical); err != nil || !same {
			t.Fatal("canonical regclass payload differs", err)
		}
		if text, err := v.Text(); err != nil || text != "4294967295" {
			t.Fatal(text, err)
		}
		if text, err := types[Regclass].SQLLiteral(ctx, v); err != nil || text != "'4294967295'" {
			t.Fatal(text, err)
		}
		g, err := BindGrouping(types[Regclass].Usage())
		if err != nil {
			t.Fatal(err)
		}
		a, err := g.Hash(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		b, err := g.Hash(ctx, canonical)
		if err != nil || a != b {
			t.Fatal("hash differs by numeric provenance", a, b, err)
		}
		if equal, err := g.Equal(ctx, v, canonical); err != nil || !equal {
			t.Fatal("grouping differs by numeric provenance", err)
		}
		zero := castBinaryUnitApply(t, Value{typ: types[Xid]}, types[Regclass])
		if SameDatum(zero, String(types[Regclass], "")) {
			t.Fatal("unresolved empty name equaled numeric OID zero")
		}
	})
}

// Deterministic cancellation at codec polling, not a sleep or scheduling race.
type castBinaryCancelContext struct {
	context.Context
	calls int
}

func (c *castBinaryCancelContext) Err() error {
	c.calls++
	if c.calls >= 3 {
		return context.Canceled
	}
	return nil
}

func TestCastDefinitionBinaryAllocations(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	raw := strings.Repeat("é", 1<<16)
	p, _ := PlanBinaryCoercion(types[Bytea], types[Text])
	v, _ := Binary(types[Bytea], raw)
	cancel := &castBinaryCancelContext{Context: ctx}
	if _, err := p.Apply(cancel, v); !errors.Is(err, context.Canceled) {
		t.Fatal("long octet validation missed cancellation", err)
	}
	cases := []struct {
		source, target *Descriptor
		value          Value
	}{
		{types[Xid], types[Float4], Value{typ: types[Xid], oid: 0x7f800001}},
		{types[Xid], types[Regclass], Value{typ: types[Xid], oid: math.MaxUint32}},
		{types[Xid8], types[Float8], Value{typ: types[Xid8], integer: 0x3ff0000000000000}},
		{types[Bytea], types[Text], v},
		{types[Text], types[Bytea], String(types[Text], raw)},
	}
	for _, tc := range cases {
		p, err := PlanBinaryCoercion(tc.source, tc.target)
		if err != nil {
			t.Fatal(err)
		}
		result := testing.Benchmark(func(b *testing.B) {
			for range b.N {
				for range 16 {
					value, err := p.Apply(ctx, tc.value)
					if err != nil || value.typ != tc.target {
						b.Fatal(value, err)
					}
				}
			}
		})
		if result.AllocedBytesPerOp() != 0 || result.AllocsPerOp() != 0 {
			t.Fatal("binary mapping allocated payload bytes", tc.source.Name(), tc.target.Name(), result.AllocedBytesPerOp(), result.AllocsPerOp())
		}
	}
}

func TestCastDefinitionNegativeCircleBinary(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	p, err := PlanBinaryCoercion(types[Line], types[Circle])
	if err != nil {
		t.Fatal(err)
	}
	source, err := types[Line].Input(ctx, "{1,2,-3}", -1)
	if err != nil {
		t.Fatal(err)
	}
	circle, err := p.Apply(ctx, source)
	if err != nil || circle.circle != (circleDatum{1, 2, -3}) || !circle.RequiresCircleConstructorReplay() {
		t.Fatalf("signed radius changed or replay lost: %v, %v", circle, err)
	}
	if text, err := types[Circle].Output(ctx, circle); err != nil || text != "<(1,2),-3>" {
		t.Fatal(text, err)
	}
	if back := castBinaryUnitApply(t, circle, types[Line]); !SameDatum(source, back) {
		t.Fatal("signed triple failed roundtrip")
	}
	clone := circle
	if !SameDatum(circle, clone) || !clone.RequiresCircleConstructorReplay() {
		t.Fatal("copy lost signed radius")
	}
	null, err := p.Apply(ctx, Null(types[Line]))
	if err != nil || !null.IsNull() || null.Type() != types[Circle] || null.RequiresCircleConstructorReplay() {
		t.Fatal("NULL mapping changed", err)
	}
	foreign := testTypes()
	if _, err := p.Apply(ctx, Null(foreign[Line])); err == nil {
		t.Fatal("foreign source generation accepted")
	}
	cancel, stop := context.WithCancel(ctx)
	stop()
	if _, err := p.Apply(cancel, source); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if _, err := p.Apply(ctx, Value{typ: types[Line]}); err == nil {
		t.Fatal("missing LINE payload accepted")
	}
	// Binary relabel admission does not relax circle_in or circle_recv.
	if _, err := types[Circle].Input(ctx, "<(1,2),-3>", -1); err == nil {
		t.Fatal("ordinary circle input admitted negative radius")
	}
	wire := binaryUnitHex(t, "3ff00000000000004000000000000000c008000000000000")
	if _, err := types[Circle].BinaryInputWithEnvironment(ctx, wire, -1, InputEnvironment{}); err == nil {
		t.Fatal("ordinary circle receive admitted negative radius")
	}
}

func TestAggregateBinaryConversionRebind(t *testing.T) {
	ctx := context.Background()
	first, second := testTypes(), testTypes()
	plan, err := PlanDeclaredBinaryCoercion(first[Text].Usage(), first[Bytea].Usage())
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := plan.Rebind(second[Text].Usage(), second[Bytea].Usage(), Implicit)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"abc", `\x4142`, ""} {
		source, err := second[Text].Input(ctx, raw, -1)
		if err != nil {
			t.Fatal(err)
		}
		value, err := mapped.Apply(ctx, source, InputEnvironment{}, OutputEnvironment{})
		if err != nil || value.Type() != second[Bytea] || value.text != raw {
			t.Fatal("remapped binary adapter decoded or lost bytes", raw, value, err)
		}
		if _, err := plan.Apply(ctx, source, InputEnvironment{}, OutputEnvironment{}); err == nil {
			t.Fatal("source descriptor from a different owner accepted")
		}
	}
	value, err := mapped.Apply(ctx, Null(second[Text]), InputEnvironment{}, OutputEnvironment{})
	if err != nil || !value.IsNull() || value.Type() != second[Bytea] {
		t.Fatal("binary typed NULL ownership", value, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := mapped.Apply(cancelled, Null(second[Text]), InputEnvironment{}, OutputEnvironment{}); err == nil {
		t.Fatal("cancelled binary conversion succeeded")
	}
	if _, err := plan.Rebind(second[Int4].Usage(), second[Int8].Usage(), Implicit); err == nil {
		t.Fatal("remap changed the captured octet representation to a word")
	}
}
