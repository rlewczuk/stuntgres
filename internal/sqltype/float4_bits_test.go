package sqltype

import (
	"context"
	"encoding/binary"
	"math"
	"testing"
)

func TestCastDefinitionFloat4RawBits(t *testing.T) {
	ctx := context.Background()
	types, foreign := testTypes(), testTypes()
	words := []uint32{0, 0x80000000, 1, 0x007fffff, 0x00800000, 0x3f800000, 0x7f7fffff, 0x7f800000, 0xff800000, 0x7fc00001, 0x7f800001, 0xff800001, 0x7fffffff}
	for _, word := range words {
		v, err := Float4FromBits(types[Float4], word)
		if err != nil {
			t.Fatal(err)
		}
		check := func(value Value) {
			t.Helper()
			got, err := value.Float4Bits()
			if err != nil || got != word {
				t.Fatalf("raw32 %08x became %08x: %v", word, got, err)
			}
		}
		check(v)
		copy := v
		check(copy)
		reowned, err := v.Retag(foreign[Float4])
		if err != nil {
			t.Fatal(err)
		}
		check(reowned)
		same, err := v.SamePayload(ctx, reowned)
		if err != nil || !same || SameDatum(v, reowned) {
			t.Fatal("payload/descriptor authority conflated", same, err)
		}
		var wire [4]byte
		binary.BigEndian.PutUint32(wire[:], word)
		received, err := types[Float4].BinaryInputWithEnvironment(ctx, wire[:], -1, InputEnvironment{})
		if err != nil || !SameDatum(v, received) {
			t.Fatal("binary receive changed word", word, err)
		}
		check(received)
		var scratch [8]byte
		out, err := types[Float4].AppendBinaryWithEnvironment(ctx, scratch[:2], received, OutputEnvironment{})
		if err != nil || len(out) != 6 || binary.BigEndian.Uint32(out[2:]) != word {
			t.Fatalf("binary send changed %08x: %x %v", word, out, err)
		}
		back := castBinaryUnitApply(t, v, types[Xid])
		if back.oid != word {
			t.Fatal("binary cast ignored authority", word, back.oid)
		}
		changed, _ := Float4FromBits(types[Float4], word^1)
		if SameDatum(v, changed) {
			t.Fatal("SameDatum lost raw payload distinction", word)
		}
		if same, err := v.SamePayload(ctx, changed); err != nil || same {
			t.Fatal("SamePayload lost raw payload distinction", word, err)
		}
	}
	for _, word := range []uint32{0, 0x80000000, 1, 0x3f800000, 0x7f800000} {
		raw, _ := Float4FromBits(types[Float4], word)
		ordinary, err := Float(types[Float4], float64(math.Float32frombits(word)))
		if err != nil || !SameDatum(raw, ordinary) {
			t.Fatal("raw provenance changed datum semantics", word, err)
		}
		if same, err := raw.SamePayload(ctx, ordinary); err != nil || !same {
			t.Fatal("raw provenance changed payload semantics", word, err)
		}
	}
	one, _ := Float4FromBits(types[Float4], 0x3f800000)
	p, err := BindArithmetic(ArithmeticAdd, types[Float4], types[Float4], types[Float4])
	if err != nil {
		t.Fatal(err)
	}
	two, err := p.Binary(ctx, one, one, InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if bits, err := two.Float4Bits(); err != nil || bits != 0x40000000 || two.float4RawValid {
		t.Fatal("arithmetic retained stale raw authority", bits, err)
	}
	snan, _ := Float4FromBits(types[Float4], 0x7f800001)
	computed, err := p.Binary(ctx, snan, one, InputEnvironment{})
	if err != nil || computed.float4RawValid || !math.IsNaN(computed.floating) {
		t.Fatal("arithmetic did not create fresh NaN", computed, err)
	}
	if bits, err := computed.Float4Bits(); err != nil || bits&0x00400000 == 0 {
		t.Fatal("computed NaN reused signaling word", bits, err)
	}
	// Nested immutable constructors and remapping retain raw leaves; the core
	// literal renderer recursively consumes Float4Bits rather than NaN text.
	array := types[types[Float4].AssociatedArray()]
	v, err := ArrayValue(array, []ArrayDimension{{Lower: 1, Length: 1}}, []Value{snan})
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := v.Retag(foreign[array.id])
	if err != nil || mapped.array.elements[0].float4Bits() != 0x7f800001 {
		t.Fatal("array remapping lost raw leaf", err)
	}
	recordType := compositeForTest(t, 61000, CompositeMember{1, "raw", types[Float4].Usage()})
	record, err := CompositeValue(recordType, []Value{snan})
	if err != nil || record.CompositeField(0).float4Bits() != 0x7f800001 {
		t.Fatal("composite constructor lost raw leaf", err)
	}
	for _, value := range []Value{Null(types[Float4]), Value{}, Value{typ: types[Float8], floating: 1}} {
		if _, err := value.Float4Bits(); err == nil {
			t.Fatal("accepted non-REAL payload")
		}
	}
	if _, err := Float4FromBits(types[Float8], 0); err == nil {
		t.Fatal("accepted FLOAT8 constructor authority")
	}
}

func TestFloat4NeedsRawReplay(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[Float4]
	domain, err := Domain(61001, "real_word", "public.real_word", d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := d.Input(ctx, "NaN", -1)
	if err != nil {
		t.Fatal(err)
	}
	if bits, err := canonical.Float4Bits(); err != nil || bits != 0x7fc00000 || canonical.Float4NeedsRawReplay() {
		t.Fatal("canonical input NaN", bits, err)
	}
	for _, tc := range []struct {
		word uint32
		raw  bool
	}{
		{0, false}, {0x80000000, false},
		{1, false}, {0x007fffff, false}, {0x00800000, false},
		{0x3f800000, false}, {0xbf800000, false},
		{0x7f7fffff, false}, {0xff7fffff, false},
		{0x7f800000, false}, {0xff800000, false},
		{0x7fc00000, false}, {0xffc00000, true},
		{0x7fc00001, true}, {0xffc00001, true},
		{0x7f800001, true}, {0xff800001, true}, {0x7fffffff, true},
	} {
		v, err := Float4FromBits(d, tc.word)
		if err != nil || v.Float4NeedsRawReplay() != tc.raw {
			t.Fatalf("word %08x: raw=%v want %v: %v", tc.word, v.Float4NeedsRawReplay(), tc.raw, err)
		}
		wrapped, err := domain.Wrap(v)
		if err != nil || wrapped.Float4NeedsRawReplay() != tc.raw {
			t.Fatalf("domain word %08x: %v", tc.word, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		roundtrip, err := d.Input(ctx, literal[1:len(literal)-1], -1)
		if err != nil {
			t.Fatal(err)
		}
		word, err := roundtrip.Float4Bits()
		if err != nil || (word != tc.word) != tc.raw {
			t.Fatalf("word %08x canonical %s became %08x: %v", tc.word, literal, word, err)
		}
	}
	for _, v := range []Value{{}, Null(d), Null(domain), {typ: types[Float8], floating: math.NaN()}} {
		if v.Float4NeedsRawReplay() {
			t.Fatal("non-REAL or NULL requires raw replay")
		}
	}
}
