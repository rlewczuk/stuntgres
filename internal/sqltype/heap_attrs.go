package sqltype

import (
	"context"
	"fmt"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

// HeapStoredColumn is a physical slot, including dropped attributes. It contains
// no codec, catalog authority or embedded row data.
type HeapStoredColumn struct {
	Length    int16
	Alignment uint8
}

func HeapColumnFacts(f PostgreSQLAttributeFacts) (HeapStoredColumn, error) {
	alignment := uint8(0)
	switch f.Alignment {
	case "c":
		alignment = 1
	case "s":
		alignment = 2
	case "i":
		alignment = 4
	case "d":
		alignment = 8
	default:
		return HeapStoredColumn{}, nativeLayoutError()
	}
	return HeapStoredColumn{Length: f.Length, Alignment: alignment}, nil
}

// HeapTupleLayout can only be produced after source-compatible bitmap admission.
// This admission precedes relation opening; deformation follows schema capture.
type HeapTupleLayout struct {
	natts   uint16
	bitmap  string
	hasNull bool
}

func heapAttrsError(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func ReadHeapTupleLayout(mask, mask2 uint16, bits *string) (HeapTupleLayout, error) {
	layout := HeapTupleLayout{natts: mask2 & 0x7ff, hasNull: mask&1 != 0}
	if !layout.hasNull {
		if bits != nil {
			return layout, heapAttrsError("XX001", fmt.Sprintf("t_bits string is expected to be NULL, but instead it is %d bytes long", len(*bits)))
		}
		return layout, nil
	}
	if bits == nil {
		return layout, heapAttrsError("XX001", "t_bits string must not be NULL")
	}
	width := (int(layout.natts) + 7) / 8 * 8
	if len(*bits) != width {
		return layout, heapAttrsError("XX001", fmt.Sprintf("unexpected length of t_bits string: %d, expected %d", len(*bits), width))
	}
	for at := 0; at < len(*bits); {
		r, size := utf8.DecodeRuneInString((*bits)[at:])
		if r != '0' && r != '1' {
			return layout, heapAttrsError("XX001", fmt.Sprintf("invalid character \"%s\" in t_bits string", (*bits)[at:at+size]))
		}
		at += size
	}
	layout.bitmap = *bits
	return layout, nil
}

// HeapToastReference names absent physical content, not a fabricated datum.
// The engine checks relation existence using ordinary locked capture before
// reporting the unsupported physical-content boundary for an existing relation.
type HeapToastReference struct{ Relation uint32 }

func (e *HeapToastReference) Error() string {
	return "stuntgres: supplied heap datum references unavailable physical TOAST content"
}

func (layout HeapTupleLayout) Split(ctx context.Context, raw string, detoast bool, columns []HeapStoredColumn, array *Descriptor) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if int(layout.natts) > len(columns) {
		return Value{}, heapAttrsError("XX001", "number of attributes in tuple header is greater than number of attributes in tuple descriptor")
	}
	if array == nil || !array.IsArray() || array.Element().ID() != Bytea {
		return Value{}, nativeLayoutError()
	}
	values := make([]Value, len(columns))
	offset := 0
	// REL_18_6 tuple_data_split_internal receives a uint16 data length.
	length := int(uint16(len(raw)))
	for i, column := range columns {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if i >= int(layout.natts) || layout.hasNull && layout.bitmap[i] == '0' {
			values[i] = Null(array.Element())
			continue
		}
		alignment := int(column.Alignment)
		if alignment != 1 && alignment != 2 && alignment != 4 && alignment != 8 {
			return Value{}, nativeLayoutError()
		}
		if column.Length != -1 || offset >= len(raw) || raw[offset] == 0 {
			offset = (offset + alignment - 1) &^ (alignment - 1)
		}
		size := int(column.Length)
		if column.Length == -1 {
			if offset >= len(raw) {
				return Value{}, nativeLayoutError()
			}
			if raw[offset]&1 != 0 {
				size = int(raw[offset] >> 1)
				if size == 0 {
					if len(raw)-offset < 2 {
						return Value{}, nativeLayoutError()
					}
					switch raw[offset+1] {
					case 18:
						size = 18
					case 1:
						size = 10
					default:
						return Value{}, heapAttrsError("XX001", fmt.Sprintf("first byte of varlena attribute is incorrect for attribute %d", i))
					}
				}
			} else {
				if len(raw)-offset < 4 {
					return Value{}, nativeLayoutError()
				}
				size = int(native32(raw[offset:]) >> 2)
				if size < 4 {
					return Value{}, nativeLayoutError()
				}
			}
		} else if size < 0 {
			return Value{}, nativeLayoutError()
		}
		if offset > length || size > length-offset {
			return Value{}, heapAttrsError("XX001", "unexpected end of tuple data")
		}
		data := raw[offset : offset+size]
		if column.Length == -1 && detoast {
			var err error
			data, err = heapDetoast(data)
			if err != nil {
				return Value{}, err
			}
		}
		var err error
		values[i], err = Binary(array.Element(), data)
		if err != nil {
			return Value{}, err
		}
		offset += size
	}
	if length != offset {
		return Value{}, heapAttrsError("XX001", "end of tuple reached without looking at all its data")
	}
	var dimensions []ArrayDimension
	if len(values) != 0 {
		dimensions = []ArrayDimension{{Lower: 1, Length: int32(len(values))}}
	}
	return OwnedArrayValue(array, dimensions, values)
}

func heapDetoast(raw string) (string, error) {
	if raw[0]&1 != 0 {
		if raw[0] != 1 {
			return raw[1:], nil
		}
		if raw[1] == 18 {
			return "", &HeapToastReference{Relation: native32(raw[14:])}
		}
		return "", nativeLayoutError()
	}
	if raw[0]&3 == 0 {
		return raw[4:], nil
	}
	if len(raw) < 8 {
		return "", nativeLayoutError()
	}
	info := native32(raw[4:])
	size, method := int(info&0x3fffffff), info>>30
	if method > 1 {
		return "", heapAttrsError("XX000", fmt.Sprintf("invalid compression method id %d", method))
	}
	input := raw[8:]
	var output []byte
	var err error
	if size > len(input)*273 {
		err = nativeLayoutError()
	} else if method == 0 {
		output, err = nativePGLZ(input, size)
	} else {
		output, err = nativeLZ4(input, size)
	}
	if err != nil {
		name := "pglz"
		if method == 1 {
			name = "lz4"
		}
		return "", heapAttrsError("XX001", "compressed "+name+" data is corrupt")
	}
	return string(output), nil
}
