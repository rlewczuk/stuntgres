package largeobject

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"stuntgres/internal/sqlstate"
)

func TestLargeObjectSparseReadWrite(t *testing.T) {
	var value Value
	var err error
	value, err = value.Write(0, "abcde")
	if err != nil {
		t.Fatal(err)
	}
	value, err = value.Write(1, "\x00\xff")
	if err != nil {
		t.Fatal(err)
	}
	value, err = value.Write(8, "z")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		offset, length int64
		want           string
	}{
		{0, -1, "a\x00\xffde\x00\x00\x00z"}, {4, 20, "e\x00\x00\x00z"}, {9, 2, ""}, {20, 2, ""}, {0, 0, ""},
	} {
		got, err := value.Read(tc.offset, tc.length)
		if err != nil || got != tc.want {
			t.Fatalf("Read(%d,%d)=%x,%v want%x", tc.offset, tc.length, got, err, tc.want)
		}
	}
	value, err = value.Write(2047, "\x00\x80\xff")
	if err != nil {
		t.Fatal(err)
	}
	got, err := value.Read(2044, 10)
	if err != nil || got != "\x00\x00\x00\x00\x80\xff" {
		t.Fatalf("cross-page read%x,%v", got, err)
	}
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
	before := value
	value, err = value.Write(MaximumSize, "")
	if err != nil || value.size != before.size || len(value.pages) != len(before.pages) {
		t.Fatal("empty write extended sparse value", err)
	}
}

func TestLargeObjectImmutablePages(t *testing.T) {
	var empty Value
	first, err := empty.Write(0, "abc")
	if err != nil {
		t.Fatal(err)
	}
	second, err := first.Write(PageSize+1, "\x80\x00")
	if err != nil {
		t.Fatal(err)
	}
	if &first.pages[0][0] != &second.pages[0][0] {
		t.Fatal("untouched page was copied")
	}
	third, err := second.Write(1, "z")
	if err != nil {
		t.Fatal(err)
	}
	if &second.pages[0][0] == &third.pages[0][0] {
		t.Fatal("touched page was shared")
	}
	if &second.pages[1][0] != &third.pages[1][0] {
		t.Fatal("unmodified sparse page was copied")
	}
	if got, _ := first.Read(0, -1); got != "abc" {
		t.Fatalf("old snapshot changed%x", got)
	}
	if got, _ := third.Read(0, 3); got != "azc" {
		t.Fatalf("overwrite%x", got)
	}
	if empty.size != 0 || empty.pages != nil {
		t.Fatal("zero value mutated")
	}
	input := []byte{0, 128, 255}
	owned, err := empty.Write(0, string(input))
	if err != nil {
		t.Fatal(err)
	}
	input[1] = 1
	if got, _ := owned.Read(0, -1); got != "\x00\x80\xff" {
		t.Fatalf("input alias%x", got)
	}
}

func TestLargeObjectSizeBounds(t *testing.T) {
	var value Value
	value, err := value.Write(4294967296, "foo")
	if err != nil {
		t.Fatal(err)
	}
	if len(value.pages) != 1 {
		t.Fatal("sparse gap materialized")
	}
	if got, err := value.Read(4294967280, 19); err != nil || got != strings.Repeat("\x00", 16)+"foo" {
		t.Fatalf("high sample%x,%v", got, err)
	}
	assertCode := func(err error, want string) {
		t.Helper()
		d, ok := err.(interface{ SQLState() string })
		if !ok || d.SQLState() != want {
			t.Fatalf("error%v want%s", err, want)
		}
	}
	_, err = value.Read(0, -1)
	assertCode(err, "54000")
	_, err = value.Read(math.MinInt64, math.MaxInt32)
	assertCode(err, "54000")
	_, err = value.Read(math.MinInt64, 0)
	assertCode(err, "22023")
	value, err = value.Write(MaximumSize-1, "z")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := value.Read(MaximumSize-2, 2); err != nil || got != "\x00z" {
		t.Fatalf("maximum sample%x,%v", got, err)
	}
	_, err = value.Write(MaximumSize, "z")
	assertCode(err, "22023")
	for _, offset := range []int64{-1, MaximumSize + 1, math.MaxInt64} {
		_, err = value.Write(offset, "")
		assertCode(err, "22023")
	}
}

func TestLargeObjectTruncate(t *testing.T) {
	for _, length := range []int64{0, 1, 2047, 2048, 2049, 2147483647, 2147483648, MaximumSize} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			var empty Value
			value, err := empty.Truncate(length)
			if err != nil { t.Fatal(err) }
			if value.size != length || len(value.pages) != 1 { t.Fatal("endpoint materialized holes", value.size, len(value.pages)) }
			page, within := length/PageSize, length%PageSize
			data, exists := value.pages[page]
			if !exists || string(data) != strings.Repeat("\x00", int(within)) { t.Fatal("wrong physical endpoint", page, data) }
			if err := value.Validate(); err != nil { t.Fatal(err) }
			if empty.pages != nil || empty.size != 0 { t.Fatal("zero value mutated") }
			offset := max(int64(0), length-2)
			got, err := value.Read(offset, 4)
			if err != nil || got != strings.Repeat("\x00", int(length-offset)) { t.Fatal("bounded tail", got, err) }
			if got, err := value.Read(length, 1); err != nil || got != "" { t.Fatal("EOF", got, err) }
			same, err := value.Truncate(length)
			if err != nil || same.size != length || len(same.pages) != 1 { t.Fatal("equal length changed physical shape", err) }
			if within != 0 && &same.pages[page][0] != &data[0] { t.Fatal("equal endpoint copied immutable payload") }
		})
	}
	base, err := (Value{}).Write(0, "abc")
	if err != nil { t.Fatal(err) }
	base, err = base.Write(PageSize, "XYZ")
	if err != nil { t.Fatal(err) }
	base, err = base.Write(4*PageSize, "tail")
	if err != nil { t.Fatal(err) }
	short, err := base.Truncate(PageSize+2)
	if err != nil { t.Fatal(err) }
	if len(short.pages) != 2 || string(short.pages[1]) != "XY" || &short.pages[0][0] != &base.pages[0][0] || &short.pages[1][0] == &base.pages[1][0] { t.Fatal("truncate did not copy only endpoint") }
	if string(base.pages[1]) != "XYZ" || string(base.pages[4]) != "tail" || len(base.pages) != 3 || base.size != 4*PageSize+4 { t.Fatal("old value mutated") }
	short.pages[2] = nil
	if _, exists := base.pages[2]; exists { t.Fatal("replacement borrowed mutable page index") }
	delete(short.pages, 2)
	reused, err := base.Truncate(PageSize+3)
	if err != nil || &reused.pages[1][0] != &base.pages[1][0] || len(reused.pages) != 2 { t.Fatal("exact immutable endpoint was copied or doomed page retained", err) }
	extended, err := short.Truncate(PageSize+5)
	if err != nil || string(extended.pages[1]) != "XY\x00\x00\x00" || string(short.pages[1]) != "XY" || &extended.pages[1][0] == &short.pages[1][0] { t.Fatal("discarded suffix resurrected or endpoint aliased", err) }
	if err := extended.Validate(); err != nil { t.Fatal(err) }

	full, err := (Value{}).Write(0, strings.Repeat("z", int(PageSize)))
	if err != nil { t.Fatal(err) }
	aligned, err := full.Truncate(PageSize)
	if err != nil { t.Fatal(err) }
	if len(full.pages) != 1 || len(aligned.pages) != 2 || &full.pages[0][0] != &aligned.pages[0][0] { t.Fatal("equal aligned EOF skipped endpoint or copied lower page") }
	if data, exists := aligned.pages[1]; !exists || len(data) != 0 { t.Fatal("missing aligned endpoint") }
	var interior Value
	interior, err = interior.Truncate(PageSize)
	if err != nil { t.Fatal(err) }
	interior, err = interior.Truncate(2*PageSize)
	if err != nil { t.Fatal(err) }
	interior, err = interior.Write(4*PageSize, "Z")
	if err != nil { t.Fatal(err) }
	if len(interior.pages) != 3 { t.Fatal("lost interior empty pages") }
	for _, index := range []int64{1, 2} {
		if data, exists := interior.pages[index]; !exists || len(data) != 0 { t.Fatal("interior endpoint missing", index) }
	}
	if err := interior.Validate(); err != nil { t.Fatal(err) }
	maximum, err := (Value{}).Truncate(MaximumSize)
	if err != nil { t.Fatal(err) }
	maximum, err = maximum.Write(0, "abc")
	if err != nil || len(maximum.pages) != 2 || maximum.size != MaximumSize { t.Fatal("write lost maximum empty endpoint", err) }
	if err := maximum.Validate(); err != nil { t.Fatal(err) }
	zero, err := maximum.Truncate(0)
	if err != nil || zero.size != 0 || len(zero.pages) != 1 { t.Fatal("zero did not delete all later pages", err) }
	if _, exists := zero.pages[0]; !exists { t.Fatal("zero endpoint absent") }
	if err := zero.Validate(); err != nil { t.Fatal(err) }

	for _, length := range []int64{-1, math.MinInt32, math.MinInt64, MaximumSize+1, math.MaxInt64} {
		failed, err := base.Truncate(length)
		diagnostic, ok := err.(*sqlstate.Error)
		if !ok || diagnostic.Code != "22023" || diagnostic.Message != fmt.Sprintf("invalid large object truncation target: %d", length) || diagnostic.Severity != "ERROR" || diagnostic.SeverityUnlocalized != "ERROR" { t.Fatal("truncate range diagnostic", length, err) }
		if failed.pages != nil || failed.size != 0 || len(base.pages) != 3 || string(base.pages[1]) != "XYZ" { t.Fatal("failed truncate published or changed source") }
	}
	for _, malformed := range []Value{
		{size:-1}, {size:MaximumSize+1},
		{pages:map[int64][]byte{-1:nil}},
		{size:MaximumSize, pages:map[int64][]byte{math.MaxInt32+1:nil}},
		{size:MaximumSize, pages:map[int64][]byte{math.MaxInt32:{1}}},
		{size:PageSize+1, pages:map[int64][]byte{0:make([]byte, PageSize+1)}},
		{size:1, pages:map[int64][]byte{1:nil}},
		{size:2, pages:map[int64][]byte{0:{1}}},
		{size:PageSize, pages:map[int64][]byte{0:nil}},
	} {
		if err := malformed.Validate(); err == nil { t.Fatal("accepted malformed sparse value", malformed.size, malformed.pages) }
	}
	if err := (Value{}).Validate(); err != nil { t.Fatal("zero value rejected", err) }
}

func TestLargeObjectSize(t *testing.T) {
	var empty Value
	if empty.Size() != 0 { t.Fatal("zero EOF", empty.Size()) }
	first, err := empty.Write(0, "abc")
	if err != nil { t.Fatal(err) }
	if first.Size() != 3 || empty.Size() != 0 { t.Fatal("write EOF mutated old value") }
	sparse, err := first.Write(4294967296, "foo")
	if err != nil { t.Fatal(err) }
	if sparse.Size() != 4294967299 || first.Size() != 3 || len(sparse.pages) != 2 || &sparse.pages[0][0] != &first.pages[0][0] { t.Fatal("sparse EOF copied lower pages or allocated holes") }
	for _, length := range []int64{0, 2047, 2048, 2049, 2147483647, 2147483648, MaximumSize} {
		next, err := sparse.Truncate(length)
		if err != nil || next.Size() != length { t.Fatal("truncate EOF", length, next.Size(), err) }
		if sparse.Size() != 4294967299 || len(sparse.pages) != 2 || string(sparse.pages[2097152]) != "foo" || string(first.pages[0]) != "abc" { t.Fatal("old sparse value mutated") }
		if length >= 2048 && &next.pages[0][0] != &sparse.pages[0][0] { t.Fatal("size/truncate copied untouched page") }
	}
	// Size borrows no chunks and allocates nothing, even for a sparse ceiling.
	if allocations := testing.AllocsPerRun(100, func() {
		if sparse.Size() != 4294967299 { panic("unstable EOF") }
	}); allocations != 0 { t.Fatal("Size allocated", allocations) }
}
