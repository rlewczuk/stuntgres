package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestCopyFilterProviderProvenance(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, tc := range []struct {
		id         ID
		text, code string
		typmod     int32
		soft       bool
	}{
		{1007, "[1:0]={}", "2202E", -1, true},
		{1007, "[2147483648:2147483648]={1}", "54000", -1, true},
		{1007, "[-2147483648:0]={}", "54000", -1, true},
		{Int4Range, "[3,1)", "22000", -1, true},
		{JSONPath, "last", "42601", -1, true},
		{JSONPath, `$ ? (@ like_regex "(")`, "2201B", -1, true},
		{JSONPath, `$ ? (@ like_regex "a" flag "x")`, "0A000", -1, true},
		{JSONPath, "1e131072", "22003", -1, false},
		{JSONPath, "$.**{2147483648}", "22003", -1, false},
		{Bit, "1", "22026", 2, true},
		{JSONB, `"\u0000"`, "22P05", -1, true},
		{TSVector, "a:0", "42601", -1, true},
		{TSQuery, "a &", "42601", -1, true},
		{Line, "{1e400,2,3}", "22003", -1, true},
		{Line, "(1e308,1e308),(-1e308,-1e308)", "22003", -1, false},
		{Numeric, "1e131072", "22003", -1, true},
	} {
		_, err := types[tc.id].InputWithEnvironment(ctx, tc.text, tc.typmod, InputEnvironment{})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code || InputSoftError(err) != tc.soft {
			t.Fatal(tc.id, tc.text, err, tc.soft)
		}
	}
	// The same width validator's assignment and binary callers are not text
	// input-save contexts. Their diagnostics must not gain soft provenance.
	bit := types[Bit]
	for _, err := range []error{
		checkBitWidth(Bit, 1, 2),
		func() error { _, err := sizeBitString(Value{typ: bit, text: "1"}, 2); return err }(),
		func() error {
			_, err := bit.BinaryInputWithEnvironment(ctx, []byte{0, 0, 0, 1, 0x80}, 2, InputEnvironment{})
			return err
		}(),
	} {
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22026" || InputSoftError(err) {
			t.Fatal("nontext bit authority", err)
		}
	}
	// The shared range canonicalizer also serves binary/operator callers.
	// These actual binary bounds are lower3/upper1, not text input.
	_, err := types[Int4Range].BinaryInputWithEnvironment(ctx, []byte{2, 0, 0, 0, 4, 0, 0, 0, 3, 0, 0, 0, 4, 0, 0, 0, 1}, -1, InputEnvironment{})
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22000" || InputSoftError(err) {
		t.Fatal("binary range gained text-save authority", err)
	}
	_, err = CompilePGRegex(ctx, "(", PGRegexOptions{}, CollationRef{ID: 100, Derivation: Default})
	if !errors.As(err, &d) || d.Code != "2201B" || InputSoftError(err) {
		t.Fatal("SQL regex gained JSONPath input authority", err)
	}
	if _, err := types[JSON].Input(ctx, `"\u0000"`, -1); err != nil {
		t.Fatal("non-decoding JSON gained JSONB validation", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := types[JSONPath].Input(canceled, "last", -1); err != context.Canceled {
		t.Fatal("cancellation changed identity", err)
	}
}

func TestCopyFilterArrayInputDemand(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	array := types[1007]
	for _, tc := range []struct {
		name, input, code, message, detail string
	}{
		{"child_before_extent", "[1:50000][1:50000]={{bad}}", "22P02", `invalid input syntax for type integer: "bad"`, ""},
		{"valid_child_then_extent", "[1:50000][1:50000]={{1}}", "22P02", `malformed array literal: "[1:50000][1:50000]={{1}}"`, "Specified array dimensions do not match array contents."},
		{"declared_rank_before_child", "[1:50000][1:50000]" + strings.Repeat("[1:1]", 5) + "={{bad}}", "54000", "number of array dimensions exceeds the maximum allowed (6)", ""},
		{"declared_zero_before_child", "[1:50000][1:50000][1:0]={{bad}}", "2202E", "upper bound cannot be less than lower bound", ""},
		{"declared_upper_before_child", "[1:50000][1:50000][2147483647:2147483647]={{bad}}", "54000", "array upper bound is too large: 2147483647", ""},
		{"declared_length_before_child", "[1:50000][1:50000][-2147483648:0]={{bad}}", "54000", "array size exceeds the maximum allowed (134217727)", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := array.InputWithEnvironment(ctx, tc.input, -1, InputEnvironment{})
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail}
			if value != (Value{}) || !errors.As(err, &diagnostic) || *diagnostic != want || !InputSoftError(err) {
				t.Fatal("array input demanded the wrong producer or changed its diagnostic", value, err)
			}
		})
	}
	// Common builders still reject the declared product using only a tiny
	// actual payload. Their size error is not newly marked as text input.
	element, err := Integer(types[Int4], 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, constructor := range []struct {
		name  string
		build func(*Descriptor, []ArrayDimension, []Value) (Value, error)
	}{{"copied", ArrayValue}, {"owned", OwnedArrayValue}} {
		t.Run("construction_"+constructor.name, func(t *testing.T) {
			value, err := constructor.build(array, []ArrayDimension{{Lower: 1, Length: 50000}, {Lower: 1, Length: 50000}}, []Value{element})
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54000", Message: "array size exceeds the maximum allowed"}
			if value != (Value{}) || !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal("final checked construction lost its product guard", value, err)
			}
			if _, marked := err.(*sqlstate.InputError); marked {
				t.Fatal("common construction gained text-input provenance", err)
			}
			value, err = constructor.build(array, []ArrayDimension{{Lower: 1, Length: 2}}, []Value{element})
			if value != (Value{}) || err == nil || err.Error() != "array shape does not match elements" || errors.As(err, &diagnostic) {
				t.Fatal("final checked construction lost its actual-element count guard", value, err)
			}
		})
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if value, err := array.InputWithEnvironment(canceled, "[1:50000][1:50000]={{bad}}", -1, InputEnvironment{}); value != (Value{}) || err != context.Canceled {
		t.Fatal("array input changed cancellation identity", value, err)
	}
	value, err := array.InputWithEnvironment(ctx, "[2:2][3:3]={{7}}", -1, InputEnvironment{})
	if err != nil || value.Type() != array || value.ArrayRank() != 2 || value.ArrayLen() != 1 ||
		value.ArrayDimension(0) != (ArrayDimension{Lower: 2, Length: 1}) || value.ArrayDimension(1) != (ArrayDimension{Lower: 3, Length: 1}) ||
		value.ArrayElement(0).Type() != types[Int4] || value.ArrayElement(0).integer != 7 {
		t.Fatal("fresh valid input lost element authority or dimensions after failures/cancellation", value, err)
	}
	text, err := array.Output(ctx, value)
	if err != nil || text != "[2:2][3:3]={{7}}" {
		t.Fatal("fresh valid array output changed", text, err)
	}
}
