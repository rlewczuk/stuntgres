package sqltype

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func binaryUnitTypes() map[ID]*Descriptor {
	m := make(map[ID]*Descriptor)
	for _, d := range Builtins() {
		m[d.id] = d
	}
	return m
}
func binaryUnitHex(t *testing.T, s string) []byte {
	t.Helper()
	p, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func binaryUnitCase(t *testing.T, d *Descriptor, input, want string) {
	t.Helper()
	p := binaryUnitHex(t, input)
	v, err := d.BinaryInputWithEnvironment(context.Background(), p, -1, InputEnvironment{})
	if err != nil {
		t.Fatal(d.name, err)
	}
	if v.typ != d || v.null {
		t.Fatal("binary descriptor identity", v)
	}
	if d.id == Regclass {
		text, err := d.Input(context.Background(), v.text, -1)
		if err != nil || v.integer != text.integer || v.oid != text.oid || !SameDatum(v, text) {
			t.Fatal("binary regclass lost canonical identity", v, text, err)
		}
	}
	// A nonempty prefix also proves appending preserves caller-owned scratch.
	out, err := d.AppendBinaryWithEnvironment(context.Background(), []byte{7, 9}, v, OutputEnvironment{})
	expected := append([]byte{7, 9}, binaryUnitHex(t, want)...)
	if err != nil || !bytes.Equal(out, expected) {
		t.Fatalf("%s output=%x expected=%x err=%v", d.name, out, expected, err)
	}
	if len(p) > 0 {
		before := v
		p[0] ^= 255
		again, err := d.AppendBinaryWithEnvironment(context.Background(), nil, before, OutputEnvironment{})
		if err != nil || !bytes.Equal(again, expected[2:]) {
			t.Fatal("value borrowed mutable input", d.name, err)
		}
	}
}
func TestBinaryScalar(t *testing.T) {
	types := binaryUnitTypes()
	for _, tc := range []struct {
		id          ID
		input, want string
	}{{Bool, "02", "01"}, {Char, "00", "00"}, {Char, "ff", "ff"}, {Int2, "fffe", "fffe"}, {Int4, "0000002a", "0000002a"}, {Int8, "ffffffffffffffff", "ffffffffffffffff"}, {Oid, "ffffffff", "ffffffff"}, {Xid, "0000002a", "0000002a"}, {Regtype, "00000017", "00000017"}, {Regclass, "0000002a", "0000002a"}, {Float4, "80000000", "80000000"}, {Float8, "7ff0000000000000", "7ff0000000000000"}, {Money, "00000000000004d2", "00000000000004d2"}, {UUID, "00112233445566778899aabbccddeeff", "00112233445566778899aabbccddeeff"}, {Bytea, "00ff5c", "00ff5c"}, {Text, "6869", "6869"}, {Name, "6162", "6162"}} {
		t.Run(tc.input+"/"+types[tc.id].name, func(t *testing.T) { binaryUnitCase(t, types[tc.id], tc.input, tc.want) })
	}
	for _, tc := range []struct {
		id                  ID
		wire, code, message string
	}{
		{Int4, "00", "08P01", "insufficient data left in message"},
		{Char, "", "08P01", "no data left in message"},
		{Char, "0000", "22P03", "incorrect binary data format"},
		{Xid, "00", "08P01", "insufficient data left in message"},
		{Xid, "0000002a00", "22P03", "incorrect binary data format"},
	} {
		t.Run(types[tc.id].name+"/"+tc.wire+"/diagnostic", func(t *testing.T) {
			_, err := types[tc.id].BinaryInputWithEnvironment(context.Background(), binaryUnitHex(t, tc.wire), -1, InputEnvironment{})
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(err)
			}
		})
	}
}
func TestBinaryTemporal(t *testing.T) {
	types := binaryUnitTypes()
	for _, tc := range []struct {
		id   ID
		wire string
	}{{Date, "ffffffff"}, {Date, "7fffffff"}, {Time, "000000141dd76000"}, {Timetz, "0000000000000000ffffe3e0"}, {Timestamp, "ffffffffffffffff"}, {Timestamptz, "7fffffffffffffff"}, {Interval, "00000000000000010000000200000003"}} {
		t.Run(types[tc.id].name+"/"+tc.wire, func(t *testing.T) { binaryUnitCase(t, types[tc.id], tc.wire, tc.wire) })
	}
	for _, wire := range []string{"00000000000000000000e0ff", "0000000000000000ffff1f01"} {
		binaryUnitCase(t, types[Timetz], wire, wire)
	}
	for _, tc := range []struct{ wire, code, message string }{
		{"00000000000000000000e100", "22009", "time zone displacement out of range"},
		{"0000000000000000ffff1f00", "22009", "time zone displacement out of range"},
		{"000000141dd76001", "22008", "time out of range"},
		{"000000141dd760010000e100", "22008", "time out of range"},
	} {
		t.Run("timetz/"+tc.wire+"/diagnostic", func(t *testing.T) {
			_, err := types[Timetz].BinaryInputWithEnvironment(context.Background(), binaryUnitHex(t, tc.wire), -1, InputEnvironment{})
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(err)
			}
		})
	}
}
func TestBinaryStructured(t *testing.T) {
	types := binaryUnitTypes()
	for _, tc := range []struct {
		id          ID
		input, want string
	}{{Numeric, "0002000000000002000c0d48", "0002000000000002000c0d48"}, {Numeric, "0002000000000000000c0d48", "0001000000000000000c"}, {Numeric, "00000000d0000000", "00000000d0000020"}, {JSON, "7b2261223a317d", "7b2261223a317d"}, {JSONB, "017b2261223a317d", "017b2261223a20317d"}, {JSONPath, "01242e61", "01242e226122"}, {Bit, "00000003bf", "00000003a0"}, {Inet, "02180104c0000201", "02180004c0000201"}, {Cidr, "02180004c0000200", "02180104c0000200"}} {
		t.Run(types[tc.id].name+"/"+tc.input, func(t *testing.T) { binaryUnitCase(t, types[tc.id], tc.input, tc.want) })
	}
	t.Run("SpecialNativeInvariant", binaryReviewSpecialNative)
}
func TestBinaryNested(t *testing.T) {
	types := binaryUnitTypes()
	a, err := Array(50000, "_owned", types[Int4])
	if err != nil {
		t.Fatal(err)
	}
	binaryUnitCase(t, a, "0000000100000000000000170000000100000001ffffffff", "0000000100000001000000170000000100000001ffffffff")
	c, err := Composite(50001, "pair", "public.pair", 0, []CompositeMember{{Attribute: 1, Name: "i", Usage: types[Int4].Usage()}, {Attribute: 2, Name: "t", Usage: types[Text].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	binaryUnitCase(t, c, "0000000200000017000000040000002a00000019000000026869", "0000000200000017000000040000002a00000019000000026869")
	binaryUnitCase(t, types[Int4Range], "0200000004000000010000000400000003", "0200000004000000010000000400000003")
	binaryUnitCase(t, types[Int4Multirange], "00000001000000110200000004000000010000000400000003", "00000001000000110200000004000000010000000400000003")
	domain, err := Domain(50002, "positive", "public.positive", types[Int4].Usage())
	if err != nil {
		t.Fatal(err)
	}
	binaryUnitCase(t, domain, "0000002a", "0000002a")
	// Enum arrays have a real descriptor codec even though table DDL excludes
	// them. User OIDs are advisory on receive; send uses the owned descriptor.
	mood, err := Enum(50003, "mood", "public.mood", []string{"happy"})
	if err != nil {
		t.Fatal(err)
	}
	moods, err := Array(50004, "_mood", mood)
	if err != nil {
		t.Fatal(err)
	}
	binaryUnitCase(t, moods, "00000001000000000000c3530000000200000001000000056861707079ffffffff", "00000001000000010000c3530000000200000001000000056861707079ffffffff")
	binaryUnitCase(t, moods, "00000001000000000000c3550000000100000001000000056861707079", "00000001000000000000c3530000000100000001000000056861707079")
	t.Run("ReviewNestedRegressions", binaryReviewNestedRegressions)
}

func binaryReviewSpecialNative(t *testing.T) {
	types := binaryUnitTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		input, canonical, text string
		special                uint8
	}{
		{"00000000d0000020", "00000000d0000020", "Infinity", numericInfinity},
		{"00000000f0000020", "00000000f0000020", "-Infinity", numericNegativeInfinity},
		{"00000000c0000020", "00000000c0000000", "NaN", numericNaN},
	} {
		t.Run(tc.text, func(t *testing.T) {
			v, err := types[Numeric].BinaryInputWithEnvironment(ctx, binaryUnitHex(t, tc.input), -1, InputEnvironment{})
			if err != nil || v.special != tc.special || v.scale != 0 || v.exponent != 0 || v.negative || v.text != "" {
				t.Fatal("special native invariant", v, err)
			}
			binaryUnitCase(t, types[Numeric], tc.input, tc.canonical)
			rangeWire := "1200000008" + tc.canonical
			for _, nested := range []struct {
				d          *Descriptor
				wire, text string
			}{
				{types[NumRange], rangeWire, "[" + tc.text + ",)"},
				{types[NumMultirange], "000000010000000d" + rangeWire, "{[" + tc.text + ",)}"},
			} {
				actual, err := nested.d.BinaryInputWithEnvironment(ctx, binaryUnitHex(t, nested.wire), -1, InputEnvironment{})
				if err != nil {
					t.Fatal(err)
				}
				textual, err := nested.d.Input(ctx, nested.text, -1)
				if err != nil {
					t.Fatal(err)
				}
				if nested.d.id == NumRange {
					if !actual.validRange() || actual.rangeDatum != textual.rangeDatum {
						t.Fatal("binary/text range identity", actual)
					}
				} else if !actual.validMultirange() || len(actual.multirange.members) != 1 || actual.multirange.members[0] != textual.multirange.members[0] {
					t.Fatal("binary/text multirange identity", actual)
				}
				out, err := nested.d.Output(ctx, actual)
				if err != nil || out != nested.text {
					t.Fatal(out, err)
				}
				binaryUnitCase(t, nested.d, nested.wire, nested.wire)
			}
		})
	}
}

func binaryReviewNestedRegressions(t *testing.T) {
	types := binaryUnitTypes()
	record, err := Composite(50010, "one", "public.one", 0, []CompositeMember{{Attribute: 1, Name: "i", Usage: types[Int4].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                string
		d                   *Descriptor
		wire, code, message string
	}{
		{"array_utf8_before_missing", types[ID(1009)], "000000010000000000000019000000020000000100000001ff", "22021", "invalid byte sequence for encoding \"UTF8\": 0xff"},
		{"array_valid_before_missing", types[ID(1009)], "00000001000000000000001900000002000000010000000161", "08P01", "insufficient data left in message"},
		{"array_declared_truncated", types[ID(1007)], "00000001000000000000001700000001000000010000000400", "22P03", "insufficient data left in message"},
		{"record_declared_truncated", record, "00000001000000170000000400", "22P03", "insufficient data left in message"},
		{"array_incomplete_length", types[ID(1007)], "0000000100000000000000170000000100000001000000", "08P01", "insufficient data left in message"},
		{"record_incomplete_length", record, "0000000100000017000000", "08P01", "insufficient data left in message"},
		{"array_complete_short_child", types[ID(1007)], "00000001000000000000001700000001000000010000000100", "08P01", "insufficient data left in message"},
		{"record_complete_short_child", record, "00000001000000170000000100", "08P01", "insufficient data left in message"},
		{"range_underflow_unchanged", types[Int4Range], "020000000400", "08P01", "insufficient data left in message"},
		{"multirange_underflow_unchanged", types[Int4Multirange], "000000010000000402", "08P01", "insufficient data left in message"},
		{"multirange_reversed_before_missing", types[Int4Multirange], "00000006000000110200000004000000020000000400000001", "22000", "range lower bound must be less than or equal to range upper bound"},
		{"multirange_empty_input_before_missing", types[Int4Multirange], "0000000200000000", "08P01", "no data left in message"},
		{"multirange_negative_count_unchanged", types[Int4Multirange], "ffffffff", "XX000", "invalid memory alloc request size 34359738360"},
		{"multirange_oversized_count_unchanged", types[Int4Multirange], "08000000", "XX000", "invalid memory alloc request size 1073741824"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.d.BinaryInputWithEnvironment(context.Background(), binaryUnitHex(t, tc.wire), -1, InputEnvironment{})
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(err)
			}
		})
	}
	t.Run("AnnouncedCardinalityDoesNotAllocateElements", func(t *testing.T) {
		small := binaryUnitHex(t, "0000000100000000000000170000000100000001")
		large := binaryUnitHex(t, "00000001000000000000001707ffffff00000001")
		allocations := func(payload []byte) float64 {
			return testing.AllocsPerRun(5, func() {
				_, err := types[ID(1007)].BinaryInputWithEnvironment(context.Background(), payload, -1, InputEnvironment{})
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "08P01" {
					t.Fatal(err)
				}
			})
		}
		if smallAllocs, largeAllocs := allocations(small), allocations(large); largeAllocs > smallAllocs {
			t.Fatal("allocation grew with absent announced elements", smallAllocs, largeAllocs)
		}
	})
	t.Run("MultirangeFailedFirstMemberDoesNotAllocateStorage", func(t *testing.T) {
		ctx := context.Background()
		env := InputEnvironment{}
		member := binaryUnitHex(t, "0200000004000000020000000400000001")
		// Bound the test's announced capacity even if eager allocation returns.
		container := binaryUnitHex(t, "00001000000000110200000004000000020000000400000001")
		allocations := func(receive func() error) float64 {
			return testing.AllocsPerRun(5, func() {
				err := receive()
				var diagnostic *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22000", Message: "range lower bound must be less than or equal to range upper bound"}
				if !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatal(err)
				}
			})
		}
		memberAllocs := allocations(func() error { _, _, err := types[Int4Range].binaryRange(ctx, member, env); return err })
		containerAllocs := allocations(func() error { _, _, err := types[Int4Multirange].binaryMultirange(ctx, container, env); return err })
		if containerAllocs > memberAllocs {
			t.Fatal("multirange allocated storage before first successful member", memberAllocs, containerAllocs)
		}
	})
	for _, tc := range []struct {
		expected ID
		supplied uint32
		column   int
		message  string
	}{
		{VarChar, 1114, 0, "binary data has array element type 1114 (timestamp without time zone) instead of expected 1043 (character varying)"},
		{Timestamp, 1043, 1, "binary data has type 1043 (character varying) instead of expected 1114 (timestamp without time zone) in record column 1"},
		{Timetz, 1083, 1, "binary data has type 1083 (time without time zone) instead of expected 1266 (time with time zone) in record column 1"},
		{ID(1007), 1009, 1, "binary data has type 1009 (text[]) instead of expected 1007 (integer[]) in record column 1"},
		{Int4, 9999, 0, "binary data has array element type 9999 (9999) instead of expected 23 (integer)"},
	} {
		err := binaryOIDMismatch(types[tc.expected], tc.supplied, tc.column)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: tc.message}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal(err)
		}
	}
	if err := binaryOIDMismatch(types[Int4], 50010, 0); err != nil {
		t.Fatal("user OID permissiveness changed", err)
	}
}
