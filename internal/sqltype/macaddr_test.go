package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestMACADDRInputOutput(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Macaddr]
	for _, input := range []string{
		"08:00:2b:01:02:03", "08-00-2b-01-02-03", "08002b:010203",
		"08002b-010203", "0800.2b01.0203", "0800-2b01-0203", "08002b010203",
		"08:00:2B:01:02:03", "08002B010203",
	} {
		v, err := d.Input(ctx, input, -1)
		if err != nil {
			t.Fatal(input, err)
		}
		address, err := v.MACADDR()
		if err != nil || address != 0x08002b010203 {
			t.Fatal(input, address, err)
		}
		if out, err := d.Output(ctx, v); err != nil || out != "08:00:2b:01:02:03" {
			t.Fatal(input, out, err)
		}
		if literal, err := d.SQLLiteral(ctx, v); err != nil || literal != "MACADDR '08:00:2b:01:02:03'" {
			t.Fatal(literal, err)
		}
	}
	for _, tc := range []struct {
		address uint64
		text    string
	}{
		{0, "00:00:00:00:00:00"}, {0x800102030405, "80:01:02:03:04:05"}, {1<<48 - 1, "ff:ff:ff:ff:ff:ff"},
	} {
		v, err := MACADDRValue(d, tc.address)
		if err != nil {
			t.Fatal(err)
		}
		if out, err := d.Output(ctx, v); err != nil || out != tc.text {
			t.Fatal(out, err)
		}
	}
	if literal, err := d.SQLLiteral(ctx, Null(d)); err != nil || literal != "CAST(NULL AS MACADDR)" {
		t.Fatal(literal, err)
	}
}

func TestMACADDRScanner(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Macaddr]
	// Expectations independently observed on pinned PostgreSQL18.6, not built
	// with the native formatter or another native parser.
	valid := [][2]string{
		{"8:0:2B:1:2:3", "08:00:2b:01:02:03"},
		{"+08:+00:+2b:+01:+02:+03", "08:00:2b:01:02:03"},
		{"0x08:0x00:0x2b:0x01:0x02:0x03", "08:00:2b:01:02:03"},
		{"0008:0000:002b:0001:0002:0003", "08:00:2b:01:02:03"},
		{"-0:0:0:0:0:0", "00:00:00:00:00:00"},
		{"08002b01023", "08:00:2b:01:02:03"},
		{"8002b010203", "80:02:b0:10:20:03"},
		{"08002b0102+3", "08:00:2b:01:02:03"},
		{"08002b01020x", "08:00:2b:01:02:00"},
		{"0x002b010203", "00:00:2b:01:02:03"},
		{"0x:00:00:00:00:00", "00:00:00:00:00:00"},
		{"0x0x0x0x0x0x", "00:00:00:00:00:00"},
		{"+8002b010203", "08:00:2b:01:02:03"},
		{"08002b:01023", "08:00:2b:01:02:03"},
		{"-ffffffff:00:00:00:00:00", "01:00:00:00:00:00"},
		{"100000000:00:00:00:00:00", "00:00:00:00:00:00"},
		{"-100000000:00:00:00:00:00", "00:00:00:00:00:00"},
		{"100000001:00:00:00:00:00", "01:00:00:00:00:00"},
		{"-ffffffffffffffff:00:00:00:00:00", "01:00:00:00:00:00"},
	}
	for _, ws := range []string{" ", "\t", "\n", "\r", "\v", "\f"} {
		valid = append(valid, [2]string{ws + "08:00:2b:01:02:03" + ws, "08:00:2b:01:02:03"}, [2]string{"08:" + ws + "00:" + ws + "2b:" + ws + "01:" + ws + "02:" + ws + "03", "08:00:2b:01:02:03"})
	}
	for _, tc := range valid {
		v, err := d.Input(ctx, tc[0], -1)
		if err != nil {
			t.Fatal(tc[0], err)
		}
		if out, err := d.Output(ctx, v); err != nil || out != tc[1] {
			t.Fatal(tc, out, err)
		}
	}
	for _, tc := range []struct{ input, code string }{
		{"", "22P02"}, {"0800:2b01:0203", "22P02"}, {"not even close", "22P02"},
		{"08:00:2b:01:02:ZZ", "22P02"}, {"08:00:2b:01:02", "22P02"},
		{"08:00-2b:01:02:03", "22P02"}, {"08:00:2b:01:02:03:04", "22P02"},
		{"08:00:2b:01:02:03junk", "22P02"}, {"08:00:2b:01:02:03:04:05", "22P02"},
		{"100:00:2b:01:02:03", "22003"}, {"-1:00:2b:01:02:03", "22003"},
		{"08:00:2b:01:02:100", "22003"}, {"100:00:2b:01:02:03junk", "22P02"},
		{"08 :00:2b:01:02:03", "22P02"}, {"\u00a008:00:2b:01:02:03", "22P02"},
		{"08:00:2b:01:02:03\u00a0", "22P02"}, {"08002b0102", "22P02"},
		{"ffffffff:00:00:00:00:00", "22003"}, {"-100000001:00:00:00:00:00", "22003"},
		{"ffffffffffffffff:00:00:00:00:00", "22003"},
		{"10000000000000000:00:00:00:00:00", "22003"}, {"-10000000000000000:00:00:00:00:00", "22003"},
		{strings.Repeat("f", 100) + ":00:00:00:00:00", "22003"}, {"-" + strings.Repeat("f", 100) + ":00:00:00:00:00", "22003"},
	} {
		_, err := d.Input(ctx, tc.input, -1)
		message := `invalid input syntax for type macaddr: "` + tc.input + `"`
		if tc.code == "22003" {
			message = `invalid octet value in "macaddr" value: "` + tc.input + `"`
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != message || diagnostic.Position != "" || diagnostic.Context != "" || diagnostic.Detail != "" || diagnostic.Hint != "" {
			t.Fatalf("%q: %+v", tc.input, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Input(canceled, strings.Repeat("0", 8192)+":00:00:00:00:00", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestMACADDRComparison(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[Macaddr]
	comparison, err := BindOrdering(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	addresses := []uint64{0, 255, 256, 0xffffff, 0x1000000, 0x7fffffffffff, 0x800000000000, 0xffffffffffff}
	for i, a := range addresses {
		left, _ := MACADDRValue(d, a)
		for j, b := range addresses {
			right, _ := MACADDRValue(d, b)
			want := 0
			if i < j {
				want = -1
			}
			if i > j {
				want = 1
			}
			if got, err := comparison.Compare(ctx, left, right, InputEnvironment{}); err != nil || got != want {
				t.Fatal(a, b, got, want, err)
			}
		}
	}
	low, _ := MACADDRValue(d, 255)
	next, _ := MACADDRValue(d, 256)
	for op := CompareEqual; op <= CompareGreaterEqual; op++ {
		predicate, err := BindComparisonPredicate(op, d, d, types[Bool], CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		got, err := predicate.Evaluate(ctx, low, next, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		truth, err := got.Boolean()
		want := []bool{false, true, true, true, false, false}[op]
		if err != nil || truth != want {
			t.Fatal(op, truth, want, err)
		}
		for _, pair := range [][2]Value{{Null(d), low}, {low, Null(d)}} {
			got, err := predicate.Evaluate(ctx, pair[0], pair[1], InputEnvironment{})
			if err != nil || !got.IsNull() || got.Type() != types[Bool] {
				t.Fatal(op, got, err)
			}
		}
	}
	if eq, order := ComparisonCapabilities(d); !eq || !order {
		t.Fatal(eq, order)
	}
}

func TestMACADDRDatum(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[Macaddr]
	for _, owner := range []*Descriptor{nil, types[Oid]} {
		if _, err := MACADDRValue(owner, 0); err == nil {
			t.Fatal("invalid owner", owner)
		}
	}
	if _, err := MACADDRValue(d, 1<<48); err == nil {
		t.Fatal("oversized payload")
	}
	for _, v := range []Value{{}, Null(d), {typ: types[Oid]}, {typ: d, integer: -1}, {typ: d, integer: 1 << 48}} {
		if _, err := v.MACADDR(); err == nil {
			t.Fatal("invalid accessor", v)
		}
	}
	a, _ := d.Input(ctx, "08002b010203", -1)
	b, _ := d.Input(ctx, "08-00-2B-01-02-03", -1)
	if !SameDatum(a, b) || !SameDatum(Null(d), Null(d)) || SameDatum(a, Null(d)) || SameDatum(Value{typ: d, integer: 1 << 48}, Value{typ: d, integer: 1 << 48}) {
		t.Fatal("datum identity")
	}
	foreign, _ := MACADDRValue(testTypes()[Macaddr], 0)
	if _, err := d.Output(ctx, foreign); err == nil {
		t.Fatal("foreign output owner")
	}
	grouping, err := BindGrouping(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	ha, err := grouping.Hash(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := grouping.Hash(ctx, b)
	if err != nil || ha != hb {
		t.Fatal(ha, hb, err)
	}
	for _, pair := range [][2]ID{{Text, Macaddr}, {Macaddr, Text}} {
		if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Explicit); err != nil {
			t.Fatal(pair, err)
		}
	}
	for _, pair := range [][2]ID{{Int4, Macaddr}, {Macaddr, Int4}, {Bool, Macaddr}, {Macaddr, Oid}} {
		if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Explicit); err == nil {
			t.Fatal("unrelated cast", pair)
		}
	}
	if _, err := PlanCoercion(types[Text].Usage(), d.Usage(), Implicit); err == nil {
		t.Fatal("implicit text cast")
	}
}

func TestMACADDRDescriptor(t *testing.T) {
	types := testTypes()
	d := types[Macaddr]
	if d.ID() != 829 || !d.Concrete() || d.Name() != "macaddr" || d.SQLName() != "MACADDR" || d.DatabaseName() != "MACADDR" || d.AssociatedArray() != 1040 || d.Usage().Typmod != -1 || d.Usage().Collation != (CollationRef{}) {
		t.Fatal(d)
	}
	facts, ok := d.PGTypeMetadata()
	if !ok || facts.Length != 6 || facts.ByValue || facts.Alignment != "i" || facts.Storage != "p" || facts.Category != "U" || facts.Preferred || facts.Input != 436 || facts.Output != 437 || facts.Receive != 2494 || facts.Send != 2495 {
		t.Fatal(facts, ok)
	}
	array := types[1040]
	if array == nil || array.Concrete() || array.Element() != d {
		t.Fatal("array executable", array)
	}
	if equality, ordering := ComparisonCapabilities(array); equality || ordering {
		t.Fatal("array provider", equality, ordering)
	}
	if _, err := array.Input(context.Background(), "{}", -1); err == nil {
		t.Fatal("array codec")
	}
	for _, err := range []error{d.CheckBinaryInput(), d.CheckBinaryOutput()} {
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal("binary boundary", err)
		}
	}
}

func TestMACADDRArrayBoundary(t *testing.T) {
	d := testTypes()[Macaddr]
	domain, err := Domain(60001, "mac_domain", "public.mac_domain", d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		element *Descriptor
		id      ID
	}{{"scalar", d, 1040}, {"domain", domain, 60002}} {
		t.Run(tc.name, func(t *testing.T) {
			if array, err := Array(tc.id, "_mac_boundary", tc.element); err == nil || array != nil {
				t.Fatal("MACADDR element acquired executable array authority", array, err)
			}
		})
	}
}
