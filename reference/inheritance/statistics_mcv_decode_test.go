package reference_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

// This reader describes the released linux/amd64 physical image, not COPY wire
// values. It intentionally uses no stuntgres codec or internal package.
type mcvDimension struct {
	oid    uint32
	length int
	byval  bool
	values [][]byte
}
type mcvItem struct {
	values              [][]byte
	nulls               []bool
	frequency, baseline float64
}
type mcvImage struct {
	dimensions []mcvDimension
	items      []mcvItem
}

func decodeMCV(t *testing.T, data []byte) mcvImage {
	t.Helper()
	offset := 0
	take := func(n int) []byte {
		if n < 0 || n > len(data)-offset {
			t.Fatalf("truncated MCV at %d needing %d of %d", offset, n, len(data))
		}
		v := data[offset : offset+n]
		offset += n
		return v
	}
	u32 := func() uint32 { return binary.LittleEndian.Uint32(take(4)) }
	if u32() != 0xe1a651c2 || u32() != 1 {
		t.Fatal("MCV magic/version")
	}
	count := int(u32())
	width := int(binary.LittleEndian.Uint16(take(2)))
	if count < 1 || count > 10000 || width < 1 || width > 8 {
		t.Fatal("MCV bounds", count, width)
	}
	out := mcvImage{dimensions: make([]mcvDimension, width)}
	for i := range out.dimensions {
		out.dimensions[i].oid = u32()
	}
	type extent struct{ count, size, aligned int }
	extents := make([]extent, width)
	for i := range out.dimensions {
		e := &extents[i]
		e.count = int(u32())
		e.size = int(u32())
		e.aligned = int(u32())
		d := &out.dimensions[i]
		d.length = int(int32(u32()))
		flag := take(1)[0]
		if flag > 1 {
			t.Fatal("byval flag", flag)
		}
		d.byval = flag == 1
		// PostgreSQL need not initialize C struct padding; it is not semantic data.
		take(3)
	}
	align := func(n int) int { return (n + 7) &^ 7 }
	for i := range out.dimensions {
		d := &out.dimensions[i]
		e := extents[i]
		start := offset
		aligned := 0
		if e.count < 0 || e.count > 10000 {
			t.Fatal("dictionary size", e.count)
		}
		for range e.count {
			n := d.length
			if n < 0 {
				n = int(u32())
			}
			v := take(n)
			d.values = append(d.values, v)
			if !d.byval {
				if d.length == -1 {
					aligned += align(n + 4)
				} else {
					aligned += align(n)
				}
			}
			if d.length == -2 && (n == 0 || v[n-1] != 0) {
				t.Fatal("unterminated cstring")
			}
		}
		if offset-start != e.size || aligned != e.aligned {
			t.Fatalf("dimension %d extents: %d/%d want %d/%d", i, offset-start, aligned, e.size, e.aligned)
		}
	}
	for i := range count {
		item := mcvItem{values: make([][]byte, width), nulls: make([]bool, width)}
		for j := range item.nulls {
			b := take(1)[0]
			if b > 1 {
				t.Fatal("NULL flag", b)
			}
			item.nulls[j] = b == 1
		}
		item.frequency = math.Float64frombits(binary.LittleEndian.Uint64(take(8)))
		item.baseline = math.Float64frombits(binary.LittleEndian.Uint64(take(8)))
		if !(item.frequency > 0 && item.frequency <= 1 && item.baseline > 0 && item.baseline <= 1) {
			t.Fatal("invalid frequencies", item)
		}
		for j := range item.values {
			index := int(binary.LittleEndian.Uint16(take(2)))
			if !item.nulls[j] {
				if index >= len(out.dimensions[j].values) {
					t.Fatal("dictionary index", index)
				}
				item.values[j] = out.dimensions[j].values[index]
			}
		}
		if i > 0 && out.items[i-1].frequency < item.frequency {
			t.Fatal("frequency order")
		}
		out.items = append(out.items, item)
	}
	if offset != len(data) {
		t.Fatal("MCV trailing bytes", len(data)-offset)
	}
	return out
}

const mcvSelect = "SELECT pg_mcv_list_send(d.stxdmcv) FROM pg_statistic_ext s JOIN pg_statistic_ext_data d ON d.stxoid=s.oid WHERE s.stxname=$1 AND d.stxdinherit=$2"

func readMCV(t *testing.T, s ndistinctScenario, name string, inherited bool) ([]byte, mcvImage) {
	t.Helper()
	var data, direct []byte
	if err := s.conn.QueryRowContext(s.pair.Ctx, mcvSelect, name, inherited).Scan(&direct); err != nil {
		t.Fatal(err)
	}
	stmt, err := s.conn.PrepareContext(s.pair.Ctx, mcvSelect)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if err := stmt.QueryRowContext(s.pair.Ctx, name, inherited).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(direct, data) {
		t.Fatal("direct/prepared MCV differs")
	}
	if len(data) == 0 {
		t.Fatal("missing MCV", name)
	}
	return data, decodeMCV(t, data)
}
func intMCVKey(item mcvItem) string {
	key := ""
	for i, v := range item.values {
		if i > 0 {
			key += ","
		}
		if item.nulls[i] {
			key += "NULL"
		} else if len(v) == 4 {
			key += fmt.Sprint(int32(binary.LittleEndian.Uint32(v)))
		} else {
			key += fmt.Sprintf("%x", v)
		}
	}
	return key
}

type mcvExpected struct {
	key                 string
	frequency, baseline float64
}

func assertIntMCV(t *testing.T, image mcvImage, expected ...mcvExpected) {
	t.Helper()
	if len(image.items) != len(expected) {
		t.Fatalf("MCV items %d want %d: %+v", len(image.items), len(expected), image.items)
	}
	for _, want := range expected {
		found := false
		for _, got := range image.items {
			if intMCVKey(got) == want.key {
				found = true
				if math.Abs(got.frequency-want.frequency) > 1e-14 || math.Abs(got.baseline-want.baseline) > 1e-14 {
					t.Fatalf("%s frequencies %g/%g want %g/%g", want.key, got.frequency, got.baseline, want.frequency, want.baseline)
				}
			}
		}
		if !found {
			t.Fatal("missing tuple", want.key)
		}
	}
}

var mcvFour = []mcvExpected{{"1,1", .5, .25}, {"2,2", .25, .0625}, {"NULL,NULL", .25, .0625}}
var mcvFive = []mcvExpected{{"1,1", .4, .16}, {"2,2", .4, .16}, {"NULL,NULL", .2, .04}}

func mcvAssert(t *testing.T, s ndistinctScenario, name string, want ...mcvExpected) {
	t.Helper()
	_, v := readMCV(t, s, name, false)
	assertIntMCV(t, v, want...)
}
func mcvSame(t *testing.T, a, b []byte) {
	t.Helper()
	if !bytes.Equal(a, b) {
		t.Fatal("retained MCV image changed")
	}
}
