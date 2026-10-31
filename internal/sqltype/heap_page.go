package sqltype

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
)

// HeapPage borrows immutable caller bytes; it does not validate page identity or visibility.
type HeapPage struct {
	raw   string
	count int
}

func ReadHeapPage(raw string) (HeapPage, error) {
	if len(raw) < 24 {
		return HeapPage{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf("input page too small (%d bytes)", len(raw))}
	}
	count := 0
	if lower := int(native16(raw[12:])); lower > 24 {
		count = (lower - 24) / 4
	}
	// Upstream can read outside the allocation here. Never expose a partial page.
	if count > (len(raw)-24)/4 {
		return HeapPage{}, nativeLayoutError()
	}
	return HeapPage{raw: raw, count: count}, nil
}
func (p HeapPage) Len() int { return p.count }

type HeapPageItem struct {
	LP, Offset, Flags, Length       uint16
	Header, Tail, HasBits, HasOID   bool
	Xmin, Xmax                      uint32
	Field3                          int32
	CTIDBlock                       uint32
	CTIDOffset, Infomask2, Infomask uint16
	Hoff                            uint8
	Bits, Data                      string
	OID                             uint32
}

// Item decodes a zero-based slot, preserving header fields when the tail is invalid.
func (p HeapPage) Item(ctx context.Context, slot int) (HeapPageItem, error) {
	if err := ctx.Err(); err != nil {
		return HeapPageItem{}, err
	}
	if slot < 0 || slot >= p.count {
		return HeapPageItem{}, nativeLayoutError()
	}
	word := native32(p.raw[24+slot*4:])
	v := HeapPageItem{LP: uint16(slot + 1), Offset: uint16(word & 0x7fff), Flags: uint16(word >> 15 & 3), Length: uint16(word >> 17)}
	off, length := int(v.Offset), int(v.Length)
	if length < 24 || off&7 != 0 || off > len(p.raw)-length {
		return v, nil
	}
	raw := p.raw[off : off+length]
	v.Header = true
	v.Xmin = native32(raw)
	v.Xmax = native32(raw[4:])
	v.Field3 = int32(native32(raw[8:]))
	v.CTIDBlock = uint32(native16(raw[12:]))<<16 | uint32(native16(raw[14:]))
	v.CTIDOffset = native16(raw[16:])
	v.Infomask2 = native16(raw[18:])
	v.Infomask = native16(raw[20:])
	v.Hoff = raw[22]
	hoff := int(v.Hoff)
	if hoff < 23 || hoff > length || hoff&7 != 0 {
		return v, nil
	}
	v.Tail = true
	v.Data = raw[hoff:]
	if v.Infomask&1 != 0 {
		n := (int(v.Infomask2&0x07ff) + 7) / 8
		if n <= hoff-23 {
			v.HasBits = true
			if n != 0 {
				var bits strings.Builder
				bits.Grow(n * 8)
				for i := range n * 8 {
					bits.WriteByte('0' + (raw[23+i/8] >> uint(i%8) & 1))
				}
				v.Bits = bits.String()
			}
		}
	}
	if v.Infomask&8 != 0 {
		v.HasOID = true
		v.OID = native32(raw[hoff-4:])
	}
	return v, nil
}

// HeapInfomaskFlags retains only static names, avoiding intermediate slices.
type HeapInfomaskFlags struct {
	Raw                     [19]string
	Combined                [3]string
	RawCount, CombinedCount int
}

var heapRawFlags = [16]string{"HEAP_HASNULL", "HEAP_HASVARWIDTH", "HEAP_HASEXTERNAL", "HEAP_HASOID_OLD", "HEAP_XMAX_KEYSHR_LOCK", "HEAP_COMBOCID", "HEAP_XMAX_EXCL_LOCK", "HEAP_XMAX_LOCK_ONLY", "HEAP_XMIN_COMMITTED", "HEAP_XMIN_INVALID", "HEAP_XMAX_COMMITTED", "HEAP_XMAX_INVALID", "HEAP_XMAX_IS_MULTI", "HEAP_UPDATED", "HEAP_MOVED_OFF", "HEAP_MOVED_IN"}
var heapSecondFlags = [3]string{"HEAP_KEYS_UPDATED", "HEAP_HOT_UPDATED", "HEAP_ONLY_TUPLE"}

func DecodeHeapInfomaskFlags(first, second int32) (v HeapInfomaskFlags) {
	mask, mask2 := uint16(first), uint16(second)
	for i, name := range heapRawFlags {
		if mask&(1<<uint(i)) != 0 {
			v.Raw[v.RawCount] = name
			v.RawCount++
		}
	}
	for i, name := range heapSecondFlags {
		if mask2&(0x2000<<uint(i)) != 0 {
			v.Raw[v.RawCount] = name
			v.RawCount++
		}
	}
	for i, bits := range [...]uint16{0x0050, 0x0300, 0xc000} {
		if mask&bits == bits {
			v.Combined[v.CombinedCount] = [...]string{"HEAP_XMAX_SHR_LOCK", "HEAP_XMIN_FROZEN", "HEAP_MOVED"}[i]
			v.CombinedCount++
		}
	}
	return v
}
