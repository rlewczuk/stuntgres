package enginetests

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func segType(t *testing.T, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	d, e := sqltype.Seg(id, "seg", "public.seg")
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func segInput(t *testing.T, d *sqltype.Descriptor, s string) sqltype.Value {
	t.Helper()
	v, e := d.Input(t.Context(), s, -1)
	if e != nil {
		t.Fatal(s, e)
	}
	return v
}

func TestSegDescriptor(t *testing.T) {
	d := segType(t, 65000)
	if !d.IsSeg() || !d.IsUserDefined() || !d.Concrete() || d.DatabaseName() != "SEG" || d.IsBuiltin() {
		t.Fatal("descriptor identity")
	}
	m, ok := d.PGTypeMetadata()
	if !ok || m.Length != 12 || m.ByValue || m.Alignment != "i" || m.Storage != "p" || m.Kind != "b" || m.Category != "U" || m.Modifier != -1 || m.Collation != 0 {
		t.Fatal("metadata", m)
	}
	for _, tc := range []struct {
		id         sqltype.ID
		name, decl string
	}{{0, "seg", "seg"}, {sqltype.Unknown, "seg", "seg"}, {65000, "", "seg"}, {65000, "seg", ""}} {
		if _, e := sqltype.Seg(tc.id, tc.name, tc.decl); e == nil {
			t.Fatal("invalid descriptor")
		}
	}
	var textType *sqltype.Descriptor
	for _, builtin := range sqltype.Builtins() {
		if builtin.ID() == sqltype.Text {
			textType = builtin
			break
		}
	}
	for _, v := range []sqltype.Value{{}, sqltype.Null(d), sqltype.String(textType, "1")} {
		if _, e := v.SegDatum(); e == nil {
			t.Fatal("invalid accessor admitted")
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("string constructor admitted seg descriptor")
			}
		}()
		sqltype.String(d, "1")
	}()
	if _, e := sqltype.Array(65001, "_seg", d); e == nil {
		t.Fatal("executable array admitted")
	}
	if eq, ord := sqltype.ComparisonCapabilities(d); eq || ord {
		t.Fatal("storage grants advanced comparison")
	}
}

func TestSegInput(t *testing.T) {
	d := segType(t, 65000)
	for _, tc := range []struct {
		in   string
		want sqltype.SegDatum
	}{
		// Exact rational rounding at exponent3 gives significand0xc587e7;
		// reinput of six-digit canonical12.3457 has a different binary32 word.
		{"12.34567890123456", sqltype.SegDatum{LowerBits: 0x414587e7, UpperBits: 0x414587e7, LowerDigits: 6, UpperDigits: 6}},
		{"1.0 .. 1.00", sqltype.SegDatum{LowerBits: 0x3f800000, UpperBits: 0x3f800000, LowerDigits: 2, UpperDigits: 3}},
		{"<1.5 .. ~2.50", sqltype.SegDatum{LowerBits: 0x3fc00000, UpperBits: 0x40200000, LowerDigits: 2, UpperDigits: 3, LowerMarker: '<', UpperMarker: '~'}},
		{"0...", sqltype.SegDatum{LowerBits: 0, UpperBits: 0x7f800000, LowerDigits: 1, UpperMarker: '-'}},
		{"...0", sqltype.SegDatum{LowerBits: 0xff800000, UpperBits: 0, UpperDigits: 1, LowerMarker: '-'}},
		{"-0.00", sqltype.SegDatum{LowerBits: 0x80000000, UpperBits: 0x80000000, LowerDigits: 3, UpperDigits: 3}},
		{"1.0(+-)0.005", sqltype.SegDatum{LowerBits: 0x3f7eb852, UpperBits: 0x3f80a3d7, LowerDigits: 3, UpperDigits: 4}},
		{"0(+-)-1", sqltype.SegDatum{LowerBits: 0x3f800000, UpperBits: 0xbf800000, LowerDigits: 1, UpperDigits: 1}},
		{"3.4e38(+-)3.4e38", sqltype.SegDatum{LowerBits: 0, UpperBits: 0x7f800000, LowerDigits: 2, UpperDigits: 2}},
	} {
		v := segInput(t, d, tc.in)
		got, e := v.SegDatum()
		if e != nil || got != tc.want {
			t.Fatalf("%s datum %#v want %#v (%v)", tc.in, got, tc.want, e)
		}
	}
	for _, tc := range []struct{ in, code, message, detail string }{
		{"", "42601", "bad seg representation", "syntax error at end of input"},
		{"ABC", "42601", "bad seg representation", `syntax error at or near "A"`},
		{"1.", "42601", "bad seg representation", `syntax error at or near "."`},
		{"1.....", "42601", "bad seg representation", `syntax error at or near ".."`},
		{"1e700", "22003", `"1e700" is out of range for type real`, ""},
		{"5 .. 2", "22023", "swapped boundaries: 5 is greater than 2", ""},
	} {
		_, e := d.Input(t.Context(), tc.in, -1)
		var se *sqlstate.Error
		if !errors.As(e, &se) || se.Code != tc.code || se.Message != tc.message || se.Detail != tc.detail {
			t.Fatalf("%q: %#v", tc.in, e)
		}
	}
	for _, s := range []string{"NaN", "Infinity", ".1", "1.", "..", "[1,2]", "1(+-)~2", "1e", "1 .. 2 junk"} {
		if _, e := d.Input(t.Context(), s, -1); e == nil {
			t.Fatal("malformed accepted", s)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e := d.Input(ctx, "1", -1); !errors.Is(e, context.Canceled) {
		t.Fatal("canceled input", e)
	}
}

func TestSegDatum(t *testing.T) {
	d, dest := segType(t, 65000), segType(t, 65001)
	a, b := segInput(t, d, "1.0 .. 1.00"), segInput(t, d, "1.0")
	if sqltype.SameDatum(a, b) {
		t.Fatal("upper precision discarded")
	}
	if same, e := a.SamePayload(t.Context(), b); e != nil || same {
		t.Fatal("payload precision", same, e)
	}
	z, nz := segInput(t, d, "0"), segInput(t, d, "-0")
	if sqltype.SameDatum(z, nz) {
		t.Fatal("signed zero discarded")
	}
	for _, v := range []sqltype.Value{a, sqltype.Null(d)} {
		m, e := v.RemapSeg(dest)
		if e != nil || m.Type() != dest || m.IsNull() != v.IsNull() {
			t.Fatal("remap", e)
		}
		back, e := m.RemapSeg(d)
		if e != nil || !sqltype.SameDatum(v, back) {
			t.Fatal("exact remap", e)
		}
		if _, e := v.Retag(dest); e == nil {
			t.Fatal("retag changed OID")
		}
	}
	foreign, _ := sqltype.QueryInt(d.ID(), "seg", "public.seg")
	if _, e := a.Retag(foreign); e == nil {
		t.Fatal("foreign family retag")
	}
	for _, s := range []sqltype.SegDatum{{}, {LowerBits: math.Float32bits(float32(math.NaN())), UpperBits: 0, LowerDigits: 1, UpperDigits: 1}, {LowerBits: 0, UpperBits: 0, LowerDigits: 7, UpperDigits: 1}, {LowerBits: 0, UpperBits: 0, LowerDigits: 1, UpperDigits: 1, LowerMarker: '-'}, {LowerBits: 0, UpperBits: 0, LowerDigits: 1, UpperDigits: 1, UpperMarker: 'x'}} {
		if _, e := sqltype.SegValue(d, s); e == nil {
			t.Fatal("invalid raw payload", s)
		}
	}
}

func TestSegCoercion(t *testing.T) {
	d := segType(t, 65000)
	var text *sqltype.Descriptor
	for _, b := range sqltype.Builtins() {
		if b.ID() == sqltype.Text {
			text = b
		}
	}
	v := sqltype.String(text, "~1.00")
	p, e := sqltype.PlanCoercion(text.Usage(), d.Usage(), sqltype.Explicit)
	if e != nil {
		t.Fatal(e)
	}
	got, e := p.Apply(t.Context(), v, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if e != nil || got.Type() != d {
		t.Fatal(e)
	}
	p, e = sqltype.PlanCoercion(d.Usage(), text.Usage(), sqltype.Explicit)
	if e != nil {
		t.Fatal(e)
	}
	out, e := p.Apply(t.Context(), got, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if e != nil {
		t.Fatal(e)
	}
	s, e := text.Output(t.Context(), out)
	if e != nil || s != "~1.00" {
		t.Fatal(s, e)
	}
}

func TestSegDerivedPayload(t *testing.T) {
	d := segType(t, 65000)
	for _, tc := range []struct{ in, out string }{
		{"0(+-)-1", "1 .. -1"},
		{"3.4e38(+-)3.4e38", "0.0 .. Infinity"},
		{"-3.4e38(+-)3.4e38", "-Infinity .. 0.0"},
		{"3.4e38(+-)-3.4e38", "Infinity .. 0.0"},
		{"-3.4e38(+-)-3.4e38", "0.0 .. -Infinity"},
	} {
		v := segInput(t, d, tc.in)
		payload, e := v.SegDatum()
		if e != nil {
			t.Fatal(e)
		}
		if payload.Reinputtable() {
			t.Fatal("derived payload marked reinputtable")
		}
		out, e := d.Output(t.Context(), v)
		if e != nil || out != tc.out {
			t.Fatal(out, e)
		}
		if _, e := d.Input(t.Context(), out, -1); e == nil {
			t.Fatal("stock non-reinputtable output accepted")
		}
	}
	for _, s := range []string{"1", "<1 .. ~2", "0...", "...0"} {
		payload, e := segInput(t, d, s).SegDatum()
		if e != nil || !payload.Reinputtable() {
			t.Fatal("ordinary payload", s, e)
		}
	}
	for _, payload := range []sqltype.SegDatum{
		{LowerBits: 0xff800000, UpperBits: 0x7f800000, LowerDigits: 1, UpperDigits: 1},
		{LowerBits: 0, UpperBits: 0x7f800000, LowerDigits: 1, UpperDigits: 1, UpperMarker: '~'},
		{LowerBits: 0x3f800000, UpperBits: 0xbf800000, LowerDigits: 1, UpperDigits: 1, LowerMarker: '<'},
		{LowerBits: 0x7fc00000, UpperBits: 0x3f800000, LowerDigits: 1, UpperDigits: 1},
		{LowerBits: 0x7f800001, UpperBits: 0x3f800000, LowerDigits: 1, UpperDigits: 1},
		{LowerBits: 0x3f800000, UpperBits: 0xffc00001, LowerDigits: 1, UpperDigits: 1},
		{LowerBits: 0x7f800000, UpperBits: 0, UpperDigits: 1, LowerMarker: '-'},
		{LowerBits: 0xff800000, UpperBits: 0, LowerDigits: 1, UpperDigits: 1, LowerMarker: '-'},
		{LowerBits: 0xff800000, UpperBits: 0x7f800000, UpperDigits: 1, LowerMarker: '-'},
	} {
		if _, e := sqltype.SegValue(d, payload); e == nil {
			t.Fatal("impossible derived payload admitted")
		}
	}
}

func TestSegInputOwnership(t *testing.T) {
	d := segType(t, 65000)
	input := []byte("<1.50 .. ~2.500")
	value := segInput(t, d, string(input))
	before, e := value.SegDatum()
	if e != nil {
		t.Fatal(e)
	}
	for i := range input {
		input[i] = '0'
	}
	after, e := value.SegDatum()
	if e != nil || before != after {
		t.Fatal("borrowed input changed datum", e)
	}
	copied, e := value.Retag(d)
	if e != nil || !sqltype.SameDatum(value, copied) {
		t.Fatal("fixed datum copy", e)
	}
}
