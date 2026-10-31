package enginetests

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"unsafe"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func heapAttrsUnitRaw(t *testing.T, text string) string {
	t.Helper()
	bytes, err := hex.DecodeString(text)
	if err != nil {
		t.Fatal(err)
	}
	return string(bytes)
}

func heapAttrsUnitLayout(t *testing.T, mask, natts uint16, bits *string) sqltype.HeapTupleLayout {
	t.Helper()
	layout, err := sqltype.ReadHeapTupleLayout(mask, natts, bits)
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func heapAttrsUnitError(t *testing.T, err error, code, message string) {
	t.Helper()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message || diagnostic.Detail != "" || diagnostic.Hint != "" || diagnostic.Position != "" || diagnostic.Context != "" {
		t.Fatal("heap diagnostic", err, code, message)
	}
}

func TestPageinspectHeapAttrsPhysical(t *testing.T) {
	types := brinUnitTypes()
	columns := []sqltype.HeapStoredColumn{{Length: 2, Alignment: 2}, {Length: 8, Alignment: 8}, {Length: 1, Alignment: 1}, {Length: 4, Alignment: 4}}
	raw := heapAttrsUnitRaw(t, "11220000000000000102030405060708ff000000aabbccdd")
	value, err := heapAttrsUnitLayout(t, 0, 4, nil).Split(t.Context(), raw, false, columns, types[1001])
	if err != nil {
		t.Fatal(err)
	}
	for i, expected := range []string{"1122", "0102030405060708", "ff", "aabbccdd"} {
		data, err := value.ArrayElement(i).Binary()
		if err != nil || data != heapAttrsUnitRaw(t, expected) {
			t.Fatal(i, data, err)
		}
	}
	first, _ := value.ArrayElement(0).Binary()
	if unsafe.StringData(first) != unsafe.StringData(raw) {
		t.Fatal("physical bytes were needlessly copied")
	}
	columns[0].Length = 8
	if again, _ := value.ArrayElement(0).Binary(); again != first {
		t.Fatal("schema scratch mutated accepted value")
	}
	bits := "10000000"
	value, err = heapAttrsUnitLayout(t, 1, 2, &bits).Split(t.Context(), heapAttrsUnitRaw(t, "01020304"), false, []sqltype.HeapStoredColumn{{Length: 4, Alignment: 4}, {Length: 8, Alignment: 8}, {Length: 4, Alignment: 4}}, types[1001])
	if err != nil || value.ArrayLen() != 3 || !value.ArrayElement(1).IsNull() || !value.ArrayElement(2).IsNull() {
		t.Fatal("physical/trailing NULL slots", value, err)
	}
	value, err = heapAttrsUnitLayout(t, 0, 0, nil).Split(t.Context(), "", false, nil, types[1001])
	if err != nil || value.IsNull() || value.ArrayLen() != 0 || len(value.ArrayDimensions()) != 0 {
		t.Fatal("nonnull zero-dimensional empty array", value, err)
	}
}

func TestPageinspectHeapAttrsDetoast(t *testing.T) {
	array := brinUnitTypes()[1001]
	column := []sqltype.HeapStoredColumn{{Length: -1, Alignment: 4}}
	for _, tc := range []struct{ raw, payload string }{
		{"076162", "6162"}, {"180000006162", "6162"}, {"320000000300000000616263", "616263"}, {"320000000300004030616263", "616263"}, {"03", ""}, {"10000000", ""},
	} {
		for _, detoast := range []bool{false, true} {
			want := tc.raw
			if detoast {
				want = tc.payload
			}
			value, err := heapAttrsUnitLayout(t, 0, 1, nil).Split(t.Context(), heapAttrsUnitRaw(t, tc.raw), detoast, column, array)
			if err != nil {
				t.Fatal(tc.raw, detoast, err)
			}
			data, err := value.ArrayElement(0).Binary()
			if err != nil || value.ArrayElement(0).IsNull() || data != heapAttrsUnitRaw(t, want) {
				t.Fatal(tc.raw, detoast, data, err)
			}
		}
	}
	for _, tc := range []struct{ raw, code, message string }{
		{"260000000300000000", "XX001", "compressed pglz data is corrupt"},
		{"260000000300004030", "XX001", "compressed lz4 data is corrupt"},
		{"260000000300008000", "XX000", "invalid compression method id 2"},
	} {
		_, err := heapAttrsUnitLayout(t, 0, 1, nil).Split(t.Context(), heapAttrsUnitRaw(t, tc.raw), true, column, array)
		heapAttrsUnitError(t, err, tc.code, tc.message)
	}
}

func TestPageinspectHeapAttrsBoundaries(t *testing.T) {
	array := brinUnitTypes()[1001]
	fixed := []sqltype.HeapStoredColumn{{Length: 4, Alignment: 4}}
	for _, tc := range []struct {
		raw     string
		natts   uint16
		message string
	}{
		{"", 2, "number of attributes in tuple header is greater than number of attributes in tuple descriptor"},
		{"abc", 1, "unexpected end of tuple data"},
		{"abcde", 1, "end of tuple reached without looking at all its data"},
	} {
		_, err := heapAttrsUnitLayout(t, 0, tc.natts, nil).Split(t.Context(), tc.raw, false, fixed, array)
		heapAttrsUnitError(t, err, "XX001", tc.message)
	}
	for _, tc := range []struct {
		mask, natts uint16
		bits        *string
		message     string
	}{
		{1, 1, nil, "t_bits string must not be NULL"},
		{0, 1, new(""), "t_bits string is expected to be NULL, but instead it is 0 bytes long"},
		{1, 9, new("00000000"), "unexpected length of t_bits string: 8, expected 16"},
		{1, 1, new("é000000"), "invalid character \"é\" in t_bits string"},
	} {
		_, err := sqltype.ReadHeapTupleLayout(tc.mask, tc.natts, tc.bits)
		heapAttrsUnitError(t, err, "XX001", tc.message)
	}
	// The source uint16 length wraps, rather than imposing an invented page cap.
	_, err := heapAttrsUnitLayout(t, 0, 0, nil).Split(t.Context(), strings.Repeat("x", 65536), false, nil, array)
	if err != nil {
		t.Fatal("uint16 zero length", err)
	}
	_, err = heapAttrsUnitLayout(t, 0, 0, nil).Split(t.Context(), strings.Repeat("x", 65535), false, nil, array)
	heapAttrsUnitError(t, err, "XX001", "end of tuple reached without looking at all its data")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = heapAttrsUnitLayout(t, 0, 1, nil).Split(ctx, "abcd", false, fixed, array)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
}

func TestPageinspectHeapAttrsSafety(t *testing.T) {
	array := brinUnitTypes()[1001]
	column := []sqltype.HeapStoredColumn{{Length: -1, Alignment: 4}}
	for _, raw := range []string{"", "\x00", "\x01", "\x04\x00\x00\x00", "\x01\x01abcdefgh"} {
		_, err := heapAttrsUnitLayout(t, 0, 1, nil).Split(t.Context(), raw, true, column, array)
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) {
			t.Fatal("unsafe native layout invented source diagnostic", raw, err)
		}
	}
	pointer := heapAttrsUnitRaw(t, "0112070000000300000001000000ffffffff")
	_, err := heapAttrsUnitLayout(t, 0, 1, nil).Split(t.Context(), pointer, true, column, array)
	var reference *sqltype.HeapToastReference
	if !errors.As(err, &reference) || reference.Relation != 4294967295 {
		t.Fatal("physical content boundary", err)
	}
}
