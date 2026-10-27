package sqltype

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// REL_18_6 mcv.c statext_mcv_serialize: one dimension, one value, one item.
// DimensionInfo starts at 18; data at 38. No production sizing helper is used.
func statisticsMCVExtentVector(oid uint32, length int32, byValue bool, allocation uint32, data []byte) []byte {
	b := make([]byte, 38+len(data)+19)
	binary.LittleEndian.PutUint32(b, 0xE1A651C2)
	binary.LittleEndian.PutUint32(b[4:], 1)
	binary.LittleEndian.PutUint32(b[8:], 1)
	binary.LittleEndian.PutUint16(b[12:], 1)
	binary.LittleEndian.PutUint32(b[14:], oid)
	binary.LittleEndian.PutUint32(b[18:], 1)
	binary.LittleEndian.PutUint32(b[22:], uint32(len(data)))
	binary.LittleEndian.PutUint32(b[26:], allocation)
	binary.LittleEndian.PutUint32(b[30:], uint32(length))
	if byValue {
		b[34] = 1
	}
	copy(b[38:], data)
	item := 38 + len(data)
	binary.LittleEndian.PutUint64(b[item+1:], math.Float64bits(1))
	binary.LittleEndian.PutUint64(b[item+9:], math.Float64bits(1))
	return b
}

func TestStatisticsTypeMCVDimensionExtents(t *testing.T) {
	d := testTypes()[PgMCVList]
	for _, tc := range []struct {
		name       string
		oid        uint32
		length     int32
		byValue    bool
		allocation uint32
		data       []byte
	}{
		{"char_by_value", 18, 1, true, 0, []byte{'x'}},
		{"int2_by_value", 21, 2, true, 0, []byte{1, 0}},
		{"int4_by_value", 23, 4, true, 0, []byte{1, 0, 0, 0}},
		{"int8_by_value", 20, 8, true, 0, []byte{1, 0, 0, 0, 0, 0, 0, 0}},
		{"uuid_by_reference", 2950, 16, false, 16, make([]byte, 16)},
		{"text_varlena", 25, -1, false, 8, []byte{3, 0, 0, 0, 'a', 'b', 'c'}},
		{"text_varlena_aligned", 25, -1, false, 16, []byte{7, 0, 0, 0, 'a', 'b', 'c', 'd', 'e', 'f', 'g'}},
		// Serialized cstring length includes its terminator and a uint32 prefix;
		// allocated size omits that prefix and can be smaller than serialized size.
		{"cstring", 2275, -2, false, 8, []byte{8, 0, 0, 0, 'a', 'b', 'c', 'd', 'e', 'f', 'g', 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := statisticsMCVExtentVector(tc.oid, tc.length, tc.byValue, tc.allocation, tc.data)
			frozen := bytes.Clone(input)
			v, err := StatisticsMCVValue(d, input)
			if err != nil {
				t.Fatal("released dimension rejected", err)
			}
			input[38] ^= 0xff
			sent, err := d.AppendBinaryWithEnvironment(t.Context(), nil, v, OutputEnvironment{})
			if err != nil || !bytes.Equal(sent, frozen) {
				t.Fatal("dimension backing not frozen", sent, err)
			}
			for _, edit := range []func([]byte){
				func(b []byte) { binary.LittleEndian.PutUint32(b[26:], tc.allocation+8) },
				func(b []byte) { binary.LittleEndian.PutUint32(b[18:], 2) },
				func(b []byte) { binary.LittleEndian.PutUint32(b[22:], uint32(len(tc.data)+1)) },
				func(b []byte) { binary.LittleEndian.PutUint32(b[30:], 0) },
				func(b []byte) { b[len(b)-2] = 1 }, // item value index exceeds the one-value bag
			} {
				bad := bytes.Clone(frozen)
				edit(bad)
				if _, err := StatisticsMCVValue(d, bad); err == nil {
					t.Fatal("invalid dimension extent accepted", bad)
				}
			}
			if tc.byValue {
				bad := bytes.Clone(frozen)
				binary.LittleEndian.PutUint32(bad[30:], 3)
				if _, err := StatisticsMCVValue(d, bad); err == nil {
					t.Fatal("invalid by-value width accepted")
				}
			} else if tc.length < 0 {
				bad := bytes.Clone(frozen)
				binary.LittleEndian.PutUint32(bad[38:], uint32(len(tc.data)))
				if _, err := StatisticsMCVValue(d, bad); err == nil {
					t.Fatal("variable value exceeded serialized extent")
				}
				if tc.length == -2 {
					bad = bytes.Clone(frozen)
					bad[38+len(tc.data)-1] = 'x'
					if _, err := StatisticsMCVValue(d, bad); err == nil {
						t.Fatal("unterminated cstring accepted")
					}
				}
			}
		})
	}
}
