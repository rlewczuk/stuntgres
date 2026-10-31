package enginetests

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"testing"
	"unsafe"
)

func heapUnitImage() []byte {
	raw := make([]byte, 68)
	binary.LittleEndian.PutUint16(raw[12:], 28)
	binary.LittleEndian.PutUint32(raw[24:], 32|1<<15|36<<17)
	for _, v := range []struct {
		at int
		n  uint32
	}{{32, 0x89abcdef}, {36, 0xfedcba98}, {40, 0xfffffffe}, {60, 0xfedcba98}} {
		binary.LittleEndian.PutUint32(raw[v.at:], v.n)
	}
	for _, v := range []struct {
		at int
		n  uint16
	}{{44, 1}, {46, 2}, {48, 513}, {50, 0x8009}, {52, 0x0309}} {
		binary.LittleEndian.PutUint16(raw[v.at:], v.n)
	}
	raw[54], raw[55], raw[56] = 32, 0x81, 1
	copy(raw[64:], []byte{0, 128, 255, 92})
	return raw
}
func heapUnitRead(t *testing.T, raw string) sqltype.HeapPage {
	t.Helper()
	p, err := sqltype.ReadHeapPage(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func heapUnitItem(t *testing.T, p sqltype.HeapPage) sqltype.HeapPageItem {
	t.Helper()
	v, err := p.Item(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestPageinspectHeapDecode(t *testing.T) {
	want := sqltype.HeapPageItem{LP: 1, Offset: 32, Flags: 1, Length: 36, Header: true, Tail: true, HasBits: true, HasOID: true, Xmin: 2309737967, Xmax: 4275878552, Field3: -2, CTIDBlock: 65538, CTIDOffset: 513, Infomask2: 32777, Infomask: 777, Hoff: 32, Bits: "1000000110000000", OID: 4275878552, Data: "\x00\x80\xff\\"}
	raw := heapUnitImage()
	immutable := string(raw)
	p := heapUnitRead(t, immutable)
	clear(raw)
	if p.Len() != 1 {
		t.Fatal("slot count", p.Len())
	}
	got := heapUnitItem(t, p)
	if got != want {
		t.Fatal(got, want)
	}
	if unsafe.StringData(got.Data) != unsafe.StringData(immutable[64:]) {
		t.Fatal("data was copied instead of borrowing immutable input")
	}
	got.Data = "mutated"
	got.Bits = "mutated"
	if heapUnitItem(t, p) != want {
		t.Fatal("item mutation contaminated page")
	}
	for _, tc := range []struct {
		name   string
		mutate func([]byte)
		change func(*sqltype.HeapPageItem)
	}{
		{"no_version_gate", func(r []byte) {
			binary.LittleEndian.PutUint16(r[18:], 0xffff)
			binary.LittleEndian.PutUint16(r[16:], 1)
		}, func(*sqltype.HeapPageItem) {}},
		{"upper_zero", func(r []byte) { binary.LittleEndian.PutUint16(r[14:], 0) }, func(*sqltype.HeapPageItem) {}},
		{"unused_storage", func(r []byte) { binary.LittleEndian.PutUint32(r[24:], 32|36<<17) }, func(v *sqltype.HeapPageItem) { v.Flags = 0 }},
		{"redirect_storage", func(r []byte) { binary.LittleEndian.PutUint32(r[24:], 32|2<<15|36<<17) }, func(v *sqltype.HeapPageItem) { v.Flags = 2 }},
		{"dead_storage", func(r []byte) { binary.LittleEndian.PutUint32(r[24:], 32|3<<15|36<<17) }, func(v *sqltype.HeapPageItem) { v.Flags = 3 }},
		{"no_optional", func(r []byte) { binary.LittleEndian.PutUint16(r[52:], 0) }, func(v *sqltype.HeapPageItem) {
			v.Infomask = 0
			v.HasBits = false
			v.HasOID = false
			v.Bits = ""
			v.OID = 0
		}},
		{"zero_natts", func(r []byte) { binary.LittleEndian.PutUint16(r[50:], 0x8000) }, func(v *sqltype.HeapPageItem) { v.Infomask2 = 32768; v.Bits = "" }},
		{"bitmap_too_wide", func(r []byte) { binary.LittleEndian.PutUint16(r[50:], 0x87ff) }, func(v *sqltype.HeapPageItem) { v.Infomask2 = 34815; v.HasBits = false; v.Bits = "" }},
		{"empty_data", func(r []byte) { binary.LittleEndian.PutUint32(r[24:], 32|1<<15|32<<17) }, func(v *sqltype.HeapPageItem) { v.Length = 32; v.Data = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := heapUnitImage()
			tc.mutate(raw)
			expected := want
			tc.change(&expected)
			got := heapUnitItem(t, heapUnitRead(t, string(raw)))
			if got != expected {
				t.Fatal(got, expected)
			}
		})
	}
	for _, hoff := range []byte{0, 23, 25, 40} {
		raw := heapUnitImage()
		raw[54] = hoff
		v := heapUnitItem(t, heapUnitRead(t, string(raw)))
		expected := want
		expected.Hoff = hoff
		expected.Tail = false
		expected.HasBits = false
		expected.HasOID = false
		expected.Bits = ""
		expected.OID = 0
		expected.Data = ""
		if v != expected {
			t.Fatal("tail eligibility", hoff, v, expected)
		}
	}
	for _, tc := range []struct{ off, length uint16 }{{32, 0}, {32, 23}, {33, 35}, {40, 36}} {
		raw := heapUnitImage()
		binary.LittleEndian.PutUint32(raw[24:], uint32(tc.off)|1<<15|uint32(tc.length)<<17)
		v := heapUnitItem(t, heapUnitRead(t, string(raw)))
		expected := sqltype.HeapPageItem{LP: 1, Offset: tc.off, Flags: 1, Length: tc.length}
		if v != expected {
			t.Fatal("storage eligibility", v, expected)
		}
	}
	for _, lower := range []uint16{0, 24, 25, 27, 28, 31} {
		raw := heapUnitImage()
		binary.LittleEndian.PutUint16(raw[12:], lower)
		wantCount := 0
		if lower >= 28 {
			wantCount = 1
		}
		if heapUnitRead(t, string(raw)).Len() != wantCount {
			t.Fatal("lower rounding", lower)
		}
	}
	// Header-only reads and pointer validation borrow bytes without allocation.
	noBitmap := heapUnitImage()
	binary.LittleEndian.PutUint16(noBitmap[52:], 0)
	text := string(noBitmap)
	plain := heapUnitRead(t, text)
	if a, z := util.AllocationsPerCall(64, func() {
		if _, err := sqltype.ReadHeapPage(text); err != nil {
			panic(err)
		}
		if _, err := plain.Item(context.Background(), 0); err != nil {
			panic(err)
		}
	}); a != 0 || z != 0 {
		t.Fatal("reader allocation", a, z)
	}
}
func TestPageinspectHeapSafety(t *testing.T) {
	for _, n := range []int{0, 1, 23} {
		p, err := sqltype.ReadHeapPage(string(make([]byte, n)))
		headerUnitError(t, err, fmt.Sprintf("input page too small (%d bytes)", n), "")
		if p.Len() != 0 {
			t.Fatal("partial page")
		}
	}
	for _, n := range []int{24, 25, 8192, 8193} {
		if p := heapUnitRead(t, string(make([]byte, n))); p.Len() != 0 {
			t.Fatal("empty image", n)
		}
	}
	for _, n := range []int{24, 25, 27, 28, 31} {
		raw := make([]byte, n)
		binary.LittleEndian.PutUint16(raw[12:], uint16(24+4*((n-24)/4+1)))
		p, err := sqltype.ReadHeapPage(string(raw))
		if err == nil || err.Error() != "stuntgres: unsafe PostgreSQL native datum layout" || p.Len() != 0 {
			t.Fatal("truncated pointer array", n, p, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("invented SQLSTATE", err)
		}
	}
	p := heapUnitRead(t, string(heapUnitImage()))
	for _, slot := range []int{-1, 1, 16384} {
		v, err := p.Item(context.Background(), slot)
		if err == nil || v != (sqltype.HeapPageItem{}) {
			t.Fatal("unsafe slot", slot, v, err)
		}
	}
	var zero sqltype.HeapPage
	if v, err := zero.Item(context.Background(), 0); err == nil || v != (sqltype.HeapPageItem{}) {
		t.Fatal("zero view", v, err)
	}
}
func TestPageinspectHeapCancellation(t *testing.T) {
	raw := heapUnitImage()
	binary.LittleEndian.PutUint16(raw[12:], 32)
	binary.LittleEndian.PutUint32(raw[28:], 32|1<<15|36<<17)
	page := heapUnitRead(t, string(raw))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v, err := page.Item(ctx, 0)
	if !errors.Is(err, context.Canceled) || v != (sqltype.HeapPageItem{}) {
		t.Fatal("canceled item", v, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	if _, err := page.Item(ctx, 0); err != nil {
		t.Fatal(err)
	}
	cancel()
	v, err = page.Item(ctx, 1)
	if !errors.Is(err, context.Canceled) || v != (sqltype.HeapPageItem{}) {
		t.Fatal("between-slot cancellation", v, err)
	}
	if _, err := page.Item(context.Background(), 1); err != nil {
		t.Fatal("cancellation changed page", err)
	}
}
func TestPageinspectHeapFlags(t *testing.T) {
	names := []string{"HEAP_HASNULL", "HEAP_HASVARWIDTH", "HEAP_HASEXTERNAL", "HEAP_HASOID_OLD", "HEAP_XMAX_KEYSHR_LOCK", "HEAP_COMBOCID", "HEAP_XMAX_EXCL_LOCK", "HEAP_XMAX_LOCK_ONLY", "HEAP_XMIN_COMMITTED", "HEAP_XMIN_INVALID", "HEAP_XMAX_COMMITTED", "HEAP_XMAX_INVALID", "HEAP_XMAX_IS_MULTI", "HEAP_UPDATED", "HEAP_MOVED_OFF", "HEAP_MOVED_IN", "HEAP_KEYS_UPDATED", "HEAP_HOT_UPDATED", "HEAP_ONLY_TUPLE"}
	for i, name := range names {
		first, second := int32(0), int32(0)
		if i < 16 {
			first = 1 << uint(i)
		} else {
			second = 0x2000 << uint(i-16)
		}
		v := sqltype.DecodeHeapInfomaskFlags(first, second)
		if v.RawCount != 1 || v.Raw[0] != name || v.CombinedCount != 0 {
			t.Fatal("single bit", i, v)
		}
	}
	for _, tc := range []struct {
		first, second int32
		raw, combined []string
	}{
		{0, 0, []string{}, []string{}}, {65536, 65536, []string{}, []string{}}, {-2147483648, 8191, []string{}, []string{}},
		{65537, 0, []string{"HEAP_HASNULL"}, []string{}},
		{80, 0, []string{"HEAP_XMAX_KEYSHR_LOCK", "HEAP_XMAX_EXCL_LOCK"}, []string{"HEAP_XMAX_SHR_LOCK"}},
		{768, 0, []string{"HEAP_XMIN_COMMITTED", "HEAP_XMIN_INVALID"}, []string{"HEAP_XMIN_FROZEN"}},
		{49152, 0, []string{"HEAP_MOVED_OFF", "HEAP_MOVED_IN"}, []string{"HEAP_MOVED"}},
		{-1, -1, names, []string{"HEAP_XMAX_SHR_LOCK", "HEAP_XMIN_FROZEN", "HEAP_MOVED"}},
		{2147483647, 2147483647, names, []string{"HEAP_XMAX_SHR_LOCK", "HEAP_XMIN_FROZEN", "HEAP_MOVED"}},
	} {
		v := sqltype.DecodeHeapInfomaskFlags(tc.first, tc.second)
		if !reflect.DeepEqual(v.Raw[:v.RawCount], tc.raw) || !reflect.DeepEqual(v.Combined[:v.CombinedCount], tc.combined) {
			t.Fatal(tc, v)
		}
	}
	v := sqltype.DecodeHeapInfomaskFlags(-1, -1)
	v.Raw[0] = "mutated"
	if sqltype.DecodeHeapInfomaskFlags(-1, -1).Raw[0] != "HEAP_HASNULL" {
		t.Fatal("shared mutable flag backing")
	}
	if a, z := util.AllocationsPerCall(64, func() {
		v := sqltype.DecodeHeapInfomaskFlags(-1, -1)
		if v.RawCount != 19 {
			panic("flags")
		}
	}); a != 0 || z != 0 {
		t.Fatal("flag helper allocated", a, z)
	}
}
