package largeobject

import (
	"math"
	"strings"
	"testing"
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
