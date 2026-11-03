package sqltype

import (
	"reflect"
	"testing"
)

// descriptorFieldCount counts the fields of t, including fields of nested
// structs, that hold a *Descriptor.
func descriptorFieldCount(t reflect.Type) int {
	n := 0
	for i := range t.NumField() {
		switch f := t.Field(i).Type; {
		case f == reflect.TypeFor[*Descriptor]():
			n++
		case f.Kind() == reflect.Struct:
			n += descriptorFieldCount(f)
		}
	}
	return n
}

// descriptorTargets appends the non-nil *Descriptor fields of v.
func descriptorTargets(v reflect.Value, out []uintptr) []uintptr {
	switch {
	case v.Type() == reflect.TypeFor[*Descriptor]():
		if !v.IsNil() {
			out = append(out, v.Pointer())
		}
	case v.Kind() == reflect.Struct:
		for i := range v.NumField() {
			out = descriptorTargets(v.Field(i), out)
		}
	}
	return out
}

// Builtins copies its template: each call is a new generation equal to one
// built from the compiled tables, whose descriptor pointers stay inside it.
func TestBuiltinsCopyTemplate(t *testing.T) {
	if n := descriptorFieldCount(reflect.TypeFor[Descriptor]()); n != len((&Descriptor{}).descriptorPointers()) {
		t.Fatalf("Descriptor has %d descriptor pointer fields; descriptorPointers lists %d", n, len((&Descriptor{}).descriptorPointers()))
	}
	first, second := Builtins(), Builtins()
	if !reflect.DeepEqual(first, buildBuiltins()) {
		t.Fatal("a copied generation differs from a built one")
	}
	own := make(map[uintptr]bool, len(first))
	for i, d := range first {
		if d == second[i] || d == builtins().types[i] {
			t.Fatal("generations share a descriptor", d.Name())
		}
		own[reflect.ValueOf(d).Pointer()] = true
	}
	for _, d := range first {
		for _, target := range descriptorTargets(reflect.ValueOf(*d), nil) {
			if !own[target] {
				t.Fatalf("%s points outside its generation", d.Name())
			}
		}
	}
	byID := make(map[ID]*Descriptor, len(first))
	for _, d := range first {
		byID[d.id] = d
	}
	vector := byID[TSVector]
	if vector.ftsReal != byID[Float4] || vector.ftsRealArray != byID[1021] || vector.ftsConfig != byID[Regconfig] || vector.ftsRealArray.element != byID[Float4] || byID[Regconfig].ftsVector != vector || byID[1021].ftsVector != vector {
		t.Fatal("ranking/headline companions escaped the cloned generation")
	}
}

// Generation peers are the symmetric closure of builtin descriptor links.
func TestBuiltinGenerationPeers(t *testing.T) {
	linked := func(a, b ID) bool {
		for _, id := range BuiltinGenerationPeers(a) {
			if id == b {
				return true
			}
		}
		return false
	}
	for _, d := range builtins().types {
		for _, p := range d.descriptorPointers() {
			if target := *p; target != nil && target != d && (!linked(d.id, target.id) || !linked(target.id, d.id)) {
				t.Fatalf("%s and %s are not generation peers", d.Name(), target.Name())
			}
		}
		for _, id := range BuiltinGenerationPeers(d.id) {
			if id == d.id || !linked(id, d.id) {
				t.Fatalf("%s peer %d is a self or one-way link", d.Name(), id)
			}
		}
	}
	for _, pair := range [][2]ID{{TSVector, TSQuery}, {TSVector, Text}, {TSQuery, Bool}, {Bool, Point}, {Int4Range, Bool}, {Int4, 1007}} {
		if !linked(pair[0], pair[1]) {
			t.Fatal("missing generation peer", pair)
		}
	}
	if linked(TSVector, Int8) {
		t.Fatal("unlinked builtins became peers")
	}
}
