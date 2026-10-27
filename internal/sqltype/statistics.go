package sqltype

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func IsStatisticsType(id ID) bool {
	return id == PgNDistinct || id == PgDependencies || id == PgMCVList
}

// statisticsPayload owns the released serialization, excluding varlena headers.
// Strings freeze caller buffers and remain shareable across compatible retags.
// The human-readable output is compiled once from checked construction facts;
// neither output nor send reparses the serialized representation.
type statisticsPayload struct {
	kind            ID
	binary, display string
}

// StatisticsItem is one checked construction input. Value is an ndistinct
// count or dependency degree. Ndistinct dimensions retain canonical selection
// order: increasing positive keys followed by expression identities -1,-2,... .
// Dependencies use the same signed dimensions, with the dependent attribute last.
// Construction retains no caller-owned slices.
type StatisticsItem struct {
	Attributes []int16
	Value      float64
}

// StatisticsValue constructs a genuine immutable pg_ndistinct or
// pg_dependencies datum for internal producers, without granting SQL input.
func StatisticsValue(d *Descriptor, items []StatisticsItem) (Value, error) {
	if d == nil || !d.Concrete() || d.id != PgNDistinct && d.id != PgDependencies || len(items) == 0 || len(items) > 65535 {
		return Value{}, fmt.Errorf("invalid statistics construction")
	}
	magic := uint32(0xA352BFA4)
	if d.id == PgDependencies {
		magic = 0xB4549A2C
	}
	data := binary.LittleEndian.AppendUint32(nil, magic)
	data = binary.LittleEndian.AppendUint32(data, 1)
	data = binary.LittleEndian.AppendUint32(data, uint32(len(items)))
	var out strings.Builder
	out.WriteByte('{')
	for i, item := range items {
		if len(item.Attributes) < 2 || len(item.Attributes) > 8 || math.IsNaN(item.Value) || math.IsInf(item.Value, 0) || item.Value < 0 || d.id == PgDependencies && item.Value > 1 || d.id == PgNDistinct && item.Value > math.MaxInt32 {
			return Value{}, fmt.Errorf("invalid statistics item")
		}
		for j, a := range item.Attributes {
			if a == 0 || a < -8 {
				return Value{}, fmt.Errorf("invalid statistics attribute")
			}
			if d.id == PgNDistinct && j > 0 {
				prior := item.Attributes[j-1]
				ordered := prior > 0 && (a < 0 || prior < a) || prior < 0 && a < 0 && prior > a
				if !ordered {
					return Value{}, fmt.Errorf("unordered statistics dimensions")
				}
			}
			for _, prior := range item.Attributes[:j] {
				if a == prior {
					return Value{}, fmt.Errorf("duplicate statistics attribute")
				}
			}
		}
		data = binary.LittleEndian.AppendUint64(data, math.Float64bits(item.Value))
		if d.id == PgNDistinct {
			data = binary.LittleEndian.AppendUint32(data, uint32(len(item.Attributes)))
		} else {
			data = binary.LittleEndian.AppendUint16(data, uint16(len(item.Attributes)))
		}
		if i != 0 {
			out.WriteString(", ")
		}
		out.WriteByte('"')
		for j, a := range item.Attributes {
			data = binary.LittleEndian.AppendUint16(data, uint16(a))
			if j > 0 {
				if d.id == PgDependencies && j == len(item.Attributes)-1 {
					out.WriteString(" => ")
				} else {
					out.WriteString(", ")
				}
			}
			out.WriteString(strconv.Itoa(int(a)))
		}
		out.WriteString(`": `)
		if d.id == PgNDistinct {
			out.WriteString(strconv.FormatInt(int64(item.Value), 10))
		} else {
			out.WriteString(strconv.FormatFloat(item.Value, 'f', 6, 64))
		}
	}
	out.WriteByte('}')
	return Value{typ: d, statistics: &statisticsPayload{kind: d.id, binary: string(data), display: out.String()}}, nil
}

// StatisticsMCVValue validates and freezes the released linux/amd64 serialized
// pg_mcv_list payload. It neither collects data nor enables SQL receive/input.
func StatisticsMCVValue(d *Descriptor, data []byte) (Value, error) {
	invalid := func() (Value, error) { return Value{}, fmt.Errorf("invalid serialized MCV list") }
	if d == nil || !d.Concrete() || d.id != PgMCVList || len(data) < 14 {
		return invalid()
	}
	u32 := func(at int) uint32 { return binary.LittleEndian.Uint32(data[at:]) }
	if u32(0) != 0xE1A651C2 || u32(4) != 1 {
		return invalid()
	}
	n, dims := int(u32(8)), int(binary.LittleEndian.Uint16(data[12:]))
	if n < 1 || n > 10000 || dims < 1 || dims > 8 {
		return invalid()
	}
	header := 14 + 24*dims
	if len(data) < header {
		return invalid()
	}
	var counts [8]int
	valuesEnd := header
	for i := range dims {
		if u32(14+4*i) == 0 {
			return invalid()
		}
		at := 14 + 4*dims + 20*i
		count, size, aligned, length := int64(int32(u32(at))), int64(int32(u32(at+4))), int64(int32(u32(at+8))), int32(u32(at+12))
		if count < 0 || count > 65535 || size < 0 || aligned < 0 || aligned%8 != 0 || length == 0 || length < -2 || data[at+16] > 1 || data[at+17] != 0 || data[at+18] != 0 || data[at+19] != 0 {
			return invalid()
		}
		// REL_18_6 statext_mcv_serialize copies by-value data into items,
		// requiring no deserialized allocation. By-reference values have a
		// separate allocation extent, not necessarily >= serialized bytes.
		if data[at+16] == 1 && (length != 1 && length != 2 && length != 4 && length != 8 || aligned != 0) {
			return invalid()
		}
		if length > 0 && size != count*int64(length) || count == 0 && size != 0 || size > int64(len(data)-valuesEnd) {
			return invalid()
		}
		if length < 0 {
			cursor, end := valuesEnd, valuesEnd+int(size)
			var restored int64
			for range int(count) {
				if end-cursor < 4 {
					return invalid()
				}
				width := int64(u32(cursor))
				cursor += 4
				if width > int64(end-cursor) || length == -2 && (width == 0 || data[cursor+int(width)-1] != 0) {
					return invalid()
				}
				cursor += int(width)
				allocation := width
				if length == -1 {
					allocation += 4
				}
				restored += (allocation + 7) &^ 7
			}
			if cursor != end || restored != aligned {
				return invalid()
			}
		} else if data[at+16] == 0 && aligned != count*((int64(length)+7)&^7) {
			return invalid()
		}
		counts[i] = int(count)
		valuesEnd += int(size)
	}
	itemSize := 3*dims + 16
	if n > (len(data)-valuesEnd)/itemSize || valuesEnd+n*itemSize != len(data) {
		return invalid()
	}
	for item := range n {
		at := valuesEnd + item*itemSize
		for i := range dims {
			if data[at+i] > 1 {
				return invalid()
			}
		}
		for _, off := range []int{dims, dims + 8} {
			f := math.Float64frombits(binary.LittleEndian.Uint64(data[at+off:]))
			if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1 {
				return invalid()
			}
		}
		for i := range dims {
			index := int(binary.LittleEndian.Uint16(data[at+dims+16+2*i:]))
			if data[at+i] == 0 && index >= counts[i] {
				return invalid()
			}
		}
	}
	return Value{typ: d, statistics: &statisticsPayload{kind: d.id, binary: string(data)}}, nil
}
func (v Value) validStatistics() bool {
	return v.typ != nil && v.statistics != nil && v.statistics.kind == v.typ.BaseType().id && IsStatisticsType(v.statistics.kind)
}
func (d *Descriptor) statisticsOutput(ctx context.Context, v Value, env OutputEnvironment) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !v.validStatistics() {
		return "", fmt.Errorf("invalid statistics payload")
	}
	if d.id == PgMCVList {
		return byteaOutput(v.statistics.binary, env.ByteaOutput)
	}
	return v.statistics.display, nil
}

// StatisticsSend reuses the immutable serialized payload as a BYTEA datum.
// It grants no input constructor or binary receive capability.
func (v Value) StatisticsSend(ctx context.Context, result *Descriptor) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if !v.validStatistics() || v.IsNull() {
		return Value{}, fmt.Errorf("invalid statistics send value")
	}
	return Binary(result, v.statistics.binary)
}
