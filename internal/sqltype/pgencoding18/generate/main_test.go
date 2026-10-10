package main

import (
	"reflect"
	"testing"
)

func rejects(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("malformed input was accepted")
		}
	}()
	f()
}
func TestBinaryEncodingGeneratorIntegers(t *testing.T) {
	if got := ints("{0x80, /* source row */ 0xFF, 17, 0}"); !reflect.DeepEqual(got, []uint32{128, 255, 17, 0}) {
		t.Fatal(got)
	}
	for _, s := range []string{"{0x100000000}", "{UNKNOWN}", "{1 << 8}", "{-1}", "{1.5}"} {
		t.Run(s, func(t *testing.T) { rejects(t, func() { ints(s) }) })
	}
}
func TestBinaryEncodingGeneratorBounds(t *testing.T) {
	// Four descriptors, using the release's root/bounds field order.
	descriptor := []uint32{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	validateTree([]uint32{0}, descriptor)
	bad := append([]uint32(nil), descriptor...)
	bad[0] = 1
	rejects(t, func() { validateTree([]uint32{0}, bad) })
	bad = append([]uint32(nil), descriptor...)
	bad[1] = 1
	rejects(t, func() { validateTree([]uint32{0}, bad) })
	bad = append([]uint32(nil), descriptor...)
	bad[2] = 256
	rejects(t, func() { validateTree([]uint32{0}, bad) })
	rejects(t, func() { validateTree([]uint32{2}, descriptor) })
}
func TestBinaryEncodingGeneratorDeclarations(t *testing.T) {
	for _, s := range []string{
		"static const uint16 t[2] = {0x00,0x01};",
		"static const unsigned short t[][2] = {{1,2}};",
		"static const struct { unsigned short first; unsigned short second; int third; } t[] = {{1,2,3}};",
	} {
		if len(arrays.FindAllStringSubmatch(s, -1)) != 1 {
			t.Fatal("declaration not parsed", s)
		}
	}
}
